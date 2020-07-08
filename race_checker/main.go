package main

import (
	"flag"
	"fmt"
	"github.com/logrusorgru/aurora"
	log "github.com/sirupsen/logrus"
	"github.com/twmb/algoimpl/go/graph"
	"go/token"
	"go/types"
	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/pointer"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
	"strings"
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

type goroutineInfo struct {
	goIns       *ssa.Go
	entryMethod string
	goID        int
}

var (
	Analysis     *analysis
	focusPkgs    []string
	excludedPkgs []string
	allPkg       bool
	fnReported   map[string]string
	levels       = make(map[int]int)
	RWIns        [][]ssa.Instruction
	storeIns     []string
	progFunc     map[*ssa.Function]bool
	workList     []goroutineInfo
	reportedAddr []ssa.Value
)

func fromPkgsOfInterest(fn *ssa.Function) bool {
	if fn.Pkg == nil || fn.Pkg.Pkg == nil {
		return false
	}
	for _, excluded := range excludedPkgs {
		if fn.Pkg.Pkg.Name() == excluded {
			if fn.Name() == "AfterFunc" || fn.Name() == "when" { // TODO: revision needed
				return true
			}
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
						//log.Debugf("%s spawns %s", e.Caller.Func, e.Callee.Func)
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
			return err
		}
	}

	return nil
}

func isSynthetic(fn *ssa.Function) bool {
	return fn.Synthetic != "" || fn.Pkg == nil
}

func isLocalAddr(location ssa.Value) bool {
	if location.Pos() == token.NoPos {
		return true
	}
	if locPara, ok := location.(*ssa.Parameter); ok {
		_, ok := locPara.Type().(*types.Pointer)
		return !ok
	}
	switch loc := location.(type) {
	case *ssa.FieldAddr:
		return false
	case *ssa.UnOp:
		return isLocalAddr(loc.X)
	}
	return false
}

func (a *analysis) VisitAllInstructions(fn *ssa.Function, goID int) { // called by doAnalysis()
	a.analysisStat.nGoroutine = goID + 1 // keep count of goroutine quantity
	if !isSynthetic(fn) {                // function is NOT synthetic
		for _, excluded := range excludedPkgs { // TODO: need revision
			if fn.Pkg.Pkg.Name() == excluded && fn.Name() != "AfterFunc" && fn.Name() != "when" {
				return
			}
		}
		if fn.Name() == "main" {
			levels[goID] = 0 // initialize level count at main entry
			log.Debug(strings.Repeat(" ", levels[goID]), "PUSH ", fn.Name(), " at lvl ", levels[goID])
			storeIns = append(storeIns, fn.Name())
			levels[goID]++
		}
	}
	if _, ok := levels[goID]; !ok && goID > 0 { // initialize level counter for new goroutine
		levels[goID] = 1
	}
	if goID >= len(RWIns) { // initialize interior slice for new goroutine
		RWIns = append(RWIns, []ssa.Instruction{})
	}
	fnBlocks := fn.Blocks
	var toAppend []*ssa.BasicBlock
	for j, nBlock := range fn.Blocks {
		if strings.HasSuffix(nBlock.Comment, ".done") && j != len(fnBlocks)-1 { // when return block doesn't have largest index
			bLen := len(nBlock.Instrs)
			if _, ok := nBlock.Instrs[bLen-1].(*ssa.Return); ok {
				toAppend = append([]*ssa.BasicBlock{nBlock}, toAppend...)
			}
		} else if nBlock.Comment == "recover" { // ignore built-in recover function
			fnBlocks = append(fnBlocks[:j], fnBlocks[j+1:]...)
		}
	}
	if len(toAppend) > 0 {
		fnBlocks = append(fnBlocks, toAppend...) // move return block to end of slice
	}
	repeatSwitch := false // triggered when encountering basic blocks for body of a forloop
	for i := 0; i < len(fnBlocks); i++ {
		aBlock := fnBlocks[i]
		//if strings.HasSuffix(aBlock.Comment, ".done") && i != len(fnBlocks)-1 { // ignore return block if it doesn't have largest index
		//	continue
		//}
		if aBlock.Comment == "for.body" || aBlock.Comment == "rangeindex.body" { // repeat unrolling of forloop
			if repeatSwitch == false {
				i--
				repeatSwitch = true
			} else {
				repeatSwitch = false
			}
		}
		for _, theIns := range aBlock.Instrs { // examine each instruction
			for _, ex := range excludedPkgs { // TODO: need revision
				if !isSynthetic(fn) && ex == theIns.Parent().Pkg.Pkg.Name() {
					if fn.Name() != "AfterFunc" && fn.Name() != "when" {
						return
					}
				}
			}
			switch examIns := theIns.(type) {
			case *ssa.Store: // write op
				if !isLocalAddr(examIns.Addr) {
					RWIns[goID] = append(RWIns[goID], theIns)
				}
			case *ssa.UnOp: // read op
				if examIns.Op == token.MUL && !isLocalAddr(examIns.X) {
					RWIns[goID] = append(RWIns[goID], theIns)
				}
			case *ssa.ChangeType:
				switch mc := examIns.X.(type) {
				case *ssa.MakeClosure:
					theFn := mc.Fn.(*ssa.Function)
					if fromPkgsOfInterest(theFn) {
						fnName := mc.Fn.Name()
						log.Debug(strings.Repeat(" ", levels[goID]), "PUSH ", fnName, " at lvl ", levels[goID])
						levels[goID]++
						storeIns = append(storeIns, fnName)
						RWIns[goID] = append(RWIns[goID], theIns)
						a.VisitAllInstructions(theFn, goID)
					}
				default:
					continue
				}
			case *ssa.Defer:
				if _, ok := examIns.Call.Value.(*ssa.Builtin); ok {
					continue
				}
				if fromPkgsOfInterest(examIns.Call.StaticCallee()) && examIns.Call.StaticCallee().Pkg.Pkg.Name() != "sync" {
					fnName := examIns.Call.Value.Name()
					if strings.HasPrefix(fnName, "t") && len(fnName) <= 3 { // if function name is token number
						switch callVal := examIns.Call.Value.(type) {
						case *ssa.MakeClosure:
							fnName = callVal.Fn.Name()
						default:
							fnName = callVal.Type().String()
						}
					}
					log.Debug(strings.Repeat(" ", levels[goID]), "PUSH ", fnName, " at lvl ", levels[goID])
					storeIns = append(storeIns, fnName)
					RWIns[goID] = append(RWIns[goID], theIns)
					levels[goID]++
					a.VisitAllInstructions(examIns.Call.StaticCallee(), goID)
				}
			case *ssa.MakeInterface:
				if strings.Contains(examIns.X.String(), "complit") {
					continue
				}
				if _, ok := examIns.X.(*ssa.Call); !ok {
					continue
				}
				a.pointerAnalysis(examIns.X, goID, theIns)
			case *ssa.Call:
				if examIns.Call.StaticCallee() == nil && examIns.Call.Method == nil { // calling a parameter function
					if _, ok := examIns.Call.Value.(*ssa.Builtin); !ok {
						a.pointerAnalysis(examIns.Call.Value, goID, theIns)
					} else {
						continue
					}
				} else if examIns.Call.Method != nil { // calling an method
					if _, ok := examIns.Call.Value.(*ssa.Builtin); !ok {
						a.pointerAnalysis(examIns.Call.Value, goID, theIns)
					} else {
						continue
					}
				} else if fromPkgsOfInterest(examIns.Call.StaticCallee()) && (examIns.Call.StaticCallee().Pkg.Pkg.Name() != "sync" || examIns.Call.StaticCallee().Name() == "Range") { // calling a function
					fnName := examIns.Call.Value.Name()
					if fnName == "runtimeNano" || fnName == "startTimer" { // revision needed
						continue
					}
					if strings.HasPrefix(fnName, "t") && len(fnName) <= 3 { // if function name is token number
						switch callVal := examIns.Call.Value.(type) {
						case *ssa.MakeClosure:
							fnName = callVal.Fn.Name()
						default:
							fnName = callVal.Type().String()
						}
					}
					log.Debug(strings.Repeat(" ", levels[goID]), "PUSH ", fnName, " at lvl ", levels[goID])
					storeIns = append(storeIns, fnName)
					RWIns[goID] = append(RWIns[goID], theIns)
					levels[goID]++
					a.VisitAllInstructions(examIns.Call.StaticCallee(), goID)
				}
			case *ssa.Return:
				if i != len(fnBlocks)-1 {
					continue
				}
				if (fromPkgsOfInterest(examIns.Parent()) || isSynthetic(examIns.Parent())) && len(storeIns) > 0 {
					fnName := examIns.Parent().Name()
					if fnName == storeIns[len(storeIns)-1] {
						storeIns = storeIns[:len(storeIns)-1]
						RWIns[goID] = append(RWIns[goID], theIns)
						levels[goID]--
						log.Debug(strings.Repeat(" ", levels[goID]), "POP  ", fnName, " at lvl ", levels[goID])
					}
				}
				if len(storeIns) == 0 && len(workList) != 0 { // finished reporting current goroutine and workList isn't empty
					nextGoInfo := workList[0] // get the goroutine info at end of workList
					workList = workList[1:]   // pop goroutine info from end of workList
					a.newGoroutine(nextGoInfo)
				} else {
					return
				}
			case *ssa.Go: // for spawning of goroutines
				var fnName string
				switch anonFn := examIns.Call.Value.(type) {
				case *ssa.MakeClosure: // go call for anonymous function
					fnName = anonFn.Fn.Name()
				case *ssa.Function:
					fnName = anonFn.Name()
				}
				newGoID := goID + 1 // increment goID for child goroutine
				if len(workList) > 0 {
					newGoID = workList[len(workList)-1].goID + 1
				}
				RWIns[goID] = append(RWIns[goID], theIns)
				var info = goroutineInfo{examIns, fnName, newGoID}
				workList = append(workList, info) // store encountered goroutines
				log.Debug(strings.Repeat(" ", levels[goID]), "spawning Goroutine ----->  ", fnName)
			}
		}
	}
}

func sliceContains(s []ssa.Value, e ssa.Value) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}

