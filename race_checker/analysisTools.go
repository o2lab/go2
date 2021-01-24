package main

import (
	log "github.com/sirupsen/logrus"
	"github.com/twmb/algoimpl/go/graph"
	"strings"
	"github.tamu.edu/April1989/go_tools/go/ssa"
	"fmt"
)

func (a* analysis) buildHB(HBgraph *graph.Graph) {
	var prevN graph.Node
	var goCaller []graph.Node
	var selectN []graph.Node
	var readyCh []string
	var selCaseEndN []graph.Node
	var ifN []graph.Node
	var ifSuccEndN []graph.Node
	waitingN := make(map[*ssa.Call]graph.Node)
	chanRecvs := make(map[string]graph.Node) // map channel name to graph node
	chanSends := make(map[string]graph.Node) // map channel name to graph node
	for nGo, insSlice := range a.RWIns {
		for i, anIns := range insSlice {
			disjoin := false // detach select case statement from subsequent instruction
			insKey := goIns{ins: anIns, goID: nGo}
			if nGo == 0 && i == 0 { // main goroutine, first instruction
				prevN = HBgraph.MakeNode() // initiate for future nodes
				*prevN.Value = insKey
				if _, ok := anIns.(*ssa.Go); ok {
					goCaller = append(goCaller, prevN) // sequentially store go calls in the same goroutine
				}
			} else {
				currN := HBgraph.MakeNode()
				*currN.Value = insKey
				if nGo != 0 && i == 0 { // worker goroutine, first instruction
					prevN = goCaller[0] // first node in subroutine
					goCaller = goCaller[1:]
				} else if _, ok := anIns.(*ssa.Go); ok {
					goCaller = append(goCaller, currN) // sequentially store go calls in the same goroutine
				} else if selIns, ok1 := anIns.(*ssa.Select); ok1 {
					selectN = append(selectN, currN) // select node
					readyCh = a.selReady[selIns]
					selCaseEndN = []graph.Node{} // reset slice of nodes when encountering multiple select statements
					readys := 0
					for ith, ch := range readyCh {
						if ch != "" && selIns.States[ith].Dir == 1 {
							readys++
							if _, ok0 := a.selUnknown[selIns]; ok0 && readys == 1 {
								chanSends[ch] = currN
							}
						}
					}
				} else if ins, chR := anIns.(*ssa.UnOp); chR {
					if ch := a.getRcvChan(ins); ch != "" { // a channel receive Op
						chanRecvs[a.getRcvChan(ins)] = currN
						if a.isReadySel(ch) { // channel waited on by select
							disjoin = true // no edge between current node and node of succeeding instruction
						}
					}
				} else if insS, chS := anIns.(*ssa.Send); chS {
					chanSends[a.getSndChan(insS)] = currN
				} else if _, isIf := anIns.(*ssa.If); isIf {
					ifN = append([]graph.Node{currN}, ifN...) // store if statements
				}
				if ch, ok0 := a.selectCaseEnd[anIns]; ok0 && sliceContainsStr(readyCh, ch) {
					selCaseEndN = append(selCaseEndN, currN)
				}
				if _, isSuccEnd := a.ifSuccEnd[anIns]; isSuccEnd {
					ifSuccEndN = append(ifSuccEndN, currN)
				}
				// edge manipulation:
				if ch, ok := a.selectCaseBegin[anIns]; ok {
					if ch == "defaultCase" || ch == "timeOut" {
						err := HBgraph.MakeEdge(selectN[0], currN) // select node to default case
						if err != nil {
							log.Fatal(err)
						}
					} else {
						if _, ok1 := chanRecvs[ch]; ok1 {
							err := HBgraph.MakeEdge(chanRecvs[ch], currN) // receive Op to ready case
							if err != nil {
								log.Fatal(err)
							}
						} else if sliceContainsStr(readyCh, ch) {
							err := HBgraph.MakeEdge(selectN[0], currN) // select node to assumed ready cases
							if err != nil {
								log.Fatal(err)
							}
						}
					}
				} else if _, ok1 := a.selectDone[anIns]; ok1 {
					if len(selCaseEndN) > 1 { // more than one portal is ready
						err := HBgraph.MakeEdge(selectN[0], currN) // select statement to select done
						if err != nil {
							log.Fatal(err)
						}
					} else if len(selCaseEndN) > 0 {
						err := HBgraph.MakeEdge(selCaseEndN[0], currN) // ready case to select done
						if err != nil {
							log.Fatal(err)
						}
					}
					if selectN != nil && len(selectN) > 1 {
						selectN = selectN[1:]
					} // completed analysis of one select statement
				} else if ifInstr, ok2 := a.ifSuccBegin[anIns]; ok2 {
					skipSucc := false
					for beginIns, ifIns := range a.ifSuccBegin {
						if ifIns == ifInstr && beginIns != anIns && sliceContainsInsAt(a.commIfSucc, beginIns) != -1 && channelComm { // other succ contains channel communication
							if (anIns.Block().Comment == "if.then" && beginIns.Block().Comment == "if.else") || (anIns.Block().Comment == "if.else" && beginIns.Block().Comment == "if.then") {
								skipSucc = true
								a.omitComm = append(a.omitComm, anIns.Block())
							}
						}
					}
					if !skipSucc {
						err := HBgraph.MakeEdge(ifN[0], currN)
						if err != nil {
							log.Fatal(err)
						}
					}
				} else {
					err := HBgraph.MakeEdge(prevN, currN)
					if err != nil {
						log.Fatal(err)
					}
				}
				if !disjoin {
					prevN = currN
				}
			}
			// Create additional edges:
			if a.isReadIns(anIns) || isWriteIns(anIns) {
				a.RWinsMap[insKey] = prevN
			} else if callIns, ok := anIns.(*ssa.Call); ok { // taking care of WG operations. TODO: identify different WG instances
				if callIns.Call.Value.Name() == "Wait" {
					waitingN[callIns] = prevN // store Wait node for later edge creation TO this node
				} else if callIns.Call.Value.Name() == "Done" {
					for wIns, wNode := range waitingN {
						if a.sameAddress(callIns.Call.Args[0], wIns.Call.Args[0]) {
							err := HBgraph.MakeEdge(prevN, wNode) // create edge from Done node to Wait node
							if err != nil {
								log.Fatal(err)
							}
						}
					}
				}
			} else if dIns, ok1 := anIns.(*ssa.Defer); ok1 {
				if dIns.Call.Value.Name() == "Done" {
					for wIns, wNode := range waitingN {
						if a.sameAddress(dIns.Call.Args[0], wIns.Call.Args[0]) {
							err := HBgraph.MakeEdge(prevN, wNode) // create edge from Done node to Wait node
							if err != nil {
								log.Fatal(err)
							}
						}
					}
				}
			}
			if sendIns, ok := anIns.(*ssa.Send); ok && channelComm { // detect matching channel send operations
				for ch, sIns := range a.chanSnds {
					if rcvN, matching := chanRecvs[ch]; matching && sliceContainsSnd(sIns, sendIns) {
						err := HBgraph.MakeEdge(prevN, rcvN) // create edge from Send node to Receive node
						if err != nil {
							log.Fatal(err)
						}
						err1 := HBgraph.MakeEdge(rcvN, prevN) // create edge from Send node to Receive node
						if err1 != nil {
							log.Fatal(err1)
						}
					}
				}
			} else if rcvIns, chR := anIns.(*ssa.UnOp); chR && channelComm {
				if ch := a.getRcvChan(rcvIns); ch != "" {
					if sndN, matching := chanSends[ch]; matching {
						err := HBgraph.MakeEdge(sndN, prevN) // create edge from Send node to Receive node
						if err != nil {
							log.Fatal(err)
						}
						err1 := HBgraph.MakeEdge(prevN, sndN) // create edge from Send node to Receive node
						if err1 != nil {
							log.Fatal(err1)
						}
					}
				}
			}
			if reIns, isReturn := anIns.(*ssa.Return); isReturn {
				if a.ifFnReturn[reIns.Parent()] == reIns { // this is final return
					for r, ifEndN := range ifSuccEndN {
						if r != len(ifSuccEndN)-1 {
							err := HBgraph.MakeEdge(ifEndN, prevN)
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
}

// visitAllInstructions visits each line and calls the corresponding helper function to drive the tool
func (a *analysis) visitAllInstructions(fn *ssa.Function, goID int) {
	if a.useNewPTA && a.ptaConfig.DEBUG { //bz: useNewPTA ...
		fmt.Println(".... " + fn.String())
	}
	a.analysisStat.nGoroutine = goID + 1 // keep count of goroutine quantity
	if fn == nil {
		return
	}
	if !isSynthetic(fn) {                // if function is NOT synthetic
		if !fromPkgsOfInterest(fn) {
			a.updateRecords(fn.Name(), goID, "POP  ")
			return
		}
		if fn.Name() == entryFn {
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
				} else if j < len(bVisit)-1 {
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
	//if fn.Name() == "main" {
	//	fmt.Println(bVisit)
	//	for _, ind := range bVisit {
	//		fmt.Println(fnBlocks[ind].Comment)
	//	}
	//}

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
						if !a.useNewPTA {
							a.ptaConfig.AddQuery(lockLoc)
						}
						toUnlock = append(toUnlock, lockLoc)
					} else if deferIns.Call.StaticCallee().Name() == "RUnlock" {
						RlockLoc := deferIns.Call.Args[0]
						if !a.useNewPTA {
							a.ptaConfig.AddQuery(RlockLoc)
						}
						toRUnlock = append(toRUnlock, RlockLoc)
					} else if deferIns.Call.Value.Name() == "Done" {
						a.RWIns[goID] = append(a.RWIns[goID], dIns)
						if !a.useNewPTA {
							a.ptaConfig.AddQuery(deferIns.Call.Args[0])
						}
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
			if defCase {
				a.selectCaseBody[theIns] = selIns
			}
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
