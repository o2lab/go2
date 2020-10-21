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

// fromPkgsOfInterest determines if a function is from a package of interest
func fromPkgsOfInterest(fn *ssa.Function) bool {
	if fn.Pkg == nil || fn.Pkg.Pkg == nil {
		return false
	}
	for _, excluded := range excludedPkgs {
		if fn.Pkg.Pkg.Name() == excluded {
			return false
		}
	}
	return allPkg
}

// isLocalAddr returns whether location is a local address or not
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
			return true
		}
	default:
		return false
	}
	return false
}

// isSynthetic returns whether fn is synthetic or not
func isSynthetic(fn *ssa.Function) bool { // ignore functions that are NOT true source functions
	return fn.Synthetic != "" || fn.Pkg == nil
}

// staticAnalysis builds a Happens-Before Graph and calls other functions like visitAllInstructions to drive the program further
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
	for _, pkg := range initial {
		log.Info(pkg.ID, pkg.GoFiles)
	}
	log.Infof("Done  -- packages loaded")

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
		prog:         prog,
		pkgs:         pkgs,
		mains:        mains,
		ptaConfig:    config,
		RWinsMap:     make(map[goIns]graph.Node),
		insDRA:       0,
		levels:       make(map[int]int),
		lockMap:      make(map[ssa.Instruction][]ssa.Value),
		RlockMap:     make(map[ssa.Instruction][]ssa.Value),
		goCaller:     make(map[int]int),
		goNames:      make(map[int]string),
		chanBufMap:   make(map[string][]*ssa.Send),
		insertIndMap: make(map[string]int),
		chanMap:      make(map[ssa.Instruction][]string), // map each read/write access to a list of channels with value(s) already sent to it
	}

	log.Info("Compiling stack trace for every Goroutine... ")
	log.Debug(strings.Repeat("-", 35), "Stack trace begins", strings.Repeat("-", 35))
	Analysis.visitAllInstructions(mains[0].Func("main"), 0)
	log.Debug(strings.Repeat("-", 35), "Stack trace ends", strings.Repeat("-", 35))
	totalIns := 0
	for g, _ := range Analysis.RWIns {
		totalIns += len(Analysis.RWIns[g])
	}
	log.Info("Done  -- ", len(Analysis.RWIns), " goroutines analyzed! ", totalIns, " instructions of interest detected! ")
	if len(Analysis.RWIns) < 2 {
		log.Debug("race is not possible in one goroutine")
		return nil
	}
	log.Info("Building Happens-Before graph... ")
	Analysis.HBgraph = graph.New(graph.Directed)
	var prevN graph.Node
	var goCaller []graph.Node
	waitingN := make(map[string]graph.Node) // map WG name to graph node
	chanRecvs := make(map[string]graph.Node) // map channel name to graph node
	for nGo, insSlice := range Analysis.RWIns {
		for i, anIns := range insSlice {
			insKey := goIns{ins: anIns, goID: nGo}
			if nGo == 0 && i == 0 { // main goroutine, first instruction
				if _, ok := anIns.(*ssa.Go); !ok {
					prevN = Analysis.HBgraph.MakeNode() // initiate for future nodes
					*prevN.Value = insKey
				} else {
					prevN = Analysis.HBgraph.MakeNode() // initiate for future nodes
					*prevN.Value = insKey
					goCaller = append(goCaller, prevN) // sequentially store go calls in the same goroutine
				}
			} else {
				currN := Analysis.HBgraph.MakeNode()
				*currN.Value = insKey
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
			if Analysis.isReadIns(anIns) || isWriteIns(anIns) {
				Analysis.RWinsMap[insKey] = prevN
			} else if callIns, ok := anIns.(*ssa.Call); ok { // taking care of WG operations. TODO: identify different WG instances
				if callIns.Call.Value.Name() == "Wait" {
					var wgName string
					switch wg := callIns.Call.Args[0].(type) {
					case *ssa.Alloc:
						wgName = wg.Comment
					case *ssa.FreeVar:
						wgName = wg.Name()
					}
					waitingN[wgName] = prevN // store Wait node for later edge creation TO this node
				} else if callIns.Call.Value.Name() == "Done" {
					var wgName string
					switch wg := callIns.Call.Args[0].(type) {
					case *ssa.Alloc:
						wgName = wg.Comment
					case *ssa.FreeVar:
						wgName = wg.Name()
					}
					if edgeTo, ok := waitingN[wgName]; ok {
						err := Analysis.HBgraph.MakeEdge(prevN, edgeTo) // create edge from Done node to Wait node
						if err != nil {
							log.Fatal(err)
						}
					}
				}
			}
			if recvIns, ok := anIns.(*ssa.UnOp); ok { // detect (blocking) channel receive operation
				if rcvIns, ok1 := recvIns.X.(*ssa.Alloc); ok1 && sliceContainsStr(Analysis.selectedChans, rcvIns.Comment) {
					chanRecvs[rcvIns.Comment] = prevN
				}
			} else if sendIns, ok := anIns.(*ssa.Send); ok { // detect matching channel send operations
				if sndIns, ok := sendIns.Chan.(*ssa.UnOp); ok && sliceContainsStr(Analysis.selectedChans, sndIns.X.Name()) {
					if edgeTo, ok := chanRecvs[sndIns.X.Name()]; ok {
						err := Analysis.HBgraph.MakeEdge(prevN, edgeTo) // create edge from Send node to Receive node
						if err != nil {
							log.Fatal(err)
						}
					}
				}
			}
		}
	}
	log.Info("Done  -- Happens-Before graph built ")
	result, err := pointer.Analyze(Analysis.ptaConfig) // conduct pointer analysis
	if err != nil {
		log.Fatal(err)
	}
	Analysis.result = result
	log.Info("Checking for data races... ")
	Analysis.checkRacyPairs()
	return nil
}

