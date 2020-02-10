package main

import (
	"flag"
	"fmt"
	"github.com/logrusorgru/aurora"
	"go/types"
	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/pointer"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
	"strings"

	log "github.com/sirupsen/logrus"
)

type analysis struct {
	prog          *ssa.Program
	pkgs          []*ssa.Package
	mains         []*ssa.Package
	result        *pointer.Result
	cg            *callgraph.Graph
	chanOps       []chanOp
	ptaConfig     *pointer.Config
	fn2SummaryMap map[*ssa.Function]*functionSummary
	analysisStat  stat
}

type stat struct {
	nAccess    int
	nGoroutine int
	raceCount  int
}

var (
	Analysis  *analysis
	focusPkgs []string
)

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

func (a *analysis) visitOneFunction(fn *ssa.Function, rank int) {
	log.Debug(fn)
	fnSummary := a.fn2SummaryMap[fn]
	if fnSummary == nil {
		fnSummary = &functionSummary{
			sb2GoInsMap:   make(map[*SyncBlock]*ssa.Go),
			goroutineRank: rank,
			bb2sbList:     make(map[int][]*SyncBlock),
		}
		a.fn2SummaryMap[fn] = fnSummary
		fnSummary.function = fn
	}

	visitor := &InstructionVisitor{
		allocated:     make(map[*ssa.Alloc]bool),
		parentSummary: fnSummary,
	}

	for _, b := range fn.Blocks {
		idom := -1
		if b.Idom() != nil {
			idom = b.Idom().Index
		}
		log.Debugf("  -- %s %s Dominees: %s, Idom: %d", b, b.Comment, b.Dominees(), idom)
		visitor.sb = &SyncBlock{
			start:         0,
			bb:            b,
			parentSummary: fnSummary,
		}
		visitor.parentSummary.bb2sbList[b.Index] = []*SyncBlock{}
		if b.Comment == "select.done" {
			visitor.sb.snapshot.mhaChanRecv = NondetRecv
			visitor.sb.snapshot.mhbChanSend = NondetSend
			fnSummary.selectDoneBlock = append(fnSummary.selectDoneBlock, b)
		}

		// visit each instruction in b
		for index, instr := range b.Instrs {
			log.Debugf("    -- %s: %s", instr, a.prog.Fset.Position(instr.Pos()))
			visitor.visit(instr, b, index)
		}
		// mark the end of the last SyncBlock
		if visitor.sb.accesses != nil {
			visitor.sb.end = len(b.Instrs) - 1
			visitor.parentSummary.syncBlocks = append(visitor.parentSummary.syncBlocks, visitor.sb)
			visitor.parentSummary.bb2sbList[b.Index] = append(visitor.parentSummary.bb2sbList[b.Index], visitor.sb)
		}
	}
	fnSummary.MakePredAndSuccClosure()
	for _, doneBlock := range fnSummary.selectDoneBlock {
		// traverse the dominator tree
		worklist := []*ssa.BasicBlock{doneBlock}
		for len(worklist) > 0 {
			bb := worklist[len(worklist)-1]
			worklist = worklist[:len(worklist)-1]
			worklist = append(worklist, bb.Dominees()...)
			for _, s := range fnSummary.bb2sbList[bb.Index] {
				s.snapshot.mhaChanRecv = NondetRecv
				s.snapshot.mhbChanSend = NondetSend
			}
		}
	}
	for _, op := range fnSummary.chOps {
		if op.fromSelect != nil {
			continue
		}
		if op.Mode().IsRel() {
			op.syncPred.snapshot.mhbChanSend = SingleSend
			for _, predSB := range op.syncPred.preds {
				predSB.snapshot.mhbChanSend = SingleSend
			}
		} else if op.Mode().IsAcq() {
			op.syncSucc.snapshot.mhaChanRecv = SingleRecv
			for _, succSB := range op.syncSucc.succs {
				succSB.snapshot.mhaChanRecv = SingleRecv
			}
		}
	}
	// update snapshots for blocks under select cases
	for _, selectInstr := range fnSummary.selectStmts {
		childBlocks := fnSummary.selectChildBlocks(selectInstr.Block(), len(selectInstr.States))
		for idx, st := range selectInstr.States {
			if st.Dir == types.RecvOnly {
				for _, sb := range fnSummary.bb2sbList[childBlocks[idx].Index] {
					sb.snapshot.mhaChanRecv = SingleRecv
				}
			}
		}
	}
}

