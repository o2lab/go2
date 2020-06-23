package main

import (
	"container/list"
	"flag"
	"fmt"
	"github.com/logrusorgru/aurora"
	"github.com/twmb/algoimpl/go/graph"
	"go/token"
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
	ptaConfig     *pointer.Config
	fn2SummaryMap map[*ssa.Function]*functionSummary
	analysisStat  stat
	HBgraph       *graph.Graph
	sb2HBnodeMap  map[*SyncBlock]graph.Node
}

type stat struct {
	nAccess    int
	nGoroutine int
	raceCount  int
	nFunctions int
	nPackages  int
}

type goroutineInfo struct{
	goIns  *ssa.Go
	entryMethod string
	rank int
}

var (
	Analysis     *analysis
	focusPkgs    []string
	excludedPkgs []string
	allPkg       bool
	fnReported   map[string]string
	levels = make(map[int]int)
	storeIns 	 []string
	mthdCall	 token.Pos
	progFunc	 map[*ssa.Function]bool
	worklist list.List
)

func fromPkgsOfInterest(fn *ssa.Function) bool {
	if fn.Pkg == nil || fn.Pkg.Pkg == nil {
		return false
	}
	for _, excluded := range excludedPkgs {
		if fn.Pkg.Pkg.Name() == excluded {
			return false
		}
	}
	for _, path := range focusPkgs {
		if path != "" && strings.HasPrefix(fn.Pkg.Pkg.Path(), path) {
			return true
		}
	}
	return allPkg
}

func (a *analysis) visitOneFunction(fn *ssa.Function, rank int) {
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
		//idom := -1 // for debugging purpose
		//if b.Idom() != nil {
		//	idom = b.Idom().Index
		//}

		if b.Comment == "rangeindex.body" || b.Comment == "rangeindex.loop" {
			if IsReturnBlock(b) {
				fnSummary.returnBlocks = append(fnSummary.returnBlocks, b)
			}
			visitor.sb = &SyncBlock{
				start:         0,
				bb:            b,
				parentSummary: fnSummary,
				snapshot:      SyncSnapshot{lockOpList: make(map[ssa.Value]MutexOp)},
			}
			visitor.parentSummary.bb2sbList[b.Index] = []*SyncBlock{}

			for index, instr := range b.Instrs {
				visitor.visit(instr, b, index)
			}

			if visitor.sb.hasAccessOrSyncOp() {
				visitor.sb.end = len(b.Instrs) - 1
				if visitor.lastNonEmptySB != nil {
					visitor.lastNonEmptySB.succs = []*SyncBlock{visitor.sb}
					visitor.sb.preds = []*SyncBlock{visitor.lastNonEmptySB}
				}
				visitor.parentSummary.syncBlocks = append(visitor.parentSummary.syncBlocks, visitor.sb)
				visitor.parentSummary.bb2sbList[b.Index] = append(visitor.parentSummary.bb2sbList[b.Index], visitor.sb)
			}
		}
		if IsReturnBlock(b) {
			fnSummary.returnBlocks = append(fnSummary.returnBlocks, b)
		}
		visitor.sb = &SyncBlock{
			start:         0,
			bb:            b,
			parentSummary: fnSummary,
			snapshot:      SyncSnapshot{lockOpList: make(map[ssa.Value]MutexOp)},
		}
		visitor.parentSummary.bb2sbList[b.Index] = []*SyncBlock{}

		// visit each instruction in b
		for index, instr := range b.Instrs {
			visitor.visit(instr, b, index)
		}

		// mark the end of the last SyncBlock
		if visitor.sb.hasAccessOrSyncOp() {
			visitor.sb.end = len(b.Instrs) - 1
			if visitor.lastNonEmptySB != nil {
				visitor.lastNonEmptySB.succs = []*SyncBlock{visitor.sb}
				visitor.sb.preds = []*SyncBlock{visitor.lastNonEmptySB}
			}
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
		}
	}
	for _, op := range fnSummary.chRecvOps {
		if op.fromSelect != nil {
			continue
		}
	}
	for _, op := range fnSummary.chSendOps {
		if op.fromSelect != nil {
			continue
		}
	}
}

func isSyntheticEdge(edge *callgraph.Edge) bool {
	return edge.Caller.Func.Pkg == nil
	//|| edge.Callee.Func.Synthetic != ""
}

func isSyntheticNode(node *callgraph.Node) bool {
	return node.Func == nil || node.Func.Pkg == nil || node.Func.Synthetic != ""
}

func isInit(fn *ssa.Function) bool {
	return fn.Pkg != nil && fn.Pkg.Func("init") == fn
}

