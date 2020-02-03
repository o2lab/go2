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
	"strings"

	aurora "github.com/logrusorgru/aurora"
	log "github.com/sirupsen/logrus"
)

// chanOp abstracts an ssa.Send, ssa.Unop(ARROW), or a SelectState.
type chanOp struct {
	ch  ssa.Value
	dir types.ChanDir // SendOnly=send, RecvOnly=recv, SendRecv=close
	pos token.Pos     // seems not used for now
}

type analysis struct {
	prog                *ssa.Program
	pkgs                []*ssa.Package
	mains               []*ssa.Package
	result              *pointer.Result
	chanOps             []chanOp
	ptaConfig           *pointer.Config
	fn2SummaryMap       map[*ssa.Function]*fnSummary
	goID2insMap         map[int]*ssa.Go
	bb2SyncBlockListMap map[*ssa.BasicBlock][]*SyncBlock
	analysisStat        stat
}

type stat struct {
	nAccess int
}

type fnSummary struct {
	accesses       []accessInfo
	fromGoroutines []int
}

type accessInfo struct {
	write       bool
	atomic      bool
	location    ssa.Value
	instruction *ssa.Instruction
	parentSum   *fnSummary
	bb          *ssa.BasicBlock
	index       int // index in BasicBlock
	//lockset []*ssa.Instruction // list of protecting locks
	//preChanOp []chanOp // list of chan ops before the access
	//postChanOp []chanOp // list of chan ops after the access
}

type SyncBlock struct {
	bb *ssa.BasicBlock
	//instruction *ssa.Instruction
	start, end int // start and end index in bb, excluding the instruction at index end
}

var (
	Analysis  *analysis
	focusPkgs []string
)

func (a *analysis) getLastSyncBlock(bb *ssa.BasicBlock) *SyncBlock {
	syncBlocks, ok := a.bb2SyncBlockListMap[bb]
	if ok && len(syncBlocks) > 0 {
		return syncBlocks[len(syncBlocks)-1]
	}
	return nil
}

func (a *analysis) generateSyncBlock(bb *ssa.BasicBlock, index int, isLast bool) {
	if isLast {
		return
	}
	sblock := a.getLastSyncBlock(bb)
	if sblock == nil {
		log.Fatal("No last SyncBlock")
	}
	sblock.end = index

	newSBlock := &SyncBlock{
		bb:    bb,
		start: index,
		end:   -1,
	}
	syncBlocks := a.bb2SyncBlockListMap[bb]
	a.bb2SyncBlockListMap[bb] = append(syncBlocks, newSBlock)
}

func IsSyncOp(instr *ssa.Instruction) bool {
	switch ins := (*instr).(type) {
	case *ssa.Send:
		return true
	case *ssa.UnOp:
		return ins.Op == token.ARROW
	case ssa.CallInstruction:
		return true
	case *ssa.Call:
		return false // TODO: except sync ops
	}
	return false
}

func (a *analysis) getSyncOpsBeforeAndAfter(acc accessInfo) (bef []ssa.Instruction,
	aft []ssa.Instruction) {
	bb := acc.bb
	for _, sb := range a.bb2SyncBlockListMap[bb] {
		instr := bb.Instrs[sb.end]
		if IsSyncOp(&instr) {
			if sb.end < acc.index {
				bef = append(bef, instr)
			} else if sb.end > acc.index {
				aft = append(aft, instr)
			}
		}
	}
	return
}

