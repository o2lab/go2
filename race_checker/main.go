package main

import (
	"flag"
	"fmt"
	"go/token"
	"go/types"
	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/pointer"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
	"sort"

	aurora "github.com/logrusorgru/aurora"
	log "github.com/sirupsen/logrus"
)

// chanOp abstracts an ssa.Send, ssa.Unop(ARROW), or a SelectState.
type chanOp struct {
	ch  ssa.Value
	dir types.ChanDir // SendOnly=send, RecvOnly=recv, SendRecv=close
	pos token.Pos
}

type analysis struct {
	prog          *ssa.Program
	pkgs          []*ssa.Package
	mains         []*ssa.Package
	result        *pointer.Result
	chanOps       []chanOp
	ptaConfig     *pointer.Config
	fn2SummaryMap map[*ssa.Function]*fnSummary
	goID2insMap map[int]*ssa.Go
	bb2SyncBlockListMap map[*ssa.BasicBlock][]*SyncBlock
}

type fnSummary struct {
	accesses []accessInfo
	fromGoroutines []int
}

type accessInfo struct {
	write bool
	atomic bool
	location ssa.Value
	instruction *ssa.Instruction
	parent *fnSummary
	bb *ssa.BasicBlock
	index int // index in BasicBlock
	//lockset []*ssa.Instruction // list of protecting locks
	//preChanOp []chanOp // list of chan ops before the access
	//postChanOp []chanOp // list of chan ops after the access
}

type SyncBlock struct {
	bb *ssa.BasicBlock
	//instruction *ssa.Instruction
	start, end int // start and end index in bb, excluding the instruction at index end
}

func newSyncBlock(bb *ssa.BasicBlock) *SyncBlock {
	return &SyncBlock{
		bb:    bb,
		start: 0,
		end:   -1,
	}
}

func newSyncBlockFrom(bb *ssa.BasicBlock, block *SyncBlock, index int) *SyncBlock {
	block.end = index
	return &SyncBlock{
		bb:    bb,
		start: index,
		end:   -1,
	}
}

// chanOps returns a slice of all the channel operations in the instruction.
func chanOps(instr ssa.Instruction) []chanOp {
	var ops []chanOp
	switch instr := instr.(type) {
	case *ssa.UnOp:
		if instr.Op == token.ARROW {
			ops = append(ops, chanOp{instr.X, types.RecvOnly, instr.Pos()})
		}
	case *ssa.Send:
		ops = append(ops, chanOp{instr.Chan, types.SendOnly, instr.Pos()})
	case *ssa.Select:
		for _, st := range instr.States {
			ops = append(ops, chanOp{st.Chan, st.Dir, st.Pos})
		}
	case ssa.CallInstruction:
		cc := instr.Common()
		if b, ok := cc.Value.(*ssa.Builtin); ok && b.Name() == "close" {
			ops = append(ops, chanOp{cc.Args[0], types.SendRecv, cc.Pos()})
		}
	}
	return ops
}

var (
	Analysis *analysis
)

func (a *analysis) getLastSyncBlock(bb *ssa.BasicBlock) *SyncBlock {
	syncBlocks, ok := a.bb2SyncBlockListMap[bb]
	if ok && len(syncBlocks) > 0 {
		return syncBlocks[len(syncBlocks) - 1]
	}
	return nil
}

func (a *analysis) appendSyncBlock(bb *ssa.BasicBlock, sblock *SyncBlock) {
	syncBlocks := a.bb2SyncBlockListMap[bb]
	a.bb2SyncBlockListMap[bb] = append(syncBlocks, sblock)
}

