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
	if fn.Pkg.Pkg.Name() == "main" || fn.Pkg.Pkg.Name() == "cli" {
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

// Run builds a Happens-Before Graph and calls other functions like visitAllInstructions to drive the program further
func (runner *AnalysisRunner) Run(args []string) error {
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
	runner.Analysis = &analysis{
		prog:            prog,
		pkgs:            pkgs,
		mains:           mains,
		ptaConfig:       config,
		RWinsMap:        make(map[goIns]graph.Node),
		insDRA:          0,
		levels:          make(map[int]int),
		lockMap:         make(map[ssa.Instruction][]ssa.Value),
		RlockMap:        make(map[ssa.Instruction][]ssa.Value),
		goLockset:       make(map[int][]ssa.Value),
		goRLockset:      make(map[int][]ssa.Value),
		mapFreeze:       false,
		goCaller:        make(map[int]int),
		goNames:         make(map[int]string),
		chanBuf:         make(map[string]int),
		chanRcvs:        make(map[string][]*ssa.UnOp),
		chanSnds:        make(map[string][]*ssa.Send),
		selectBloc:      make(map[int]*ssa.Select),
		selReady:        make(map[*ssa.Select][]string),
		selectCaseBegin: make(map[ssa.Instruction]string),
		selectCaseEnd:   make(map[ssa.Instruction]string),
		selectCaseBody:  make(map[ssa.Instruction]*ssa.Select),
		selectDone:      make(map[ssa.Instruction]*ssa.Select),
		ifSuccBegin:     make(map[ssa.Instruction]*ssa.If),
		ifFnReturn:      make(map[*ssa.Function]*ssa.Return),
		ifSuccEnd:       make(map[ssa.Instruction]*ssa.Return),
	}

	log.Info("Compiling stack trace for every Goroutine... ")
	log.Debug(strings.Repeat("-", 35), "Stack trace begins", strings.Repeat("-", 35))
	runner.Analysis.visitAllInstructions(mains[0].Func("main"), 0)
	log.Debug(strings.Repeat("-", 35), "Stack trace ends", strings.Repeat("-", 35))
	totalIns := 0
	for g, _ := range runner.Analysis.RWIns {
		totalIns += len(runner.Analysis.RWIns[g])
	}
	log.Info("Done  -- ", len(runner.Analysis.RWIns), " goroutines analyzed! ", totalIns, " instructions of interest detected! ")
	if len(runner.Analysis.RWIns) < 2 {
		log.Debug("race is not possible in one goroutine")
		return nil
	}

	result, err := pointer.Analyze(runner.Analysis.ptaConfig) // conduct pointer analysis
	if err != nil {
		log.Fatal(err)
	}
	runner.Analysis.result = result

	log.Info("Building Happens-Before graph... ")
	runner.Analysis.HBgraph = graph.New(graph.Directed)
	var prevN graph.Node
	var goCaller []graph.Node
	var selectN []graph.Node
	var readyCh []string
	var selCaseEndN []graph.Node
	var ifN []graph.Node
	var ifSuccEndN []graph.Node
	waitingN := make(map[*ssa.Call]graph.Node)
	chanRecvs := make(map[string]graph.Node) // map channel name to graph node
	for nGo, insSlice := range runner.Analysis.RWIns {
		for i, anIns := range insSlice {
			disjoin := false // detach select case statement from subsequent instruction
			insKey := goIns{ins: anIns, goID: nGo}
			if nGo == 0 && i == 0 { // main goroutine, first instruction
				prevN = runner.Analysis.HBgraph.MakeNode() // initiate for future nodes
				*prevN.Value = insKey
				if _, ok := anIns.(*ssa.Go); ok {
					goCaller = append(goCaller, prevN) // sequentially store go calls in the same goroutine
				}
			} else {
				currN := runner.Analysis.HBgraph.MakeNode()
				*currN.Value = insKey
				if nGo != 0 && i == 0 { // worker goroutine, first instruction
					prevN = goCaller[0] // first node in subroutine
					goCaller = goCaller[1:]
				} else if _, ok := anIns.(*ssa.Go); ok {
					goCaller = append(goCaller, currN) // sequentially store go calls in the same goroutine
				} else if selIns, ok1 := anIns.(*ssa.Select); ok1 {
					selectN = append(selectN, currN) // select node
					readyCh = runner.Analysis.insSelect(selIns, nGo, anIns)
					selCaseEndN = []graph.Node{} // reset slice of nodes when encountering multiple select statements
				} else if ins, chR := anIns.(*ssa.UnOp); chR {
					if ch := runner.Analysis.getRcvChan(ins); ch != "" { // a channel receive Op
						chanRecvs[runner.Analysis.getRcvChan(ins)] = currN
						if runner.Analysis.isReadySel(ch) {
							disjoin = true
						}
					}
				} else if _, isIf := anIns.(*ssa.If); isIf {
					ifN = append([]graph.Node{currN}, ifN...) // store if statements
				}
				if ch, ok0 := runner.Analysis.selectCaseEnd[anIns]; ok0 && sliceContainsStr(readyCh, ch) {
					selCaseEndN = append(selCaseEndN, currN)
				}
				if _, isSuccEnd := runner.Analysis.ifSuccEnd[anIns]; isSuccEnd {
					ifSuccEndN = append(ifSuccEndN, currN)
				}
				// edge manipulation:
				if ch, ok := runner.Analysis.selectCaseBegin[anIns]; ok {
					if ch == "defaultCase" {
						err := runner.Analysis.HBgraph.MakeEdge(selectN[0], currN) // select node to default case
						if err != nil {
							log.Fatal(err)
						}
					} else {
						err := runner.Analysis.HBgraph.MakeEdge(chanRecvs[ch], currN) // receive Op to ready case
						if err != nil {
							log.Fatal(err)
						}
					}
				} else if _, ok1 := runner.Analysis.selectDone[anIns]; ok1 {
					if len(selCaseEndN) > 1 { // more than one portal
						err := runner.Analysis.HBgraph.MakeEdge(selectN[0], currN) // ready case to select done
						if err != nil {
							log.Fatal(err)
						}
					} else if len(selCaseEndN) > 0 {
						err := runner.Analysis.HBgraph.MakeEdge(selCaseEndN[0], currN) // ready case to select done
						if err != nil {
							log.Fatal(err)
						}
					}
					if selectN != nil && len(selectN) > 1 {
						selectN = selectN[1:]
					} // completed analysis of one select statement
				} else if ifInstr, ok2 := runner.Analysis.ifSuccBegin[anIns]; ok2 {
					skipSucc := false
					for beginIns, ifIns := range runner.Analysis.ifSuccBegin {
						if ifIns == ifInstr && beginIns != anIns && sliceContainsInsAt(runner.Analysis.commIfSucc, beginIns) != -1 && channelComm { // other succ contains channel communication
							if (anIns.Block().Comment == "if.then" && beginIns.Block().Comment == "if.else") || (anIns.Block().Comment == "if.else" && beginIns.Block().Comment == "if.then") {
								skipSucc = true
								runner.Analysis.omitComm = append(runner.Analysis.omitComm, anIns.Block())
							}
						}
					}
					if !skipSucc {
						err := runner.Analysis.HBgraph.MakeEdge(ifN[0], currN)
						if err != nil {
							log.Fatal(err)
						}
					}
				} else {
					err := runner.Analysis.HBgraph.MakeEdge(prevN, currN)
					if err != nil {
						log.Fatal(err)
					}
				}
				if !disjoin {
					prevN = currN
				}
			}
			// Create additional edges:
			if runner.Analysis.isReadIns(anIns) || isWriteIns(anIns) {
				runner.Analysis.RWinsMap[insKey] = prevN
			} else if callIns, ok := anIns.(*ssa.Call); ok { // taking care of WG operations. TODO: identify different WG instances
				if callIns.Call.Value.Name() == "Wait" {
					waitingN[callIns] = prevN // store Wait node for later edge creation TO this node
				} else if callIns.Call.Value.Name() == "Done" {
					for wIns, wNode := range waitingN {
						if runner.Analysis.sameAddress(callIns.Call.Args[0], wIns.Call.Args[0]) {
							err := runner.Analysis.HBgraph.MakeEdge(prevN, wNode) // create edge from Done node to Wait node
							if err != nil {
								log.Fatal(err)
							}
						}
					}
				}
			} else if dIns, ok1 := anIns.(*ssa.Defer); ok1 {
				if dIns.Call.Value.Name() == "Done" {
					for wIns, wNode := range waitingN {
						if runner.Analysis.sameAddress(dIns.Call.Args[0], wIns.Call.Args[0]) {
							err := runner.Analysis.HBgraph.MakeEdge(prevN, wNode) // create edge from Done node to Wait node
							if err != nil {
								log.Fatal(err)
							}
						}
					}
				}
			}
			if sendIns, ok := anIns.(*ssa.Send); ok && channelComm { // detect matching channel send operations
				for ch, sIns := range runner.Analysis.chanSnds {
					if rcvN, matching := chanRecvs[ch]; matching && sliceContainsSnd(sIns, sendIns) {
						err := runner.Analysis.HBgraph.MakeEdge(prevN, rcvN) // create edge from Send node to Receive node
						if err != nil {
							log.Fatal(err)
						}
					}
				}
			}
			if reIns, isReturn := anIns.(*ssa.Return); isReturn {
				if runner.Analysis.ifFnReturn[reIns.Parent()] == reIns { // this is final return
					for r, ifEndN := range ifSuccEndN {
						if r != len(ifSuccEndN)-1 {
							err := runner.Analysis.HBgraph.MakeEdge(ifEndN, prevN)
							if err != nil {
								log.Fatal(err)
							}
						}
					}
					ifSuccEndN = []graph.Node{} // reset slice containing last ins of each succ block preceeding final return
				}
			}
		}
	}
	log.Info("Done  -- Happens-Before graph built ")

	log.Info("Checking for data races... ")
	runner.Analysis.checkRacyPairs()
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
	bVisit := make([]int, 1, len(fnBlocks)) // create ordering at which blocks are visited
	k := 0
	b := fnBlocks[0]
	bVisit[k] = 0
	for k < len(bVisit) {
		b = fnBlocks[bVisit[k]]
		if len(b.Succs) == 0 {
			k++
			continue
		}
		j := k
		for s, bNext := range b.Succs {
			j += s
			i := sliceContainsIntAt(bVisit, bNext.Index)
			if i < k {
				if j == len(bVisit)-1 {
					bVisit = append(bVisit, bNext.Index)
				} else {
					bVisit = append(bVisit[:j+2], bVisit[j+1:]...)
					bVisit[j+1] = bNext.Index
				}
				if i != -1 { // visited block
					bVisit = append(bVisit[:i], bVisit[i+1:]...)
					j--
				}
			}
		}
		k++
	}
	var toDefer []ssa.Instruction // stack storing deferred calls
	var toUnlock []ssa.Value
	var toRUnlock []ssa.Value
	repeatSwitch := false // triggered when encountering basic blocks for body of a forloop
	var readyChans []string
	var selIns *ssa.Select // current select statement
	var selCount int       // total cases in a select statement
	var activeCase bool
	var defCase bool
	var selDone bool
	var ifIns *ssa.If
	var ifEnds []ssa.Instruction
	for bInd := 0; bInd < len(bVisit); bInd++ {
		activeCase, selDone = false, false
		aBlock := fnBlocks[bVisit[bInd]]
		if aBlock.Comment == "recover" {
			continue
		}
		if aBlock.Comment == "select.done" {
			a.selectDone[aBlock.Instrs[0]] = selIns // map first ins in select.done to select instruction
			selDone = true
		}
		if aBlock.Comment == "select.body" && selCount < len(readyChans) {
			if readyChans[selCount] == "" {
				selCount++
				continue // skip unready case
			} else {
				activeCase = true
			}
		}
		if selIns != nil && aBlock.Comment == "select.next" && !selIns.Blocking && readyChans[selCount] == "defaultCase" {
			a.selectCaseBegin[aBlock.Instrs[0]] = readyChans[selCount]                  // map first instruction in case to channel name
			a.selectCaseEnd[aBlock.Instrs[len(aBlock.Instrs)-1]] = readyChans[selCount] // map last instruction in case to channel name
			defCase = true
		}
		if ifIns != nil && (aBlock.Comment == "if.then" || aBlock.Comment == "if.else" || aBlock.Comment == "if.done") {
			a.ifSuccBegin[aBlock.Instrs[0]] = ifIns
			if len(aBlock.Succs) == 0 {
				ifEnds = append(ifEnds, aBlock.Instrs[len(aBlock.Instrs)-1])
			}
		}
		for ii, theIns := range aBlock.Instrs { // examine each instruction
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
				a.insMakeChan(examIns, ii)
			case *ssa.Send: // channel send op
				chNm := a.insSend(examIns, goID, theIns)
				isAwait := false // is the channel send being awaited on by select?
				for _, chs := range a.selReady {
					if sliceContainsStr(chs, chNm) {
						isAwait = true
						break
					}
				}
				if isAwait && (examIns.Block().Comment == "if.then" || examIns.Block().Comment == "if.else" || examIns.Block().Comment == "if.done") {
					// send awaited on by select, other if successor will not be traversed
					a.commIfSucc = append(a.commIfSucc, examIns.Block().Instrs[0])
				}
			case *ssa.Store: // write op
				if _, ok := examIns.Addr.(*ssa.Alloc); ok && ii > 0 { // variable initialization
					switch aBlock.Instrs[ii-1].(type) {
					case *ssa.Alloc:
					case *ssa.MakeChan: // channel object
					case *ssa.Extract: // tuple index
						if ii < 2 {
							a.insStore(examIns, goID, theIns)
						} else {
							if _, ok1 := aBlock.Instrs[ii-2].(*ssa.Alloc); !ok1 {
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
				if examIns.Block().Comment == "if.then" || examIns.Block().Comment == "if.else" || examIns.Block().Comment == "if.done" {
					a.ifFnReturn[fn] = examIns // will be revised iteratively to eventually contain final return instruction
				}
			case *ssa.MapUpdate:
				a.insMapUpdate(examIns, goID, theIns)
			case *ssa.Select:
				readyChans = a.insSelect(examIns, goID, theIns)
				selCount = 0
				selIns = examIns
				a.selectBloc[bVisit[bInd]] = examIns
			case *ssa.If:
				a.RWIns[goID] = append(a.RWIns[goID], theIns)
				ifIns = examIns
			default:
				a.RWIns[goID] = append(a.RWIns[goID], theIns) // TODO: consolidate
			}
			if ii == len(aBlock.Instrs)-1 && a.mapFreeze { // TODO: this can happen too early
				a.mapFreeze = false
			}
			if activeCase && readyChans[selCount] != "defaultCase" && readyChans[selCount] != "timeOut" {
				if ii == 0 {
					a.selectCaseBegin[theIns] = readyChans[selCount] // map first instruction in case to channel name
					if a.RWIns[goID][len(a.RWIns[goID])-1] != theIns {
						a.RWIns[goID] = append(a.RWIns[goID], theIns)
					}
					a.selectCaseBody[theIns] = selIns
				} else if ii == len(aBlock.Instrs)-1 {
					a.selectCaseEnd[theIns] = readyChans[selCount] // map last instruction in case to channel name
					if a.RWIns[goID][len(a.RWIns[goID])-1] != theIns {
						a.RWIns[goID] = append(a.RWIns[goID], theIns)
					}
					a.selectCaseBody[theIns] = selIns
				} else {
					a.selectCaseBody[theIns] = selIns
				}
			}
			if defCase {a.selectCaseBody[theIns] = selIns}
			if selDone && ii == 0 {
				if sliceContainsInsAt(a.RWIns[goID], theIns) == -1 {
					a.RWIns[goID] = append(a.RWIns[goID], theIns)
				}
			}
		}
		if aBlock.Comment == "for.body" || aBlock.Comment == "rangeindex.body" { // repeat unrolling of forloop
			if repeatSwitch == false {
				repeatSwitch = true // repeat analysis of current block
				bInd--
			} else { // repetition conducted
				repeatSwitch = false
			}
		}
		if activeCase && readyChans[selCount] != "defaultCase" && readyChans[selCount] != "timeOut" {
			selCount++
		} // increment case count
		if bInd == len(bVisit)-1 && len(ifEnds) > 0 {
			for _, e := range ifEnds {
				a.ifSuccEnd[e] = a.ifFnReturn[fn]
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
	visitedIns := []ssa.Instruction{}
	if len(a.RWIns) > 0 {
		visitedIns = a.RWIns[goID]
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