func (a *analysis) visitOneInstr(bb *ssa.BasicBlock, index int, instruction ssa.Instruction,
	isLast bool, allocated map[*ssa.Alloc]bool) {
	if !instruction.Pos().IsValid() {
		return // Skip NoPos. Can such instruction take part in a race?
	}
	switch instr := instruction.(type) {
	case *ssa.Alloc:
		allocated[instr] = true
	case *ssa.UnOp:
		// read by pointer-dereference
		if instr.Op == token.MUL {
			a.addAccessInfo(&instruction, instr.X, false, false, instruction.Parent(), index, bb, allocated)
			// chan recv
		} else if instr.Op == token.ARROW {
			a.generateSyncBlock(bb, index, isLast)
			a.chanOps = append(a.chanOps, chanOp{instr.X, types.RecvOnly, instr.Pos()})
		}
	case *ssa.MapUpdate:
		if locMap, ok := instr.Map.(*ssa.UnOp); ok {
			a.addAccessInfo(&instruction, locMap.X, true, false, instruction.Parent(), index, bb, allocated)
		}
	case *ssa.Store:
		// write op
		a.addAccessInfo(&instruction, instr.Addr, true, false, instruction.Parent(), index, bb, allocated)
	case *ssa.Go:
		for k := range allocated {
			delete(allocated, k)
		}
		a.generateSyncBlock(bb, index, isLast)
	case *ssa.Send:
		a.generateSyncBlock(bb, index, isLast)
		a.chanOps = append(a.chanOps, chanOp{instr.Chan, types.SendOnly, instr.Pos()})
	case *ssa.Select:
		a.generateSyncBlock(bb, index, isLast)
		for _, st := range instr.States {
			a.chanOps = append(a.chanOps, chanOp{st.Chan, st.Dir, st.Pos})
		}
	case ssa.CallInstruction:
		cc := instr.Common()
		// chan close
		if b, ok := cc.Value.(*ssa.Builtin); ok && b.Name() == "close" {
			a.generateSyncBlock(bb, index, isLast)
			a.chanOps = append(a.chanOps, chanOp{cc.Args[0], types.SendRecv, cc.Pos()})
		}
	case *ssa.Call:
		signalStr := instr.Call.Value.String()
		if strings.HasSuffix(signalStr, ").Lock") && len(instr.Call.Args) == 1 {
			a.generateSyncBlock(bb, index, isLast)
		}
		if strings.HasSuffix(signalStr, ").Unlock") && len(instr.Call.Args) == 1 {
			a.generateSyncBlock(bb, index, isLast)
		}
	case *ssa.Defer:
		signalStr := instr.Call.Value.String()
		if strings.HasSuffix(signalStr, ").Lock") && len(instr.Call.Args) == 1 {
			a.generateSyncBlock(bb, index, isLast)
		}
		if strings.HasSuffix(signalStr, ").Unlock") && len(instr.Call.Args) == 1 {
			a.generateSyncBlock(bb, index, isLast)
		}
	}
}

func (a *analysis) addAccessInfo(ins *ssa.Instruction, location ssa.Value, write bool, atomic bool, parent *ssa.Function,
	index int, bb *ssa.BasicBlock, allocated map[*ssa.Alloc]bool) {
	a.analysisStat.nAccess += 1
	switch loc := location.(type) {
	// Ignore checking access at alloc sites
	case *ssa.FieldAddr:
		if locX, ok := loc.X.(*ssa.Alloc); ok {
			if _, ok := allocated[locX]; ok {
				return
			}
		}
	case *ssa.IndexAddr:
		if locAlloc, ok := loc.X.(*ssa.Alloc); ok {
			if _, ok := allocated[locAlloc]; ok {
				return
			}
		} else if locCall, ok := loc.X.(*ssa.Call); ok {
			// Append call. Check if the slice is in alloc sites.
			if locCall.Call.Value.Name() == "append" {
				lastArg := locCall.Call.Args[len(locCall.Call.Args)-1]
				if locSlice, ok := lastArg.(*ssa.Slice); ok {
					if locAlloc, ok := locSlice.X.(*ssa.Alloc); ok {
						if _, ok := allocated[locAlloc]; ok {
							return
						}
					}
				}
			}
		}
	case *ssa.UnOp:
		if locUnOp, ok := loc.X.(*ssa.Alloc); ok {
			if _, ok := allocated[locUnOp]; ok {
				return
			}
		}
		if locFreeVar, ok := loc.X.(*ssa.FreeVar); ok {
			if _, ok := locFreeVar.Type().(*types.Pointer); ok {
				return
			}
		}
	case *ssa.Alloc:
		if _, ok := allocated[loc]; ok {
			return
		}
		//case *ssa.Global:
		//	if write {
		//		log.Debug(loc.Name())
		//	}
	}
	summary := a.fn2SummaryMap[parent]
	info := accessInfo{
		write:       write,
		atomic:      atomic,
		location:    location,
		instruction: ins,
		parentSum:   summary,
		index:       index,
		bb:          bb,
	}
	summary.accesses = append(summary.accesses, info)
	a.ptaConfig.AddQuery(location)

	return
}

func fromPkgsOfInterest(fn *ssa.Function) bool {
	if fn.Pkg == nil || fn.Pkg.Pkg == nil {
		return false
	}
	for _, path := range focusPkgs {
		if path != "" && strings.HasPrefix(fn.Pkg.Pkg.Path(), path) {
			return true
		}
	}
	return false
}