func (a *analysis) printRace(counter int, insPair []ssa.Instruction) {
	log.Printf("Data race #%d", counter)
	log.Println(strings.Repeat("=", 100))
	for _, theIns := range insPair {
		if _, write := theIns.(*ssa.Store); write {
			log.Println("\tWrite of ", aurora.Magenta(theIns.(*ssa.Store).Addr.Name()), " in function ", aurora.BgBrightGreen(theIns.Parent().Name()), " at ", a.prog.Fset.Position(theIns.Pos()))
		} else {
			log.Println("\tRead of  ", aurora.Magenta(theIns.(*ssa.UnOp).X.Name()), " in function ", aurora.BgBrightGreen(theIns.Parent().Name()), " at ", a.prog.Fset.Position(theIns.Pos()))
		}
	}
	log.Println(strings.Repeat("=", 100))
}

func (a *analysis) checkRacyPairs() {
	counter := 0 // initialize race counter
	for i := 0; i < len(RWIns); i++ {
		for j := i + 1; j < len(RWIns); j++ { // must be in different goroutines
			for _, goI := range RWIns[i] { // get instruction from each goroutine
				for _, goJ := range RWIns[j] {
					switch insI := goI.(type) {
					case *ssa.Store:
						if insJ, ok := goJ.(*ssa.UnOp); ok {
							if insI.Addr == insJ.X && !sliceContains(reportedAddr, insI.Addr) {
								reportedAddr = append(reportedAddr, insI.Addr)
								counter++
								insSlice := []ssa.Instruction{goI, goJ}
								a.printRace(counter, insSlice)
							}
						} else if insJ, ok := goJ.(*ssa.Store); ok { // both accesses are write
							if insI.Addr == insJ.Addr && !sliceContains(reportedAddr, insI.Addr) {
								reportedAddr = append(reportedAddr, insI.Addr)
								counter++
								insSlice := []ssa.Instruction{goI, goJ}
								a.printRace(counter, insSlice)
							}
						}
					case *ssa.UnOp:
						if insJ, ok := goJ.(*ssa.Store); ok {
							if insI.X == insJ.Addr && !sliceContains(reportedAddr, insI.X) {
								reportedAddr = append(reportedAddr, insI.X)
								counter++
								insSlice := []ssa.Instruction{goI, goJ}
								a.printRace(counter, insSlice)
							}
						}
					}
				}
			}
		}
	}
	log.Println("Done  -- ", counter, "race found! ")
}