func GraphVisitPreorder(g *callgraph.Graph, node func(*callgraph.Node, int) error) error {
	seen := make(map[*callgraph.Node]bool)
	var visit func(n *callgraph.Node, rank int) error
	visit = func(n *callgraph.Node, rank int) error {
		if !seen[n] && (fromPkgsOfInterest(n.Func) || strings.HasSuffix(n.Func.Name(), "$bound")) {
			seen[n] = true
			for _, outEdge := range n.Out {
				if outEdge.Site.Block().Comment == "rangeindex.body" {
					n.Out = append(n.Out, outEdge)
				}
			}
			for _, e := range n.Out {
				newRank := rank
				if !isSyntheticEdge(e) && e.Caller.Func.Pkg.Pkg.Name() != "sync" {
					if _, ok := e.Site.(*ssa.Go); ok {
						log.Debugf("%s spawns %s", e.Caller.Func, e.Callee.Func)
						if rank == 0 {
							newRank = rank + Analysis.analysisStat.nGoroutine + 1
						} else {
							newRank = rank + Analysis.analysisStat.nGoroutine
						}
						Analysis.analysisStat.nGoroutine++
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
			VisitAllInstructions(n.Func, 0)
			return err
		}
	}

	for worklist.Len() > 0 {
		info := worklist.Front()
		worklist.Remove(info)
		newGoroutine(info.Value.(goroutineInfo))
	}

	return nil
}

func VisitAllInstructions(fn *ssa.Function, rank int) {
	for _, excluded := range excludedPkgs {
		if fn.Pkg.Pkg.Name() == excluded {
			return
		}
	}
	fnBlocks := fn.Blocks
	if theLvl, ok := levels[rank]; !ok {
		theLvl = 0
		if rank > 0 {
			theLvl = 1
		}
		levels[rank] = theLvl
	}
	if fn.Name() == "main" {
		log.Debug(strings.Repeat(" ", levels[rank]), "PUSH ", fn.Name(), " at lvl ", levels[rank])
		storeIns = append(storeIns, fn.Name())
		levels[rank]++
	}
	for _, aBlock := range fnBlocks {
		for _, theIns := range aBlock.Instrs {
			switch examIns := theIns.(type) {
			case *ssa.Call: // for function calls and method calls
				if examIns.Call.Method != nil { // calling a method
					fnName := examIns.Call.Method.Name()
					if mthdCall != examIns.Call.Method.Pos() {
						log.Debug(strings.Repeat(" ", levels[rank]), "PUSH ", examIns.Call.Method.FullName(), " at lvl ", levels[rank])
						levels[rank]++
						mthdCall = examIns.Call.Method.Pos()
						storeIns = append(storeIns, fnName)
					}
					for theFunc := range progFunc {
						if theFunc.Name() == fnName {
							VisitAllInstructions(theFunc, rank)
							break
						}
					}
				} else if fromPkgsOfInterest(examIns.Call.StaticCallee()) && examIns.Call.StaticCallee().Pkg.Pkg.Name() != "sync" { // calling a function
					fnName := examIns.Call.Value.Name()
					if strings.HasPrefix(fnName, "t") && len(fnName) > 3 {
						fnName = examIns.Call.Value.Type().String()
					}
					log.Debug(strings.Repeat(" ", levels[rank]), "PUSH ", fnName, " at lvl ", levels[rank])
					storeIns = append(storeIns, fnName)
					levels[rank]++
					VisitAllInstructions(examIns.Call.StaticCallee(), rank)
				}
			case *ssa.Return: // for endpoints of each function
				if fromPkgsOfInterest(examIns.Parent()) {
					fnName := examIns.Parent().Name()
					if fnName == storeIns[len(storeIns) - 1] {
						storeIns = storeIns[:len(storeIns) - 1]
						if levels[rank] == 0 {
							rank--
						}
						levels[rank]--
						log.Debug(strings.Repeat(" ", levels[rank]), "POP  ", fnName, " at lvl ", levels[rank])
					}
				}
			case *ssa.Go: // for spawning of goroutines
				rank++
				var fnName string
				switch anonFn := examIns.Call.Value.(type) {
				case *ssa.MakeClosure:
					fnName = anonFn.Fn.Name()
				case *ssa.Function:
					fnName = anonFn.Name()
				}
				log.Debug(strings.Repeat(" ", levels[rank]), "PUSH GO ", fnName, " at lvl ", levels[rank])

				var info = goroutineInfo {examIns,fnName,rank}
				worklist.PushBack(info)
			}
		}
	}
}

func newGoroutine(info goroutineInfo) {
	//examIns *ssa.Go, fnName string, rank int
	storeIns = append(storeIns, info.entryMethod)
	VisitAllInstructions(info.goIns.Call.StaticCallee(), info.rank)
}

func (a *analysis) sameAddress(addr1 ssa.Value, addr2 ssa.Value) bool {
	// check if both are the same global
	if global1, ok1 := addr1.(*ssa.Global); ok1 {
		if global2, ok2 := addr2.(*ssa.Global); ok2 {
			return global1.Pos() == global2.Pos()
		}
	}

	// check points-to set to see if they can point to the same object
	ptset := a.result.Queries
	return ptset[addr1].PointsTo().Intersects(ptset[addr2].PointsTo())
}

// two functions can run in parallel iff they are located in different goroutines
func inDifferentGoroutines(summary1 *functionSummary, summary2 *functionSummary) bool {
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
		a.analysisStat.nFunctions += 1
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

	log.Info("Building HB Graph...")
	a.HBgraph = graph.New(graph.Directed)
	Analysis.sb2HBnodeMap = make(map[*SyncBlock]graph.Node)
	for _, fn := range a.fn2SummaryMap {
		for _, sb := range fn.syncBlocks {
			temp := a.HBgraph.MakeNode()
			*temp.Value = sb // label
			Analysis.sb2HBnodeMap[sb] = temp
		}
	}
	// create PO edges
	for _, fn := range a.fn2SummaryMap {
		if len(fn.syncBlocks) == 0 {
			continue
		}
		curr := fn.syncBlocks[0]
		stack := []*SyncBlock{curr}
		visited := map[*SyncBlock]bool{}
		for len(stack) > 0 {
			curr = stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			visited[curr] = true
			for _, nextNode := range curr.succs {
				err := a.HBgraph.MakeEdge(a.sb2HBnodeMap[curr], a.sb2HBnodeMap[nextNode])
				if err != nil {
					return err
				}
				if !visited[nextNode] {
					stack = append(stack, nextNode)
				}
			}
			// to handle go function calls
			for _, goSumm1 := range curr.mhbGoFuncList {
				for _, sb1 := range goSumm1.syncBlocks {
					err := a.HBgraph.MakeEdge(a.sb2HBnodeMap[curr], a.sb2HBnodeMap[sb1])
					if err != nil {
						return err
					}
				}
			}
		}
	}
	// Sync Order edges created here
	for _, fn1 := range a.fn2SummaryMap {
		if len(fn1.syncBlocks) == 0 {
			continue
		}
		for _, fn2 := range a.fn2SummaryMap {
			if !(inDifferentGoroutines(fn1, fn2)) || len(fn2.syncBlocks) == 0 {
				continue
			}
			for _, sb1 := range fn1.syncBlocks {
				if !sb1.hasAccessOrSyncOp() {
					continue
				}
				for _, sb2 := range fn2.syncBlocks {
					if !sb2.hasAccessOrSyncOp() {
						continue
					}
					for _, send := range sb1.snapshot.chanSendOpList {
						for _, recv := range sb2.snapshot.chanRecvOpList {
							if areSendRecvPair(send, recv) {
								err := a.HBgraph.MakeEdge(a.sb2HBnodeMap[sb1], a.sb2HBnodeMap[sb2])
								if err != nil {
									return err
								}
							}
						}
					}
					for _, done := range sb1.snapshot.wgDoneList {
						for _, wait := range sb2.snapshot.wgWaitList {
							if areDoneWaitPair(done, wait) {
								err := a.HBgraph.MakeEdge(a.sb2HBnodeMap[sb1], a.sb2HBnodeMap[sb2])
								if err != nil {
									return err
								}
							}
						}
					}
				}
			}
		}
	}
	log.Infoln("Done")
	return nil
}

// incorporate transitivity
func (a *analysis) reachable(sb1 *SyncBlock, sb2 *SyncBlock) bool {
	//goal := a.sb2HBnodeMap[sb2]
	curr := a.sb2HBnodeMap[sb1]
	stack := []graph.Node{curr}
	visited := map[graph.Node]bool{}
	for len(stack) > 0 {
		curr = stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if curSb, ok := (*curr.Value).(*SyncBlock); ok {
			if curSb == sb2 {
				return true
			}
		}
		visited[curr] = true
		for _, reachableNode := range a.HBgraph.Neighbors(curr) {
			if !visited[reachableNode] {
				stack = append(stack, reachableNode)
			}
		}
	}
	return false
}

func (a *analysis) reachableBothWays(sb1 *SyncBlock, sb2 *SyncBlock) bool {
	if a.reachable(sb1, sb2) || a.reachable(sb2, sb1) {
		return true
	}
	return false
}

func (a *analysis) checkRace() {
	// generate a list of functions we have processed
	functions := make([]*ssa.Function, 0, len(a.fn2SummaryMap))
	for fn := range a.fn2SummaryMap {
		functions = append(functions, fn)
	}
	//generate map for storing reported racy function pairs
	fnReported = make(map[string]string)
	// for each distinct unordered pair
	for i := 0; i < len(functions); i++ {
		for j := 0; j <= i; j++ {
			fn1, fn2 := functions[i], functions[j]
			fs1, fs2 := a.fn2SummaryMap[fn1], a.fn2SummaryMap[fn2]
			if inDifferentGoroutines(fs1, fs2) {
				for _, sb1 := range fs1.syncBlocks {
					for _, sb2 := range fs2.syncBlocks {
						a.checkSyncBlock(sb1, sb2)
					}
				}
			}
		}
	}
}

func locksetsIntersect(sb1 *SyncBlock, sb2 *SyncBlock) bool {
	res := false
	for loc1 := range sb1.snapshot.lockOpList {
		for loc2 := range sb2.snapshot.lockOpList {
			if Analysis.sameAddress(loc1, loc2) {
				res = true
			}
		}
	}
	return res
}

func (a *analysis) checkSyncBlock(sb1 *SyncBlock, sb2 *SyncBlock) {
	if sb1.bb.Parent().Pkg.Pkg.Name() == "sync" {
		return
	}
	for _, acc1 := range sb1.accesses {
		for _, acc2 := range sb2.accesses {
			if (acc1.write || acc2.write) && (!acc1.atomic || !acc2.atomic) &&
				a.sameAddress(acc1.location, acc2.location) &&
				!locksetsIntersect(sb1, sb2) && !a.reachableBothWays(sb1, sb2) {
				if acc1.write {
					if fn, ok := fnReported[strings.Split(sb1.bb.Parent().Name(), "$")[0]]; ok && (fn == strings.Split(sb2.bb.Parent().Name(), "$")[0]) {
						// omit access pairs that have already been reported
					} else {
						fnReported[strings.Split(sb1.bb.Parent().Name(), "$")[0]] = strings.Split(sb2.bb.Parent().Name(), "$")[0]
						a.reportRace(acc1, acc2)
					}
				} else {
					if fn, ok := fnReported[strings.Split(sb2.bb.Parent().Name(), "$")[0]]; ok && (fn == strings.Split(sb1.bb.Parent().Name(), "$")[0]) {
						// omit access pairs that have already been reported
					} else {
						fnReported[strings.Split(sb2.bb.Parent().Name(), "$")[0]] = strings.Split(sb1.bb.Parent().Name(), "$")[0]
						a.reportRace(acc1, acc2)
					}
				}
			}
		}
	}
}

func doAnalysis(args []string) error {
	cfg := &packages.Config{
		Mode:  packages.LoadAllSyntax,
		Dir:   "",
		Tests: false,
	}
	log.Info("Loading input packages...")
	initial, err := packages.Load(cfg, args...)
	if err != nil {
		return err
	}
	if packages.PrintErrors(initial) > 0 {
		return fmt.Errorf("packages contain errors")
	}

	// Print the names of the source files
	// for each package listed on the command line.
	for _, pkg := range initial {
		fmt.Println(pkg.ID, pkg.GoFiles)
	}

	// Create and build SSA-form program representation.
	prog, pkgs := ssautil.AllPackages(initial, 0)
	log.Info("Building SSA program...")
	prog.Build()
	progFunc = ssautil.AllFunctions(prog)

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
	// Analysis.printSyncBlocks()
	log.Infof("%d function summaries", len(Analysis.fn2SummaryMap))
	Analysis.checkRace()
	Analysis.printSummary()
	return nil
}

func (a *analysis) printSyncBlocks() {
	for fn, sum := range a.fn2SummaryMap {
		log.Debug(fn)
		for idx, sb := range sum.syncBlocks {
			log.Debugf("  %d:%d [%s] %d-%d Rank %d %s", sb.bb.Index, idx, sb.bb.Comment, sb.start, sb.end, sb.parentSummary.goroutineRank, a.prog.Fset.Position(sb.bb.Instrs[sb.start].Pos()))
			log.Debug("  -- Lockset ", sb.snapshot.lockOpList)
			log.Debug("  -- chan ", sb.snapshot.chanSendOpList, " recv ", sb.snapshot.chanRecvOpList)
			log.Debug("  -- Lockset ", sb.snapshot.wgDoneList, " wait ", sb.snapshot.wgWaitList)
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
	//acc := a1
	//if !acc.write {
	//	acc = a2
	//} TODO: Report racy struct field instead of token number
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
	//flag.BoolVar(&allPkg, "all-package", true, "Analyze all packages required by the main package.")
	flag.Parse()
	if *debug {
		log.SetLevel(log.DebugLevel)
	}
	if *focus != "" {
		focusPkgs = strings.Split(*focus, ",")
		focusPkgs = append(focusPkgs, "command-line-arguments")
	} else {
		allPkg = true
	}

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

func init() {
	excludedPkgs = []string{
		"runtime",
		"fmt",
		"reflect",
		"encoding",
		"errors",
		"bytes",
		"time",
		"testing",
		"strconv",
		"atomic",
		"strings",
		"bytealg",
		"race",
		"syscall",
	}
}