func (a *analysis) visitInstrs() {
	for fn := range ssautil.AllFunctions(a.prog) {
		if !fromPkgsOfInterest(fn) || fn.Name() == "init" || strings.HasPrefix(fn.Name(), "init#") {
			continue
		}
		if _, ok := a.fn2SummaryMap[fn]; !ok {
			a.fn2SummaryMap[fn] = &fnSummary{
				accesses:       nil,
				fromGoroutines: nil,
			}
		}
		log.Debug(fn)
		for _, b := range fn.Blocks {
			log.Debug("  --", b)
			// create a SyncBlockList with a default SyncBlock for b
			a.bb2SyncBlockListMap[b] = []*SyncBlock{&SyncBlock{
				bb:    b,
				start: 0,
				end:   -1,
			}}
			allocated := make(map[*ssa.Alloc]bool)
			// visit each instruction in b
			for index, instr := range b.Instrs {
				log.Debug("    --", instr, a.prog.Fset.Position(instr.Pos()))
				lastInsBB := index == (len(b.Instrs) - 1)
				a.visitOneInstr(b, index, instr, lastInsBB, allocated)
			}
			// mark the end of the last SyncBlock
			sb := a.getLastSyncBlock(b)
			if sb == nil {
				log.Fatal("syncblock list is empty")
			}
			sb.end = len(b.Instrs) - 1
		}
	}
	for _, op := range a.chanOps {
		a.ptaConfig.AddQuery(op.ch)
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
		return
		log.Fatal("Summary not found:", function)
	}
	for _, id := range ids {
		if !contains(summary.fromGoroutines, id) {
			summary.fromGoroutines = append(summary.fromGoroutines, id)
		}
	}
}

