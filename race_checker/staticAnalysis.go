package main

import   (
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
	if fn.Pkg.Pkg.Name() == "main" || fn.Pkg.Pkg.Name() == "cli"  {
		return true
	}
	if !strings.HasPrefix(fn.Pkg.Pkg.Path(), fromPath) { // path is dependent on tested program
		return false
	}
	return true
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
		BuildCallGraph: false,
	}
	Analysis = &analysis{
		prog:         prog,
		pkgs:         pkgs,
		mains:        mains,
		ptaConfig:    config,
		RWinsMap:     make(map[goIns]graph.Node),
		insDRA:       0,
		levels:       make(map[int]int),
		lockMap:       make(map[ssa.Instruction][]ssa.Value),
		RlockMap:      make(map[ssa.Instruction][]ssa.Value),
		goLockset:     make(map[int][]ssa.Value),
		goRLockset:    make(map[int][]ssa.Value),
		mapFreeze:     false,
		goCaller:      make(map[int]int),
		goNames:       make(map[int]string),
		chanBufMap:    make(map[string][]*ssa.Send),
		insertIndMap:  make(map[string]int),
		chanMap:       make(map[ssa.Instruction][]string), // map each read/write access to a list of channels with value(s) already sent to it
		selectCaseBegin: make(map[ssa.Instruction]string),
		selectCaseEnd: make(map[ssa.Instruction]string),
		selectDone:    make(map[ssa.Instruction]ssa.Instruction),
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

	result, err := pointer.Analyze(Analysis.ptaConfig) // conduct pointer analysis
	if err != nil {
		log.Fatal(err)
	}
	Analysis.result = result

	log.Info("Building Happens-Before graph... ")
	Analysis.HBgraph = graph.New(graph.Directed)
	var prevN graph.Node
	var goCaller []graph.Node
	var selectN []graph.Node
	var selIns *ssa.Select
	var readyCh []string
	var selectDoneN []graph.Node
	waitingN := make(map[*ssa.Call]graph.Node)
	chanRecvs := make(map[string]graph.Node) // map channel name to graph node
	for nGo, insSlice := range Analysis.RWIns {
		for i, anIns := range insSlice {
			insKey := goIns{ins: anIns, goID: nGo}
			if nGo == 0 && i == 0 { // main goroutine, first instruction
				prevN = Analysis.HBgraph.MakeNode() // initiate for future nodes
				*prevN.Value = insKey
				if _, ok := anIns.(*ssa.Go); ok {
					goCaller = append(goCaller, prevN) // sequentially store go calls in the same goroutine
				}
			} else {
				currN := Analysis.HBgraph.MakeNode()
				*currN.Value = insKey
				if nGo != 0 && i == 0 { // worker goroutine, first instruction
					prevN = goCaller[0] // first node in subroutine
					goCaller = goCaller[1:]
				} else if _, ok := anIns.(*ssa.Go); ok {
					goCaller = append(goCaller, currN) // sequentially store go calls in the same goroutine
				} else if _, ok1 := anIns.(*ssa.Select); ok1 {
					selectN = append(selectN, currN) // select node
					selIns = anIns.(*ssa.Select)
					readyCh = Analysis.insSelect(selIns, nGo, anIns)
					if num, ind := chReady(readyCh); num == 1 && selIns.Blocking { // exactly one ready channel
						chanRecvs[readyCh[ind]] = currN // certainty of traversal here
					}
				}
				if _, ok2 := Analysis.selectDone[anIns]; ok2 {
					selectDoneN = append(selectDoneN, currN)
				}
				if ch, ok := Analysis.selectCaseBegin[anIns]; ok && sliceContainsStr(readyCh, ch) {
					err := Analysis.HBgraph.MakeEdge(selectN[0], currN) // select node to ready case
					if err != nil {
						log.Fatal(err)
					}
				} else if ch1, ok1 := Analysis.selectCaseEnd[anIns]; ok1 && sliceContainsStr(readyCh, ch1) {
					err := Analysis.HBgraph.MakeEdge(currN, selectDoneN[0]) // ready case to select done
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
			// Create additional edges:
			if Analysis.isReadIns(anIns) || isWriteIns(anIns) {
				Analysis.RWinsMap[insKey] = prevN
			} else if callIns, ok := anIns.(*ssa.Call); ok { // taking care of WG operations. TODO: identify different WG instances
				if callIns.Call.Value.Name() == "Wait" {
					waitingN[callIns] = prevN // store Wait node for later edge creation TO this node
				} else if callIns.Call.Value.Name() == "Done" {
					for wIns, wNode := range waitingN {
						if Analysis.sameAddress(callIns.Call.Args[0], wIns.Call.Args[0]) {
							err := Analysis.HBgraph.MakeEdge(prevN, wNode) // create edge from Done node to Wait node
							if err != nil {
								log.Fatal(err)
							}
						}
					}
				}
			} else if dIns, ok1 := anIns.(*ssa.Defer); ok1 {
				if dIns.Call.Value.Name() == "Done" {
					for wIns, wNode := range waitingN {
						if Analysis.sameAddress(dIns.Call.Args[0], wIns.Call.Args[0]) {
							err := Analysis.HBgraph.MakeEdge(prevN, wNode) // create edge from Done node to Wait node
							if err != nil {
								log.Fatal(err)
							}
						}
					}
				}
			}
			if sendIns, ok := anIns.(*ssa.Send); ok { // detect matching channel send operations
				if matchingRcv, ok1 := sendIns.Chan.(*ssa.UnOp); ok1 {
					if edgeTo, ok2 := chanRecvs[matchingRcv.X.Name()]; ok2 {
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

	log.Info("Checking for data races... ")
	Analysis.checkRacyPairs()
	return nil
}

// visitAllInstructions visits each line and calls the corresponding helper function to drive the tool
func (a *analysis) visitAllInstructions(fn *ssa.Function, goID int) {
	a.analysisStat.nGoroutine = goID + 1 // keep count of goroutine quantity
	if !isSynthetic(fn) {                // if function is NOT synthetic
		if !fromPkgsOfInterest(fn) {
			return
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
	var readyChans []string       // may be better to use maps instead
	var selectIns *ssa.Select
	var selCount int              // index of select.done block
	var toDefer []ssa.Instruction // stack storing deferred calls
	var toUnlock []ssa.Value
	var toRUnlock []ssa.Value
	repeatSwitch := false         // triggered when encountering basic blocks for body of a forloop
	for i := 0; i < len(fnBlocks); i++ {
		aBlock := fnBlocks[i]
		if aBlock.Comment == "recover" {
			continue
		}
		if aBlock.Comment == "select.done" || aBlock.Comment == "rangechan.done" {
			selCount = aBlock.Index
			a.selectDone[aBlock.Instrs[0]] = selectIns // map first ins in select.done to select instruction
			a.RWIns[goID] = append(a.RWIns[goID], aBlock.Instrs[0])
		}
		if aBlock.Comment == "select.body" {
			if selCount == 0 { // start counting
				selCount = aBlock.Index
			}
			caseNo := (aBlock.Index - selCount)/2
			if readyChans[caseNo] == "" { // channel not ready (skip)
				continue
			} else { // get beginning of clause
				a.selectCaseBegin[aBlock.Instrs[0]] = readyChans[caseNo] // map first instruction in case to channel name
				a.selectCaseEnd[aBlock.Instrs[len(aBlock.Instrs)-1]] = readyChans[caseNo] // map last instruction in case to channel name
			}
		}
		if aBlock.Comment == "select.next" && !selectIns.Blocking {
			caseNo := (aBlock.Index - selCount)/2
			if readyChans[caseNo] == "defaultCase" {
				a.selectCaseBegin[aBlock.Instrs[0]] = readyChans[caseNo] // map first instruction in case to channel name
				a.selectCaseEnd[aBlock.Instrs[len(aBlock.Instrs)-1]] = readyChans[caseNo] // map last instruction in case to channel name
			}
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
						a.ptaConfig.AddQuery(lockLoc)
						toUnlock = append(toUnlock, lockLoc)
					} else if deferIns.Call.StaticCallee().Name() == "RUnlock" {
						RlockLoc := deferIns.Call.Args[0]
						a.ptaConfig.AddQuery(RlockLoc)
						toRUnlock = append(toRUnlock, RlockLoc)
					} else if deferIns.Call.Value.Name() == "Done" {
						a.RWIns[goID] = append(a.RWIns[goID], dIns)
						a.ptaConfig.AddQuery(deferIns.Call.Args[0])
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
				if _, ok := examIns.Addr.(*ssa.Alloc); ok && k > 0 { // variable initialization
					switch aBlock.Instrs[k-1].(type) {
					case *ssa.Alloc:
					case *ssa.MakeChan: // channel object
					case *ssa.Extract: // tuple index
						if k < 2 {
							a.insStore(examIns, goID, theIns)
						} else {
							if _, ok1 := aBlock.Instrs[k-2].(*ssa.Alloc); !ok1 {
								a.insStore(examIns, goID, theIns)
							}
						}
					default:
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
				a.RWIns[goID] = append(a.RWIns[goID], theIns)
				toDefer = append([]ssa.Instruction{theIns}, toDefer...)
			case *ssa.MakeInterface: // construct instance of interface type
				a.insMakeInterface(examIns, goID, theIns)
			case *ssa.Call:
				unlockOps, runlockOps := a.insCall(examIns, goID, theIns)
				toUnlock = append(toUnlock, unlockOps...)
				toRUnlock = append(toRUnlock, runlockOps...)
			case *ssa.Go: // for spawning of goroutines
				a.insGo(examIns, goID, theIns)
			case *ssa.Return:
				a.RWIns[goID] = append(a.RWIns[goID], theIns)
			case *ssa.MapUpdate:
				a.insMapUpdate(examIns, goID, theIns)
			case *ssa.Select:
				readyChans = a.insSelect(examIns, goID, theIns)
				selectIns = examIns
			case *ssa.Jump:
				a.RWIns[goID] = append(a.RWIns[goID], theIns)
			default:
				if _, ok := a.selectCaseBegin[theIns]; ok && a.RWIns[goID][len(a.RWIns[goID])-1] != theIns {
					a.RWIns[goID] = append(a.RWIns[goID], theIns)
				} else if _, ok1 := a.selectCaseEnd[theIns]; ok1 && a.RWIns[goID][len(a.RWIns[goID])-1] != theIns {
					a.RWIns[goID] = append(a.RWIns[goID], theIns)
				}
			}
			if k == len(aBlock.Instrs)-1 && a.mapFreeze { // TODO: this can happen too early
				a.mapFreeze = false
			}
		}
	}
	if len(toUnlock) > 0 {
		for _, loc := range toUnlock {
			if goID == 0 {
				if z := a.lockSetContainsAt(a.lockSet, loc); z >= 0 {
					log.Trace("Unlocking ", loc.String(), "  (", a.lockSet[z].Pos(), ") removing index ", z, " from: ", lockSetVal(a.lockSet))
					a.lockSet = a.deleteFromLockSet(a.lockSet, z)
				}
			} else {
				if z := a.lockSetContainsAt(a.goLockset[goID], loc); z >= 0 {
					log.Trace("Unlocking ", loc.String(), "  (", a.goLockset[goID][z].Pos(), ") removing index ", z, " from: ", lockSetVal(a.goLockset[goID]))
					a.goLockset[goID] = a.deleteFromLockSet(a.goLockset[goID], z)
				}
			}
		}
	}
	if len(toRUnlock) > 0 {
		for _, rloc := range toRUnlock {
			if goID == 0 { // main goroutine
				if z := a.lockSetContainsAt(a.RlockSet, rloc); z >= 0 {
					log.Trace("RUnlocking ", rloc.String(), "  (", a.RlockSet[z].Pos(), ") removing index ", z, " from: ", lockSetVal(a.RlockSet))
					a.RlockSet = a.deleteFromLockSet(a.RlockSet, z)
				}
			} else { // worker goroutine
				if z := a.lockSetContainsAt(a.goRLockset[goID], rloc); z >= 0 {
					log.Trace("RUnlocking ", rloc.String(), "  (", a.goRLockset[goID][z].Pos(), ") removing index ", z, " from: ", lockSetVal(a.goRLockset[goID]))
					a.goRLockset[goID] = a.deleteFromLockSet(a.goRLockset[goID], z)

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

// newGoroutine goes through the goroutine, logs its info, and goes through the instructions within
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