func (a *analysis) visitInstr(bb *ssa.BasicBlock, index int, instruction ssa.Instruction, isLast bool) {
	if !instruction.Pos().IsValid() {
		return // Skip NoPos. Can such instruction take part in a race?
	}
	switch instruction.(type) {
	case *ssa.Alloc:
	case *ssa.UnOp:
		instr := instruction.(*ssa.UnOp)
		// read op
		if instr.Op == token.MUL {
			a.addAccessInfo(&instruction, instr.X, false, false, instruction.Parent(), index, bb)
		}
	case *ssa.Store:
		// write op
		instr := instruction.(*ssa.Store)
		a.addAccessInfo(&instruction, instr.Addr, true, false, instruction.Parent(), index, bb)
	case *ssa.Go:
		//instr := instruction.(*ssa.Go)
		sblock := a.getLastSyncBlock(bb)
		if sblock == nil {
			log.Fatal("No last SyncBlock")
		}
		if !isLast {
			a.appendSyncBlock(bb, newSyncBlockFrom(bb, sblock, index))
		} else {
			sblock.end = index
		}
	// TODO: process locks, chan ops
	}
}

func (a *analysis) addAccessInfo(ins *ssa.Instruction, location ssa.Value, write bool, atomic bool, parent *ssa.Function, index int, bb *ssa.BasicBlock) accessInfo {
	summary := a.fn2SummaryMap[parent]
	info := accessInfo{
		write: write,
		atomic: atomic,
		location: location,
		instruction: ins,
		parent: summary,
		index: index,
		bb: bb,
	}
	summary.accesses = append(summary.accesses, info)
	a.ptaConfig.AddQuery(location)

	return info
}

func fromMainPkg(fn *ssa.Function) bool {
	return fn.Pkg != nil && fn.Pkg.Pkg != nil && fn.Pkg.Pkg.Path() == "command-line-arguments"
}

func (a *analysis) visitInstrs() {
	for fn := range ssautil.AllFunctions(a.prog) {
		if !fromMainPkg(fn) || fn.Name() == "init" {
			continue
		}
		if _, ok := a.fn2SummaryMap[fn]; !ok {
			a.fn2SummaryMap[fn] = &fnSummary{
				accesses:       nil,
				fromGoroutines: nil,
			}
		}
		log.Debug(fn)
		for bi, b := range fn.Blocks {
			log.Debug("  --", b)
			isLast := bi == len(fn.Blocks) - 1
			for index, instr := range b.Instrs {
				log.Debug("    --", instr)
				if _, ok := a.bb2SyncBlockListMap[b]; !ok {
					a.bb2SyncBlockListMap[b] = []*SyncBlock{newSyncBlock(b)}
				}
				a.visitInstr(b, index, instr, isLast)
				for _, op := range chanOps(instr) {
					a.chanOps = append(a.chanOps, op)
					a.ptaConfig.AddQuery(op.ch)
				}
			}
		}
	}
}

func isSynthetic(edge *callgraph.Edge) bool {
	return edge.Caller.Func.Pkg == nil || edge.Callee.Func.Synthetic != ""
}

func contains(s []int, v int) bool {
	for _, a := range s {
		if a == v {
			return true
		}
	}
	return false
}

func (a *analysis) addNewGoroutineIDs(function *ssa.Function, ids ...int) {
	summary, ok := a.fn2SummaryMap[function]
	if !ok {
		log.Fatal("Summary not found:", function)
	}
	for _, id := range ids {
		if !contains(summary.fromGoroutines, id) {
			summary.fromGoroutines = append(summary.fromGoroutines, id)
		}
	}
}