func GraphVisitEdgesPreorder(g *callgraph.Graph, edge func(*callgraph.Edge) error) error { // taken from library
	seen := make(map[*callgraph.Node]bool)
	var visit func(n *callgraph.Node) error
	visit = func(n *callgraph.Node) error {
		if !seen[n] {
			seen[n] = true
			for _, e := range n.Out {
				if err := edge(e); err != nil { // change of sequence. From postOrder to preOrder
					return err
				}
				if err := visit(e.Callee); err != nil {
					return err
				} // change of sequence
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
	return ptset[*addr1].MayAlias(ptset[*addr2])
}

// check if minAcc is po-ordered to some go ins, which is po-ordered to maxAcc
// TODO: Current approach is naive. We need to consider predecessors and successors of basic blocks.
func (a *analysis) checkPO(minAcc *accessInfo, maxAcc *accessInfo) bool {
	bbMin := minAcc.bb
	bbMax := maxAcc.bb
	if bbMax.Parent() == bbMin.Parent() && bbMin.Dominates(bbMax) {
		log.Debug(a.prog.Fset.Position((*minAcc.instruction).Pos()))
		log.Debug(a.prog.Fset.Position((*maxAcc.instruction).Pos()))
		return true
	}
	sum2 := maxAcc.parentSum
	for _, id := range sum2.fromGoroutines {
		goinstr, ok := a.goID2insMap[id]
		if !ok {
			continue
		}
		if minAcc.bb == goinstr.Block() {
			// check if goinstr is in minAcc.bb, after minAcc.index
			for i := minAcc.index + 1; i < len(minAcc.bb.Instrs); i++ {
				if minAcc.bb.Instrs[i] == goinstr {
					return true
				}
			}
		}

		//TODO: check successors of minAcc.bb
	}
	return false
}

// TODO: 1. Factor out the check using function "doChannelPeers". 2. Check if send is blocking
func (a *analysis) hasBlockingSendRecvMatch(bef []ssa.Instruction, aft []ssa.Instruction) bool {
	for _, bi1 := range bef {
		// check ai2 -> bi1 by blocking send/recv
		if instr1, ok := bi1.(*ssa.UnOp); ok && instr1.Op == token.ARROW {
			for _, ai2 := range aft {
				if instr2, ok := ai2.(*ssa.Send); ok && a.sameAddress(&instr1.X, &instr2.Chan) {
					return true
				}
			}
		}
	}
	return false
}

// check if acc1 is so-ordered to acc2 or acc2 is so-ordered to acc1
// TODO: support mutex op
func (a *analysis) checkSOTwoWay(acc1 accessInfo, acc2 accessInfo) bool {
	bef1, aft1 := a.getSyncOpsBeforeAndAfter(acc1)
	bef2, aft2 := a.getSyncOpsBeforeAndAfter(acc2)
	return a.hasBlockingSendRecvMatch(bef1, aft2) || a.hasBlockingSendRecvMatch(bef2, aft1)
}

// returns conflicting access pairs from the two summaries
func (a *analysis) getConflictingAccesses(sum1 *fnSummary, sum2 *fnSummary) [][2]accessInfo {
	var res [][2]accessInfo
	if parallel, _, _ := canRunInParallel(sum1, sum2, nil, nil); !parallel {
		return res
	}
	for _, acc1 := range sum1.accesses {
		for _, acc2 := range sum2.accesses {
			if (acc1.write || acc2.write) &&
				(!acc1.atomic || !acc2.atomic) &&
				a.sameAddress(&acc1.location, &acc2.location) {
				_, minIDAcc, maxIDAcc := canRunInParallel(sum1, sum2, &acc1, &acc2)
				if !a.checkPO(minIDAcc, maxIDAcc) && !a.checkSOTwoWay(*minIDAcc, *maxIDAcc) {
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
	// traverse cg in pre-order to label each function with a list of goroutine IDs
	err := GraphVisitEdgesPreorder(cg, func(edge *callgraph.Edge) error {
		if isSynthetic(edge) {
			return nil
		}
		caller := edge.Caller
		if !fromPkgsOfInterest(caller.Func) || caller.Func.Name() == "panic" || caller.Func.Name() == "init" {
			return nil
		}
		callee := edge.Callee
		site := edge.Site

		if caller.Func.Name() == "main" && a.getFromGoroutines(caller.Func) == nil {
			a.addNewGoroutineIDs(caller.Func, 0)
		}

		if !fromPkgsOfInterest(callee.Func) || callee.Func.Name() == "init" {
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
	log.Infof("%d goroutines", len(a.goID2insMap)+1)

	for _, sum := range a.fn2SummaryMap {
		sort.Ints(sum.fromGoroutines)
	}

	// generate a list of functions we have processed
	functions := make([]*ssa.Function, 0, len(a.fn2SummaryMap))
	for fn := range a.fn2SummaryMap {
		functions = append(functions, fn)
	}

	// filter conflicting access pairs from every pair of accesses within every pair of functions
	var fnPairs [][2]*ssa.Function
	var accPairs [][2]accessInfo
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

func init() {
}

func doAnalysis(args []string) error {
	cfg := &packages.Config{
		Mode:  packages.LoadAllSyntax,
		Dir:   "",
		Tests: false,
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
	log.Info("Loading SSA...")
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
		prog:                prog,
		pkgs:                pkgs,
		mains:               mains,
		ptaConfig:           config,
		fn2SummaryMap:       make(map[*ssa.Function]*fnSummary),
		goID2insMap:         make(map[int]*ssa.Go),
		bb2SyncBlockListMap: make(map[*ssa.BasicBlock][]*SyncBlock),
	}
	log.Info("Preprocessing...")
	Analysis.visitInstrs()
	log.Infof("%d function summaries, %d basic blocks", len(Analysis.fn2SummaryMap),
		len(Analysis.bb2SyncBlockListMap))
	Analysis.printSyncBlocks()
	log.Info("Running whole-program pointer analysis for ", len(config.Queries), " variables...")
	result, err := pointer.Analyze(config)
	log.Infoln("Done")

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

// test only
func (a *analysis) printSyncBlocks() {
	for bb, sbs := range a.bb2SyncBlockListMap {
		log.Debug(bb.Parent().Name(), " ", bb.Index)
		for i, sb := range sbs {
			log.Debugf("  %d: start=%d, end=%d, %s", i, sb.start, sb.end, bb.Instrs[sb.end])
		}
	}
}

func (a *analysis) printAccPairs(accPairs [][2]accessInfo) {
	rwString := func(write bool) string {
		if write {
			return "Write"
		}
		return "Read"
	}
	for i, pair := range accPairs {
		ins1, ins2 := *pair[0].instruction, *pair[1].instruction
		log.Printf("Data race #%d", i+1)
		log.Println("======================")
		log.Println("  ", rwString(pair[0].write),
			"of", aurora.Magenta(pair[0].location),
			"at", pair[0].bb.Parent().Name(), a.prog.Fset.Position(ins1.Pos()))
		log.Println("  ", rwString(pair[1].write),
			"of", aurora.Magenta(pair[1].location),
			"at", pair[1].bb.Parent().Name(), a.prog.Fset.Position(ins2.Pos()))
		log.Println("======================")
	}
	if len(accPairs) == 1 {
		log.Printf("Summary: 1 race reported, %d accesses checked", a.analysisStat.nAccess)
	} else {
		log.Printf("Summary: %d races reported, %d accesses checked", len(accPairs), a.analysisStat.nAccess)
	}
}

func main() {
	debug := flag.Bool("debug", false, "Prints debug messages.")
	focus := flag.String("focus", "", "Specifies a list of packages to check races.")
	flag.Parse()
	if *debug {
		log.SetLevel(log.DebugLevel)
	}
	focusPkgs = strings.Split(*focus, ",")
	focusPkgs = append(focusPkgs, "command-line-arguments")

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