func isSyntheticEdge(edge *callgraph.Edge) bool {
	return edge.Caller.Func.Pkg == nil || edge.Callee.Func.Synthetic != ""
}

func isSyntheticNode(node *callgraph.Node) bool {
	return node.Func == nil || node.Func.Pkg == nil || node.Func.Synthetic != ""
}

func isInit(fn *ssa.Function) bool {
	return fn.Pkg != nil && fn.Pkg.Func("init") == fn
}

func contains(s []int, v int) bool {
	for _, a := range s {
		if a == v {
			return true
		}
	}
	return false
}

func GraphVisitPreorder(g *callgraph.Graph, node func(*callgraph.Node, int) error) error {
	seen := make(map[*callgraph.Node]bool)
	var visit func(n *callgraph.Node, rank int) error
	visit = func(n *callgraph.Node, rank int) error {
		if !seen[n] {
			seen[n] = true
			for _, e := range n.Out {
				newRank := rank
				if !isSyntheticEdge(e) {
					if _, ok := e.Site.(*ssa.Go); ok {
						newRank++
						if Analysis.analysisStat.nGoroutine < newRank {
							Analysis.analysisStat.nGoroutine = newRank
						}
					}
				}
				if err := visit(e.Callee, newRank); err != nil {
					return err
				} // change of sequence
			}
			if err := node(n, rank); err != nil {
				return err
			}
		}
		return nil
	}
	var err error
	for f, n := range g.Nodes {
		if f != nil && f.Pkg != nil && fromPkgsOfInterest(f) && f.Pkg.Func("main") == f {
			err = visit(n, 0)
			return err
		}
	}
	return nil
}

func (a *analysis) sameAddress(addr1 ssa.Value, addr2 ssa.Value) bool {
	// check if both are the same global
	if global1, ok1 := addr1.(*ssa.Global); ok1 {
		if global2, ok2 := addr2.(*ssa.Global); ok2 {
			return global1.Pos() == global2.Pos()
		}
	}

	// check if they can point to the same obj
	ptset := a.result.Queries
	return ptset[addr1].MayAlias(ptset[addr2])
}

// two functions can run in parallel iff their fromGoroutines slices do not equal
// return the summary with the min goroutine ID as 2nd return val
func canRunInParallel(summary1 *functionSummary, summary2 *functionSummary) bool {
	return summary1.goroutineRank != summary2.goroutineRank
}

func (a *analysis) preprocess() error {
	log.Info("Building callgraph...")
	cg := cha.CallGraph(a.prog)
	Analysis.cg = cg
	log.Infoln("Done")
	visitNode := func(node *callgraph.Node, rank int) error {
		if isSyntheticNode(node) || isInit(node.Func) || !fromPkgsOfInterest(node.Func) {
			return nil
		}
		a.visitOneFunction(node.Func, rank)
		return nil
	}

	err := GraphVisitPreorder(cg, visitNode)
	if err != nil {
		log.Fatal(err)
	}
	log.Infof("%d goroutines", a.analysisStat.nGoroutine+1)
	a.ptaConfig.BuildCallGraph = false
	log.Info("Running whole-program pointer analysis for ", len(a.ptaConfig.Queries), " variables...")
	result, err := pointer.Analyze(a.ptaConfig)
	if err != nil {
		log.Fatal(err)
	}
	Analysis.result = result

	return nil
}

