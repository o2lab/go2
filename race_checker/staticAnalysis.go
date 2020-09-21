package main

import (
	"fmt"
	log "github.com/sirupsen/logrus"
	"github.com/twmb/algoimpl/go/graph"
	"go/token"
	"go/types"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/pointer"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
	"strings"
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

func isLocalAddr(location ssa.Value) bool {
	if location.Pos() == token.NoPos {
		return true
	}
	switch loc := location.(type) {
	case *ssa.Parameter:
		_, ok := loc.Type().(*types.Pointer)
		return !ok
	case *ssa.FieldAddr:
		isLocalAddr(loc.X)
	case *ssa.IndexAddr:
		isLocalAddr(loc.X)
	case *ssa.UnOp:
		isLocalAddr(loc.X)
	case *ssa.Alloc:
		if !loc.Heap {
			return true // false heap means local alloc
		}
	default:
		return false
	}
	return false
}

func isSynthetic(fn *ssa.Function) bool { // ignore functions that are NOT true source functions
	return fn.Synthetic != "" || fn.Pkg == nil
}

func staticAnalysis(args []string) error {
	cfg := &packages.Config{
		Mode:  packages.LoadAllSyntax, // the level of information returned for each package
		Dir:   "",                     // directory in which to run the build system's query tool
		Tests: false,                  // setting Tests will include related test packages
	}
	log.Info("Loading input packages...")
	initial, err := packages.Load(cfg, args...)
	if err != nil {
		return err
	}
	if packages.PrintErrors(initial) > 0 {
		return fmt.Errorf("packages contain errors")
	} else if len(initial) == 0 {
		return fmt.Errorf("package list empty")
	}

	// Print the names of the source files
	// for each package listed on the command line.
	for nP, pkg := range initial {
		log.Info(pkg.ID, pkg.GoFiles)
		log.Infof("Done  -- %d packages loaded", nP+1)
	}

	// Create and build SSA-form program representation.
	prog, pkgs := ssautil.AllPackages(initial, 0)

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
		prog:      prog,
		pkgs:      pkgs,
		mains:     mains,
		ptaConfig: config,
		RWinsMap:  make(map[ssa.Instruction]graph.Node),
	}

	log.Info("Compiling stack trace for every Goroutine... ")
	log.Debug(strings.Repeat("-", 35), "Stack trace begins", strings.Repeat("-", 35))
	Analysis.visitAllInstructions(mains[0].Func("main"), 0)
	log.Debug(strings.Repeat("-", 35), "Stack trace ends", strings.Repeat("-", 35))
	totalIns := 0
	for g, _ := range RWIns {
		totalIns += len(RWIns[g])
	}
	log.Info("Done  -- ", Analysis.analysisStat.nGoroutine, " goroutines analyzed! ", totalIns, " instructions of interest detected! ")

	log.Info("Building Happens-Before graph... ")
	Analysis.HBgraph = graph.New(graph.Directed)
	var prevN graph.Node
	var goCaller []graph.Node
	var waitingN graph.Node
	for nGo, insSlice := range RWIns {
		for i, anIns := range insSlice {
			if nGo == 0 && i == 0 { // main goroutine, first instruction
				if _, ok := anIns.(*ssa.Go); !ok {
					prevN = Analysis.HBgraph.MakeNode() // initiate for future nodes
					*prevN.Value = anIns
				} else {
					prevN = Analysis.HBgraph.MakeNode() // initiate for future nodes
					*prevN.Value = anIns
					goCaller = append(goCaller, prevN) // sequentially store go calls in the same goroutine
				}
			} else {
				currN := Analysis.HBgraph.MakeNode()
				*currN.Value = anIns
				if nGo != 0 && i == 0 { // worker goroutine, first instruction
					prevN = goCaller[0]
					goCaller = goCaller[1:] // pop from stack
					err := Analysis.HBgraph.MakeEdge(prevN, currN)
					if err != nil {
						log.Fatal(err)
					}
				} else if _, ok := anIns.(*ssa.Go); ok {
					goCaller = append(goCaller, currN) // sequentially store go calls in the same goroutine
					err := Analysis.HBgraph.MakeEdge(prevN, currN)
					if err != nil {
						log.Fatal(err)
					}
				} else {
					err := Analysis.HBgraph.MakeEdge(prevN, currN)
					if err != nil {
						log.Fatal(err)
					}
				}
				prevN = currN
			}
			if isReadIns(anIns) || isWriteIns(anIns) {
				Analysis.RWinsMap[anIns] = prevN
			} else if callIns, ok := anIns.(*ssa.Call); ok { // taking care of WG operations. TODO: identify different WG instances
				if callIns.Call.Value.Name() == "Wait" {
					waitingN = prevN
				} else if callIns.Call.Value.Name() == "Done" {
					err := Analysis.HBgraph.MakeEdge(prevN, waitingN)
					if err != nil {
						log.Fatal(err)
					}
				}
			}
		}
	}
	log.Info("Done  -- Happens-Before graph built ")

	log.Info("Checking for data races... ")
	result, err := pointer.Analyze(Analysis.ptaConfig) // conduct pointer analysis
	if err != nil {
		log.Fatal(err)
	}
	Analysis.result = result
	Analysis.checkRacyPairs()
	return nil
}