// visitAllInstructions visits each line and calls the corresponding helper function to drive the tool
func (a *analysis) visitAllInstructions(fn *ssa.Function, goID int) {
	a.analysisStat.nGoroutine = goID + 1 // keep count of goroutine quantity
	if !isSynthetic(fn) {                // if function is NOT synthetic
		for _, excluded := range excludedPkgs { // TODO: need revision
			if fn.Pkg.Pkg.Name() == excluded {
				return
			}
		}
		if fn.Name() == "main" {
			a.levels[goID] = 0 // initialize level count at main entry
			a.updateRecords(fn.Name(), goID, "PUSH ")
			a.goStack = append(a.goStack, []string{}) // initialize first interior slice for main goroutine
			a.trieMap = make(map[fnInfo]*trie)
		}
	}
	if _, ok := a.levels[goID]; !ok && goID > 0 { // initialize level counter for new goroutine
		a.levels[goID] = 1
	}
	if goID >= len(a.RWIns) { // initialize interior slice for new goroutine
		a.RWIns = append(a.RWIns, []ssa.Instruction{})
	}
	fnBlocks := fn.Blocks
	var caseStatus []int
	var skipBBInd []int // these basic blocks and their successor blocks shall be omitted
	var defaultBB []int
	var toAppend []*ssa.BasicBlock
	var toDefer []ssa.Instruction // stack storing deferred calls
	repeatSwitch := false         // triggered when encountering basic blocks for body of a forloop
	for i := 0; i < len(fnBlocks); i++ {
		aBlock := fnBlocks[i]
		if aBlock.Comment == "recover" {
			continue
		}
		if aBlock.Comment == "select.done" {
			defaultBB = append(defaultBB, aBlock.Preds[len(aBlock.Preds)-1].Index) // TODO: consider successor blocks
		}
		if aBlock.Comment == "select.body" && len(caseStatus) > 0 {
			skipBBInd = append(skipBBInd, aBlock.Index) // skipBBInd correlates with caseStatus
			if len(skipBBInd) <= len(caseStatus) && caseStatus[len(skipBBInd)-1] == 0 { // channel has no value (skip)
				continue
			}
			if len(skipBBInd) > len(caseStatus) && sliceContainsInt(skipBBInd, aBlock.Preds[0].Index) {
				continue
			}
		}
		if strings.HasSuffix(aBlock.Comment, ".done") && i != len(fnBlocks)-1 && sliceContainsBloc(toAppend, aBlock) && aBlock.Comment != "select.done" { // ignore return block if it doesn't have largest index
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
		for k, theIns := range aBlock.Instrs { // examine each instruction
			if theIns.String() == "rundefers" { // execute deferred calls at this index
				for _, dIns := range toDefer {
					deferIns := dIns.(*ssa.Defer)
					if _, ok := deferIns.Call.Value.(*ssa.Builtin); ok {
						continue
					}
					if deferIns.Call.StaticCallee() == nil {
						continue
					} else if fromPkgsOfInterest(deferIns.Call.StaticCallee()) && deferIns.Call.StaticCallee().Pkg.Pkg.Name() != "sync" {
						fnName := deferIns.Call.Value.Name()
						fnName = checkTokenNameDefer(fnName, deferIns)
						if !a.exploredFunction(deferIns.Call.StaticCallee(), goID, theIns) {
							a.updateRecords(fnName, goID, "PUSH ")
							a.RWIns[goID] = append(a.RWIns[goID], dIns)
							a.visitAllInstructions(deferIns.Call.StaticCallee(), goID)
						}
					} else if deferIns.Call.StaticCallee().Name() == "Unlock" {
						lockLoc := deferIns.Call.Args[0]
						if k := a.lockSetContainsAt(a.lockSet, lockLoc); k >= 0 {
							a.lockSet = deleteFromLockSet(a.lockSet, k)
						}
					} else if deferIns.Call.StaticCallee().Name() == "Done" {
						a.RWIns[goID] = append(a.RWIns[goID], theIns)
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
				if k > 0 { // check if previous instruction is *ssa.Alloc
					if assign, ok := aBlock.Instrs[k-1].(*ssa.Alloc); ok && assign == examIns.Addr {
						// assignment statement won't race
					} else {
						a.insStore(examIns, goID, theIns)
					}
				} else {
					a.insStore(examIns, goID, theIns)
				}
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
				a.RWIns[goID] = append(a.RWIns[goID], theIns)
			case *ssa.MapUpdate:
				a.insMapUpdate(examIns, goID, theIns)
			case *ssa.Select:
				if examIns.Blocking { // only analyze channels with available values
					caseStatus = a.insSelect(examIns, goID, theIns)
				} else { // select contains default case, which becomes race-prone (non-blocking channel ops)
					//caseStatus = a.insSelect(examIns, goID, theIns)
				}
			}
		}
	}
	// done with all instructions in function body, now pop the function
	fnName := fn.Name()
	if fnName == a.storeIns[len(a.storeIns)-1] {
		a.updateRecords(fnName, goID, "POP  ")
	}
	if len(a.storeIns) == 0 && len(a.workList) != 0 { // finished reporting current goroutine and workList isn't empty
		nextGoInfo := a.workList[0] // get the goroutine info at head of workList
		a.workList = a.workList[1:] // pop goroutine info from head of workList
		a.newGoroutine(nextGoInfo)
	} else {
		return
	}
}

// newGoroutine goes through the go routine, logs its info, and goes through the instructions within,   the r isn't capitalized?
func (a *analysis) newGoroutine(info goroutineInfo) {
	a.storeIns = append(a.storeIns, info.entryMethod)
	if info.goID >= len(a.RWIns) { // initialize interior slice for new goroutine
		a.RWIns = append(a.RWIns, []ssa.Instruction{})
	}
	a.RWIns[info.goID] = append(a.RWIns[info.goID], info.goIns)
	a.goNames[info.goID] = info.entryMethod
	log.Debug(strings.Repeat("-", 35), "Goroutine ", info.entryMethod, strings.Repeat("-", 35), "[", info.goID, "]")
	log.Debug(strings.Repeat(" ", a.levels[info.goID]), "PUSH ", info.entryMethod, " at lvl ", a.levels[info.goID])
	a.levels[info.goID]++
	switch info.goIns.Call.Value.(type) {
	case *ssa.MakeClosure:
		a.visitAllInstructions(info.goIns.Call.StaticCallee(), info.goID)
	case *ssa.TypeAssert:
		a.visitAllInstructions(a.paramFunc.(*ssa.MakeClosure).Fn.(*ssa.Function), info.goID)
	default:
		a.visitAllInstructions(info.goIns.Call.StaticCallee(), info.goID)
	}
}

// exploredFunction determines if we already visited this function
func (a *analysis) exploredFunction(fn *ssa.Function, goID int, theIns ssa.Instruction) bool {
	if efficiency && !fromPkgsOfInterest(fn) { // for temporary debugging purposes only
		return true
	}
	if sliceContainsInsAt(a.RWIns[goID], theIns) >= 0 {
		return true
	}
	if efficiency && sliceContainsStr(a.storeIns, fn.Name()) { // for temporary debugging purposes only
		return true
	}
	var visitedIns []ssa.Instruction
	if len(a.RWIns) > 0 {
		visitedIns = a.RWIns[goID]
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

// isBudgetExceeded determines if the budget has exceeded the limit
func (t trie) isBudgetExceeded() bool {
	if t.budget > trieLimit {
		return true
	}
	return false
}