func GraphVisitEdgesPreorder(g *callgraph.Graph, edge func(*callgraph.Edge) error) error {
	seen := make(map[*callgraph.Node]bool)
	var visit func(n *callgraph.Node) error
	visit = func(n *callgraph.Node) error {
		if !seen[n] {
			seen[n] = true
			for _, e := range n.Out {
				if err := edge(e); err != nil {
					return err
				}
				if err := visit(e.Callee); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, n := range g.Nodes {
		if err := visit(n); err != nil {
			return err
		}
	}
	return nil
}

func (a *analysis) getFromGoroutines(function *ssa.Function) []int {
	if summary, ok := a.fn2SummaryMap[function]; ok {
		return summary.fromGoroutines
	}
	return nil
}

func (a *analysis) sameAddress(addr1 *ssa.Value, addr2 *ssa.Value) bool {
	// check if both are the same global
	if global1, ok1 := (*addr1).(*ssa.Global); ok1 {
		if global2, ok2 := (*addr2).(*ssa.Global); ok2 {
			return global1.Pos() == global2.Pos()
		}
	}

	// check if they can point to the same obj
	ptset := a.result.Queries
	pt1Labels := ptset[*addr1].PointsTo().Labels()
	pt2Labels := ptset[*addr2].PointsTo().Labels()
	for _, l1 := range pt1Labels {
		for _, l2 := range pt2Labels {
			if l1.Value() == l2.Value() {
				return true
			}
		}
	}
	return false
}

// check if access1 is po-ordered to some go ins, which is po-ordered to access2
func (a *analysis) checkPO(minAcc *accessInfo, maxAcc *accessInfo) bool {
	sum2 := maxAcc.parent
	for _, id := range sum2.fromGoroutines {
		goins := a.goID2insMap[id]
		if minAcc.bb == goins.Block() {
			// check if goins is in minAcc.bb, after minAcc.index
			for i := minAcc.index + 1; i < len(minAcc.bb.Instrs); i++ {
				if minAcc.bb.Instrs[i] == goins {
					return true
				}
			}
		}
	}
	return false
}

func (a *analysis) getConflictingAccesses(sum1 *fnSummary, sum2 *fnSummary) [][2]accessInfo {
	var res[][2]accessInfo
	if parallel, _, _ := canRunInParallel(sum1, sum2, nil, nil); !parallel {
		return res
	}
	for _, acc1 := range sum1.accesses {
		for _, acc2 := range sum2.accesses {
			if (acc1.write || acc2.write) &&
				(!acc1.atomic || !acc2.atomic) &&
				a.sameAddress(&acc1.location, &acc2.location) {
				if _, minIDAcc, maxIDAcc := canRunInParallel(sum1, sum2, &acc1, &acc2); !a.checkPO(minIDAcc, maxIDAcc) {
					res = append(res, [2]accessInfo{*minIDAcc, *maxIDAcc})
				}
			}
		}
	}
	return res
}

// two functions can run in parallel iff their fromGoroutines slices do not equal
// return the summary with the min goroutine ID as 2nd return val
func canRunInParallel(summary1 *fnSummary, summary2 *fnSummary, info1 *accessInfo, info2 *accessInfo) (bool, *accessInfo, *accessInfo) {
	if len(summary1.fromGoroutines) != len(summary2.fromGoroutines) {
		if len(summary1.fromGoroutines) == 0 {
			return true, info1, info2
		}
		if len(summary2.fromGoroutines) == 0 {
			return true, info2, info1
		}
		if summary1.fromGoroutines[0] < summary2.fromGoroutines[0] {
			return true, info1, info2
		} else {
			return true, info2, info1
		}
	}
	for i := 0; i < len(summary1.fromGoroutines); i++ {
		if summary1.fromGoroutines[i] < summary2.fromGoroutines[i] {
			return true, info1, info2
		} else if summary1.fromGoroutines[i] > summary2.fromGoroutines[i] {
			return true, info2, info1
		}
	}
	return false, nil, nil
}

func (a *analysis) getConflictingPairs() ([][2]*ssa.Function, [][2]accessInfo) {
	cg := a.result.CallGraph
	goID := 0
	err := GraphVisitEdgesPreorder(cg, func(edge *callgraph.Edge) error {
		if isSynthetic(edge) {
			return nil
		}
		caller := edge.Caller
		if !fromMainPkg(caller.Func) && caller.Func.Name() != "panic" {
			return nil
		}
		callee := edge.Callee
		site := edge.Site

		if caller.Func.Name() == "main" && a.getFromGoroutines(caller.Func) == nil {
			a.addNewGoroutineIDs(caller.Func, 0)
		}

		if !fromMainPkg(callee.Func) {
			return nil
		}

		if goIns, ok := site.(*ssa.Go); ok {
			goID++
			a.goID2insMap[goID] = goIns
			a.addNewGoroutineIDs(callee.Func, goID)
		} else {
			callerIDs := a.getFromGoroutines(caller.Func)
			if callerIDs == nil {
				a.addNewGoroutineIDs(callee.Func, 0)
			}
			a.addNewGoroutineIDs(callee.Func, callerIDs...)
		}

		return nil
	})
	if err != nil {
		log.Fatal(err)
	}

	for _, sum := range a.fn2SummaryMap {
		sort.Ints(sum.fromGoroutines)
	}

	functions := make([]*ssa.Function, 0, len(a.fn2SummaryMap))
	for fn := range a.fn2SummaryMap {
		functions = append(functions, fn)
	}
	var fnPairs [][2]*ssa.Function
	var accPairs[][2]accessInfo
	for i, fn1 := range functions {
		for j := 0; j < i; j++ {
			fn2 := functions[j]
			sum1 := a.fn2SummaryMap[fn1]
			sum2 := a.fn2SummaryMap[fn2]
			if conflictAccPairs := a.getConflictingAccesses(sum1, sum2); conflictAccPairs != nil {
				fnPairs = append(fnPairs, [2]*ssa.Function{fn1, fn2})
				accPairs = append(accPairs, conflictAccPairs...)
			}
		}
 	}
	return fnPairs, accPairs
}

func (a *analysis) doChannelPeers(ptsets map[ssa.Value]pointer.Pointer) {

}

func init() {
	log.SetLevel(log.DebugLevel)
}

func doAnalysis(args []string) error {
	cfg := &packages.Config{
		Mode:       packages.LoadAllSyntax,
		Dir:        "",
		Tests:      false,
	}

	initial, err := packages.Load(cfg, args...)
	if err != nil {
		return err
	}
	if packages.PrintErrors(initial) > 0 {
		return fmt.Errorf("packages contain errors")
	}

	// Print the names of the source files
	// for each package listed on the command line.
	//for _, pkg := range initial {
	//	fmt.Println(pkg.ID, pkg.GoFiles)
	//}

	// Create and build SSA-form program representation.
	prog, pkgs := ssautil.AllPackages(initial, 0)
	prog.Build()

	mains, err := mainPackages(pkgs)
	if err != nil {
		return err
	}
	config := &pointer.Config{
		Mains:          mains,
		BuildCallGraph: true,
	}

	Analysis = &analysis{
		prog:          prog,
		pkgs:          pkgs,
		mains:         mains,
		ptaConfig:	   config,
		fn2SummaryMap: make(map[*ssa.Function]*fnSummary),
		goID2insMap: make(map[int]*ssa.Go),
		bb2SyncBlockListMap:make(map[*ssa.BasicBlock][]*SyncBlock),
	}

	Analysis.visitInstrs()

	result, err := pointer.Analyze(config)
	if err != nil {
		return err // internal error in pointer analysis
	}
	result.CallGraph.DeleteSyntheticNodes()
	Analysis.result = result

	fnPairs, accPairs := Analysis.getConflictingPairs()
	log.Debug(fnPairs)
	Analysis.printAccPairs(accPairs)
	return nil
}

func (a *analysis) printAccPairs(accPairs [][2]accessInfo) {
	rwString := func(write bool) string {
		if write {
			return "Write"
		}
		return "Read"
	}
	for _, pair := range accPairs {
		ins1, ins2 := *pair[0].instruction, *pair[1].instruction
		log.Println("Data race:")
		log.Println("  ", rwString(pair[0].write),
			"of", aurora.Magenta(pair[0].location),
			"at", pair[0].bb.Parent().Name(), a.prog.Fset.Position(ins1.Pos()))
		log.Println("  ", rwString(pair[1].write),
			"of", aurora.Magenta(pair[1].location),
			"at", pair[1].bb.Parent().Name(), a.prog.Fset.Position(ins2.Pos()))
	}
}

func main() {
	flag.Parse()

	err := doAnalysis(flag.Args())
	if err != nil {
		log.Fatal(err)
	}
}

// mainPackages returns the main packages to analyze.
// Each resulting package is named "main" and has a main function.
func mainPackages(pkgs []*ssa.Package) ([]*ssa.Package, error) {
	var mains []*ssa.Package
	for _, p := range pkgs {
		if p != nil && p.Pkg.Name() == "main" && p.Func("main") != nil {
			mains = append(mains, p)
		}
	}
	if len(mains) == 0 {
		return nil, fmt.Errorf("no main packages")
	}
	return mains, nil
}