func (a *analysis) visitAllInstructions(fn *ssa.Function, goID int) {
	a.analysisStat.nGoroutine = goID + 1 // keep count of goroutine quantity
	if !isSynthetic(fn) {                // if function is NOT synthetic
		for _, excluded := range excludedPkgs { // TODO: need revision
			if fn.Pkg.Pkg.Name() == excluded {
				return
			}
		}
		if fn.Name() == "main" {
			levels[goID] = 0 // initialize level count at main entry
			updateRecords(fn.Name(), goID, "PUSH ")
			goStack = append(goStack, []string{}) // initialize first interior slice for main goroutine
			a.trieMap = make(map[fnInfo]*trie)
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
	var toDefer []ssa.Instruction // stack storing deferred calls
	repeatSwitch := false         // triggered when encountering basic blocks for body of a forloop
	for i := 0; i < len(fnBlocks); i++ {
		aBlock := fnBlocks[i]
		if aBlock.Comment == "recover" {
			continue
		}
		if strings.HasSuffix(aBlock.Comment, ".done") && i != len(fnBlocks)-1 && sliceContainsBloc(toAppend, aBlock) { // ignore return block if it doesn't have largest index
			continue
		}
		if aBlock.Comment == "for.body" || aBlock.Comment == "rangeindex.body" { // repeat unrolling of forloop
			if repeatSwitch == false {
				i--
				repeatSwitch = true
			} else {
				repeatSwitch = false
			}
		}
		for _, theIns := range aBlock.Instrs { // examine each instruction
			if theIns.String() == "rundefers" { // execute deferred calls at this index
				for _, dIns := range toDefer {
					deferIns := dIns.(*ssa.Defer)
					if _, ok := deferIns.Call.Value.(*ssa.Builtin); ok {
						continue
					}
					if fromPkgsOfInterest(deferIns.Call.StaticCallee()) && deferIns.Call.StaticCallee().Pkg.Pkg.Name() != "sync" {
						fnName := deferIns.Call.Value.Name()
						fnName = checkTokenNameDefer(fnName, deferIns)
						if !a.exploredFunction(deferIns.Call.StaticCallee(), goID, theIns) {
							updateRecords(fnName, goID, "PUSH ")
							RWIns[goID] = append(RWIns[goID], dIns)
							a.visitAllInstructions(deferIns.Call.StaticCallee(), goID)
						}
					} else if deferIns.Call.StaticCallee().Name() == "Unlock" {
						lockLoc := deferIns.Call.Args[0]
						if k := a.lockSetContainsAt(lockSet, lockLoc); k >= 0 {
							lockSet = deleteFromLockSet(lockSet, k)
						}
					} else if deferIns.Call.StaticCallee().Name() == "Done" {
						RWIns[goID] = append(RWIns[goID], theIns)
					}
				}
			}
			for _, ex := range excludedPkgs { // TODO: need revision
				if !isSynthetic(fn) && ex == theIns.Parent().Pkg.Pkg.Name() {
					return
				}
			}
			switch examIns := theIns.(type) {
			case *ssa.MakeChan: // channel creation op
				a.insMakeChan(examIns)
			case *ssa.Send: // channel send op
				a.insSend(examIns, goID, theIns)
			case *ssa.Store: // write op
				a.insStore(examIns, goID, theIns)
			case *ssa.UnOp:
				a.insUnOp(examIns, goID, theIns)
			case *ssa.FieldAddr:
				a.insFieldAddr(examIns, goID, theIns)
			case *ssa.Lookup: // look up element index, read op
				a.insLookUp(examIns, goID, theIns)
			case *ssa.ChangeType: // a value-preserving type change, write op
				a.insChangeType(examIns, goID, theIns)
			case *ssa.Defer:
				toDefer = append([]ssa.Instruction{theIns}, toDefer...)
			case *ssa.MakeInterface: // construct instance of interface type
				a.insMakeInterface(examIns, goID, theIns)
			case *ssa.Call:
				a.insCall(examIns, goID, theIns)
			case *ssa.Go: // for spawning of goroutines
				a.insGo(examIns, goID, theIns)
			case *ssa.Return:
				RWIns[goID] = append(RWIns[goID], theIns)
			}
		}
	}
	// done with all instructions in function body, now pop the function
	fnName := fn.Name()
	if fnName == storeIns[len(storeIns)-1] {
		updateRecords(fnName, goID, "POP  ")
	}
	if len(storeIns) == 0 && len(workList) != 0 { // finished reporting current goroutine and workList isn't empty
		nextGoInfo := workList[0] // get the goroutine info at head of workList
		workList = workList[1:]   // pop goroutine info from head of workList
		a.newGoroutine(nextGoInfo)
	} else {
		return
	}
}

func (a *analysis) newGoroutine(info goroutineInfo) {
	storeIns = append(storeIns, info.entryMethod)
	if info.goID >= len(RWIns) { // initialize interior slice for new goroutine
		RWIns = append(RWIns, []ssa.Instruction{})
	}
	RWIns[info.goID] = append(RWIns[info.goID], info.goIns)
	goNames[info.goID] = info.entryMethod
	log.Debug(strings.Repeat("-", 35), "Goroutine ", info.entryMethod, strings.Repeat("-", 35), "[", info.goID, "]")
	log.Debug(strings.Repeat(" ", levels[info.goID]), "PUSH ", info.entryMethod, " at lvl ", levels[info.goID])
	levels[info.goID]++
	switch info.goIns.Call.Value.(type) {
	case *ssa.MakeClosure:
		a.visitAllInstructions(info.goIns.Call.StaticCallee(), info.goID)
	case *ssa.TypeAssert:
		a.visitAllInstructions(paramFunc.(*ssa.MakeClosure).Fn.(*ssa.Function), info.goID)
	default:
		a.visitAllInstructions(info.goIns.Call.StaticCallee(), info.goID)
	}
}

func (a *analysis) exploredFunction(fn *ssa.Function, goID int, theIns ssa.Instruction) bool {
	//if !fromPkgsOfInterest(fn) { // for temporary debugging purposes only
	//	return true
	//}
	if sliceContainsInsAt(RWIns[goID], theIns) >= 0 {
		return true
	}
	//if sliceContainsStr(storeIns, fn.Name()) { // for temporary debugging purposes only
	//	return true
	//}
	var visitedIns []ssa.Instruction
	if len(RWIns) > 0 {
		visitedIns = RWIns[goID]
	} else {
		visitedIns = []ssa.Instruction{}
	}
	csSlice, csStr := insToCallStack(visitedIns)
	if sliceContainsStrCtr(csSlice, fn.Name()) > trieLimit {
		return true
	}
	fnKey := fnInfo{
		fnName:     fn.Name(),
		contextStr: csStr,
	}
	if existingTrieNode, ok := a.trieMap[fnKey]; ok {
		existingTrieNode.budget++ // increment the number of times for calling the function under the current context by one
	} else {
		newTrieNode := trie{
			fnName:    fn.Name(),
			budget:    1,
			fnContext: csSlice,
		}
		a.trieMap[fnKey] = &newTrieNode
	}
	return a.trieMap[fnKey].isBudgetExceeded()
}

func (t trie) isBudgetExceeded() bool {
	if t.budget > trieLimit {
		return true
	}
	return false
}