func (a *analysis) checkRace() {
	// generate a list of functions we have processed
	functions := make([]*ssa.Function, 0, len(a.fn2SummaryMap))
	for fn := range a.fn2SummaryMap {
		functions = append(functions, fn)
	}
	// for each distinct unordered pair
	for i := 0; i < len(functions); i++ {
		for j := 0; j <= i; j++ {
			fn1, fn2 := functions[i], functions[j]
			fs1, fs2 := a.fn2SummaryMap[fn1], a.fn2SummaryMap[fn2]
			if canRunInParallel(fs1, fs2) {
				for _, sb1 := range fs1.syncBlocks {
					for _, sb2 := range fs2.syncBlocks {
						a.checkSyncBlock(sb1, sb2)
					}
				}
			}
		}
	}
}

func hasChannelComm(sb1 *SyncBlock, sb2 *SyncBlock) bool {
	return sb1.snapshot.mhbChanSend >= NondetSend && sb2.snapshot.mhaChanRecv == SingleRecv
}

func maySyncByChannelComm(sb1 *SyncBlock, sb2 *SyncBlock) bool {
	return hasChannelComm(sb1, sb2) || hasChannelComm(sb2, sb1)
}

func (a *analysis) checkSyncBlock(sb1 *SyncBlock, sb2 *SyncBlock) {
	for _, acc1 := range sb1.accesses {
		for _, acc2 := range sb2.accesses {
			if (acc1.write || acc2.write) &&
				(!acc1.atomic || !acc2.atomic) &&
				a.sameAddress(acc1.location, acc2.location) &&
				!maySyncByChannelComm(sb1, sb2) &&
				(sb1.snapshot.lockCount == 0 || sb2.snapshot.lockCount == 0) {
				a.reportRace(acc1, acc2)
			}
		}
	}
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
		prog:          prog,
		pkgs:          pkgs,
		mains:         mains,
		ptaConfig:     config,
		fn2SummaryMap: make(map[*ssa.Function]*functionSummary),
	}
	err = Analysis.preprocess()
	if err != nil {
		return err // internal error in pointer analysis
	}
	Analysis.printSyncBlocks()
	log.Infof("%d function summaries", len(Analysis.fn2SummaryMap))
	Analysis.checkRace()
	Analysis.printSummary()
	return nil
}

// test only
func (a *analysis) printSyncBlocks() {
	for fn, sum := range a.fn2SummaryMap {
		log.Debug(fn)
		for idx, sb := range sum.syncBlocks {
			log.Debugf("  %d:%d [%s] %d-%d SEND=%d RECV=%d LOCK=%d %s", sb.bb.Index, idx, sb.bb.Comment, sb.start, sb.end,
				sb.snapshot.mhbChanSend, sb.snapshot.mhaChanRecv, sb.snapshot.lockCount, a.prog.Fset.Position(sb.bb.Instrs[sb.start].Pos()))
		}
	}
}

func (a *analysis) printSummary() {
	if a.analysisStat.raceCount == 1 {
		log.Printf("Summary: 1 race reported, %d accesses checked", a.analysisStat.nAccess)
	} else {
		log.Printf("Summary: %d races reported, %d accesses checked", a.analysisStat.raceCount, a.analysisStat.nAccess)
	}
}

func (a *analysis) reportRace(a1, a2 accessInfo) {
	rwString := func(write bool) string {
		if write {
			return "Write"
		}
		return "Read"
	}
	ins1, ins2 := *a1.instruction, *a2.instruction
	//alloc1, alloc2 := a.result.Queries[a1.location], a.result.Queries[a2.location]
	a.analysisStat.raceCount++
	log.Printf("Data race #%d", a.analysisStat.raceCount)
	log.Println("======================")
	log.Println("  ", rwString(a1.write),
		"of", aurora.Magenta(a1.comment),
		"at", ins1.Parent().Name(), a.prog.Fset.Position(ins1.Pos()))
	log.Println("  ", rwString(a2.write),
		"of", aurora.Magenta(a2.comment),
		"at", ins2.Parent().Name(), a.prog.Fset.Position(ins2.Pos()))
	log.Println("======================")
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
