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
		goLockset:    make(map[int][]ssa.Value),
		goRLockset:   make(map[int][]ssa.Value),
		mapFreeze:    false,
		goCaller:     make(map[int]int),
		goNames:      make(map[int]string),
		chanBufMap:   make(map[string][]*ssa.Send),
		insertIndMap: make(map[string]int),
		chanMap:      make(map[ssa.Instruction][]string), // map each read/write access to a list of channels with value(s) already sent to it
		selectedChans:make(map[string]ssa.Instruction),
		selectDefault:make(map[*ssa.Select]ssa.Instruction), // map select statement to first instruction in its default block
		afterSelect:  make(map[ssa.Instruction]ssa.Instruction),
		selectHB:	  make(map[ssa.Instruction]ssa.Instruction),
		selectafterHB:	  make(map[ssa.Instruction]ssa.Instruction),
		serverWorker:     0,
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
	var caseEnd []ssa.Instruction
	var caseN []graph.Node
	waitingN := make(map[*ssa.Call]graph.Node)
	chanRecvs := make(map[string]graph.Node) // map channel name to graph node
	beforeSel := make(map[ssa.Instruction]graph.Node) // map ins before select straight to default clause
	var selDefault []ssa.Instruction
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
					prevN = goCaller[0]
					goCaller = goCaller[1:] // pop from stack
				} else if _, ok := anIns.(*ssa.Go); ok {
					goCaller = append(goCaller, currN) // sequentially store go calls in the same goroutine
				//} else if sliceContainsInsAt(selDefault, anIns) > -1 {
				//	for from, to := range Analysis.selectHB {
				//		if to == anIns {
				//			prevN = beforeSel[from]
				//			err := Analysis.HBgraph.MakeEdge(prevN, currN)
				//			if err != nil {
				//				fmt.Println(len(beforeSel))
				//				log.Fatal(err)
				//			}
				//		}
				//		break
				//	}
				} else {
					if sliceContainsInsAt(caseEnd, anIns) > -1 { // last instruction in clause
						caseN = append(caseN, currN)
					}
				}
				if _, ok := Analysis.afterSelect[anIns]; len(Analysis.selectedChans)<=1||!ok { // first instruction after encountering select
					err := Analysis.HBgraph.MakeEdge(prevN, currN)
					if err != nil {
						log.Fatal(err)
					}
				}else{
					err := Analysis.HBgraph.MakeEdge(beforeSel[Analysis.afterSelect[anIns]], currN)
					if err != nil {
						log.Fatal(err)
					}
				}
				prevN = currN
			}
			if _, ok := Analysis.selectHB[anIns]; ok { // last instruction before encountering select
				beforeSel[anIns] = prevN
			}
			if _, ok := Analysis.selectafterHB[anIns]; ok { // last instruction before encountering select
				beforeSel[anIns] = prevN
			}
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
			} else if selectIns, ok := anIns.(*ssa.Select); ok && !selectIns.Blocking {
				selDefault = append(selDefault, Analysis.selectDefault[selectIns])
			}
			if recvIns, ok := anIns.(*ssa.UnOp); ok { // detect (blocking) channel receive operation
				if rcvIns, ok1 := recvIns.X.(*ssa.Alloc); ok1 {
					if _, ok2 := Analysis.selectedChans[rcvIns.Comment]; ok2 {
						chanRecvs[rcvIns.Comment] = prevN
						caseEnd = append(caseEnd, Analysis.selectedChans[rcvIns.Comment])
					}
				}
			} else if sendIns, ok := anIns.(*ssa.Send); ok { // detect matching channel send operations
				if sndIns, ok0 := sendIns.Chan.(*ssa.UnOp); ok0 {
					if _, ok1 := Analysis.selectedChans[sndIns.X.Name()]; ok1 {
						if edgeTo, ok2 := chanRecvs[sndIns.X.Name()]; ok2 {
							err := Analysis.HBgraph.MakeEdge(prevN, edgeTo) // create edge from Send node to Receive node
							if err != nil {
								log.Fatal(err)
							}
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
	var readyChans []string       // may be better to use maps instead
	var skipBBInd []int           // these basic blocks and their successor blocks shall be omitted
	var selectIns *ssa.Select
	var beforeSel ssa.Instruction // last instruction before encountering select
	var toAppend []*ssa.BasicBlock
	var toDefer []ssa.Instruction // stack storing deferred calls
	var toUnlock []ssa.Value
	var toRUnlock []ssa.Value
	repeatSwitch := false         // triggered when encountering basic blocks for body of a forloop
	for i := 0; i < len(fnBlocks); i++ {
		var aBlock *ssa.BasicBlock
		if i < len(fnBlocks) {
			aBlock = fnBlocks[i]
		} else {
			aBlock = toAppend[i-len(fnBlocks)]
		}
		if aBlock.Comment == "recover" {
			continue
		}
		if selectIns != nil && aBlock.Comment == "select.done" && !selectIns.Blocking { // non-blocking select
			k := len(aBlock.Preds)-1 // get index of basic block corresponding to default clause
			for aBlock.Preds[k].Comment != "select.next" && k > 0 {
				k--
			}
			a.selectDefault[selectIns] = aBlock.Preds[k].Instrs[0] // map select ins. to first ins. in its default clause
			a.selectHB[beforeSel] = a.selectDefault[selectIns] // map last ins before select to first ins in default
			a.afterSelect[aBlock.Instrs[0]] = beforeSel // map select to first ins after select
		}else{
			if aBlock.Comment == "select.done"{ // blocking select
				a.selectafterHB[beforeSel] = aBlock.Instrs[0]
				a.afterSelect[aBlock.Instrs[0]] = beforeSel // map select to first ins after select
			}
		}
		if aBlock.Comment == "select.body" && len(caseStatus) > 0 {
			skipBBInd = append(skipBBInd, aBlock.Index) // skipBBInd correlates with caseStatus
			if len(skipBBInd) <= len(caseStatus) && caseStatus[len(skipBBInd)-1] == 0 { // channel has no value (skip)
				continue
			} else { // active channel
				if len(readyChans) > 0 {
					a.selectedChans[readyChans[0]] = aBlock.Instrs[len(aBlock.Instrs)-1] // map to last instruction
					readyChans = readyChans[1:]
				}
			}
			if len(skipBBInd) > len(caseStatus) && sliceContainsInt(skipBBInd, aBlock.Preds[0].Index) {
				skipBBInd = append(skipBBInd, aBlock.Index)
				continue
			}
		}
		if aBlock.Comment == "select.next" && sliceContainsInt(skipBBInd, aBlock.Preds[0].Index) {
			skipBBInd = append(skipBBInd, aBlock.Index)
			continue
		}
		if strings.HasSuffix(aBlock.Comment, ".done") && i < len(fnBlocks)-1 && sliceContainsBloc(toAppend, aBlock) { // ignore return block if it doesn't have largest index
			toAppend = append(toAppend, aBlock)
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
				if k > 0 { // check if previous instruction is *ssa.Alloc
					if assign, ok := aBlock.Instrs[k-1].(*ssa.Alloc); ok && assign == examIns.Addr {
						// assignment statement won't race
					} else if _, ok := aBlock.Instrs[k-1].(*ssa.Extract); ok && k > 1 { // tuple assignment
						if assign, ok := aBlock.Instrs[k-2].(*ssa.Alloc); ok && assign == examIns.Addr {
							// assignment statement won't race
						}
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
				if examIns.Blocking { // only analyze channels with available values
					caseStatus, readyChans = a.insSelect(examIns, goID, theIns)
					skipIns := 1
					for _, val := range caseStatus { // skip channel receive instructions to obtain last instruction encountered prior to encountering select
						if val == 1 {
							skipIns++
						}
					}
					if k-skipIns >= 0 {
						beforeSel = aBlock.Instrs[k - skipIns]
					}
				} else { // select contains default case, which becomes race-prone (non-blocking channel ops)
					selectIns = examIns
					caseStatus, _ = a.insSelect(examIns, goID, theIns)
					skipIns := 1
					for _, val := range caseStatus { // skip channel receive instructions to obtain last instruction encountered prior to encountering select
						if val == 1 {
							skipIns++
						}
					}
					if k-skipIns >= 0 {
						beforeSel = aBlock.Instrs[k - skipIns]
					}
				}
			case *ssa.Jump:
				a.RWIns[goID] = append(a.RWIns[goID], theIns)
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