func (a *analysis) newGoroutine(info goroutineInfo) {
	storeIns = append(storeIns, info.entryMethod)
	if info.goID >= len(RWIns) { // initialize interior slice for new goroutine
		RWIns = append(RWIns, []ssa.Instruction{})
	}
	RWIns[info.goID] = append(RWIns[info.goID], info.goIns)
	log.Debug(strings.Repeat("-", 35), "Goroutine ", info.entryMethod, strings.Repeat("-", 35), "[", info.goID, "]")
	log.Debug(strings.Repeat(" ", levels[info.goID]), "PUSH ", info.entryMethod, " at lvl ", levels[info.goID])
	levels[info.goID]++
	a.VisitAllInstructions(info.goIns.Call.StaticCallee(), info.goID)
}

func (a *analysis) pointerAnalysis(location ssa.Value, goID int, theIns ssa.Instruction) {
	a.ptaConfig.AddQuery(location)
	result, err := pointer.Analyze(a.ptaConfig) // conduct pointer analysis
	if err != nil {
		log.Fatal(err)
	}
	Analysis.result = result
	ptrSet := a.result.Queries                    // set of pointers from result of pointer analysis
	PTSet := ptrSet[location].PointsTo().Labels() // set of labels for locations that the pointer points to
	var fnName string
	switch theFunc := PTSet[0].Value().(type) {
	case *ssa.Function:
		fnName = theFunc.Name()
		log.Debug(strings.Repeat(" ", levels[goID]), "PUSH ", fnName, " at lvl ", levels[goID])
		levels[goID]++
		storeIns = append(storeIns, fnName)
		RWIns[goID] = append(RWIns[goID], theIns)
		a.VisitAllInstructions(theFunc, goID)
	case *ssa.MakeInterface:
		methodName := theIns.(*ssa.Call).Call.Method.Name()
		check := a.prog.LookupMethod(ptrSet[location].PointsTo().DynamicTypes().Keys()[0], a.mains[0].Pkg, methodName)
		fnName = check.Name()
		log.Debug(strings.Repeat(" ", levels[goID]), "PUSH ", fnName, " at lvl ", levels[goID])
		levels[goID]++
		storeIns = append(storeIns, fnName)
		RWIns[goID] = append(RWIns[goID], theIns)
		a.VisitAllInstructions(check, goID)
	default:
		return
	}
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
	log.Infoln("Done  -- callgraph built")
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
	//log.Infof("%d goroutines", a.analysisStat.nGoroutine+1)
	a.ptaConfig.BuildCallGraph = false
	//log.Info("Running whole-program pointer analysis for ", len(a.ptaConfig.Queries), " variables...")
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
	for nP, pkg := range initial {
		log.Info(pkg.ID, pkg.GoFiles)
		log.Infof("Done  -- %d packages loaded", nP+1)
	}

	// Create and build SSA-form program representation.
	prog, pkgs := ssautil.AllPackages(initial, 0)
	//mainPkg := prog.Package(pkgs[0].Pkg)

	log.Info("Building SSA code for entire program...")
	prog.Build()
	log.Info("Done  -- SSA code built")

	mains, err := mainPackages(pkgs)
	if err != nil {
		return err
	}

	// Configure pointer analysis to build call-graph
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
	log.Infof("Done  -- %d function summaries assembled", len(Analysis.fn2SummaryMap))

	log.Info("Compiling stack trace for every Goroutine... ")
	log.Debug(strings.Repeat("-", 35), "Stack trace begins", strings.Repeat("-", 35))
	Analysis.VisitAllInstructions(mains[0].Func("main"), 0)
	log.Debug(strings.Repeat("-", 35), "Stack trace ends", strings.Repeat("-", 35))
	log.Info("Done  -- ", Analysis.analysisStat.nGoroutine, " goroutines analyzed!")

	log.Info("Checking for data races... ")
	Analysis.checkRacyPairs()
	//Analysis.checkRace()
	//Analysis.printSummary()
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
		log.Printf("Done  -- 1 race reported, %d accesses checked", a.analysisStat.nAccess)
	} else {
		log.Printf("Done  -- %d races reported, %d accesses checked", a.analysisStat.raceCount, a.analysisStat.nAccess)
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
		"poll",
		"trace",
		"logging",
		"os",
	}
}
