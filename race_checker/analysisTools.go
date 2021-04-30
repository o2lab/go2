package main

import (
	"github.com/april1989/origin-go-tools/go/ssa"
	log "github.com/sirupsen/logrus"
	"github.com/twmb/algoimpl/go/graph"
	"strings"
)

func (a *analysis) buildHB() {
	var prevN graph.Node
	var selectN []graph.Node
	var readyCh []string
	var selCaseEndN []graph.Node
	var ifN []graph.Node
	var ifSuccEndN []graph.Node
	goCaller := make(map[*ssa.Go]graph.Node)
	waitingN := make(map[goIns]graph.Node)
	chanRecvs := make(map[string]graph.Node) // map channel name to graph node
	chanSends := make(map[string]graph.Node) // map channel name to graph node
	for nGo, insSlice := range a.RWIns {
		for i, anIns := range insSlice {
			disjoin := false // detach select case statement from subsequent instruction
			insKey := goIns{ins: anIns, goID: nGo}
			if nGo == 0 && i == 0 { // main goroutine, first instruction
				prevN = a.HBgraph.MakeNode() // initiate for future nodes
				*prevN.Value = insKey
				if goInstr, ok := anIns.(*ssa.Go); ok {
					goCaller[goInstr] = prevN // sequentially store go calls in the same goroutine
				}
			} else {
				currN := a.HBgraph.MakeNode()
				*currN.Value = insKey
				if nGo != 0 && i == 0 { // worker goroutine, first instruction
					prevN = goCaller[anIns.(*ssa.Go)] // first node in subroutine
				} else if goInstr, ok := anIns.(*ssa.Go); ok { // spawning of subroutine
					//if _, ok1 := goCaller[goInstr]; ok1 { // repeated spawning in loop
					//	continue
					//}
					goCaller[goInstr] = currN // store go calls in the same goroutine
				} else if selIns, ok1 := anIns.(*ssa.Select); ok1 {
					selectN = append(selectN, currN) // select node
					readyCh = a.selReady[selIns]
					selCaseEndN = []graph.Node{} // reset slice of nodes when encountering multiple select statements
					readys := 0
					for ith, ch := range readyCh {
						if ith < len(selIns.States) && ch != "" && selIns.States[ith].Dir == 1 { // TODO: readyCh may be longer than selIns.States?
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
				if ch, ok := a.selectCaseBegin[anIns]; ok && channelComm && selectN != nil {
					if ch == "defaultCase" || ch == "timeOut" {
						err := a.HBgraph.MakeEdge(selectN[0], currN) // select node to default case
						if err != nil {
							log.Fatal(err)
						}
					} else {
						if _, ok1 := chanRecvs[ch]; ok1 {
							err := a.HBgraph.MakeEdge(chanRecvs[ch], currN) // receive Op to ready case
							if err != nil {
								log.Fatal(err)
							}
						} else if sliceContainsStr(readyCh, ch) {
							err := a.HBgraph.MakeEdge(selectN[0], currN) // select node to assumed ready cases
							if err != nil {
								log.Fatal(err)
							}
						}
					}
				} else if _, ok1 := a.selectDone[anIns]; ok1 && channelComm && selectN != nil {
					if len(selCaseEndN) > 1 { // more than one portal is ready
						err := a.HBgraph.MakeEdge(selectN[0], currN) // select statement to select done
						if err != nil {
							log.Fatal(err)
						}
					} else if len(selCaseEndN) > 0 {
						err := a.HBgraph.MakeEdge(selCaseEndN[0], currN) // ready case to select done
						if err != nil {
							log.Fatal(err)
						}
					}
					if len(selectN) > 1 {
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
					if !skipSucc && ifN != nil {
						err := a.HBgraph.MakeEdge(ifN[0], currN)
						if err != nil {
							log.Fatal(err)
						}
					}
				} else {
					err := a.HBgraph.MakeEdge(prevN, currN)
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
					waitingN[insKey] = prevN // store Wait node for later edge creation TO this node
				} else if callIns.Call.Value.Name() == "Done" {
					for wKey, wNode := range waitingN {
						if a.sameAddress(callIns.Call.Args[0], wKey.ins.(*ssa.Call).Call.Args[0], nGo, wKey.goID) &&
							(*(prevN.Value)).(goIns).goID != (*(wNode.Value)).(goIns).goID {
							err := a.HBgraph.MakeEdge(prevN, wNode) // create edge from Done node to Wait node
							var fromName string
							var toName string
							if nGo == 0 {
								fromName = entryFn
							} else {
								fromName = a.goNames(a.RWIns[nGo][0].(*ssa.Go))
							}
							if (*wNode.Value).(goIns).goID == 0 {
								toName = entryFn
							} else {
								toName = a.goNames(a.RWIns[(*wNode.Value).(goIns).goID][0].(*ssa.Go))
							}
							if debugFlag {
								log.Debug("WaitGroup edge from Goroutine ", fromName, " [", nGo, "] to Goroutine ", toName, " [", (*wNode.Value).(goIns).goID, "]")
							}
							if err != nil {
								log.Fatal(err)
							}
						}
					}
				}
			} else if dIns, ok1 := anIns.(*ssa.Defer); ok1 {
				if dIns.Call.Value.Name() == "Done" {
					for wKey, wNode := range waitingN {
						if a.sameAddress(dIns.Call.Args[0], wKey.ins.(*ssa.Call).Call.Args[0], nGo, wKey.goID) &&
							(*(prevN.Value)).(goIns).goID != (*(wNode.Value)).(goIns).goID {
							err := a.HBgraph.MakeEdge(prevN, wNode) // create edge from Done node to Wait node
							var fromName string
							var toName string
							if nGo == 0 {
								fromName = entryFn
							} else {
								fromName = a.goNames(a.RWIns[nGo][0].(*ssa.Go))
							}
							if (*wNode.Value).(goIns).goID == 0 {
								toName = entryFn
							} else {
								toName = a.goNames(a.RWIns[(*wNode.Value).(goIns).goID][0].(*ssa.Go))
							}
							if debugFlag {
								log.Debug("WaitGroup edge from Goroutine ", fromName, " [", nGo, "] to Goroutine ", toName, " [", (*wNode.Value).(goIns).goID, "]")
							}
							if err != nil {
								log.Fatal(err)
							}
						}
					}
				}
			}
			if sendIns, ok := anIns.(*ssa.Send); ok && channelComm { // detect matching channel send operations
				for ch, sIns := range a.chanSnds {
					if rcvN, matching := chanRecvs[ch]; matching && sliceContainsSnd(sIns, sendIns) &&
						(*(prevN.Value)).(goIns).goID != (*(rcvN.Value)).(goIns).goID {
						err := a.HBgraph.MakeEdge(prevN, rcvN) // create edge from Send node to Receive node
						var fromName, toName string
						if nGo == 0 {
							fromName = entryFn
						} else {
							fromName = a.goNames(a.RWIns[nGo][0].(*ssa.Go))
						}
						if (*rcvN.Value).(goIns).goID == 0 {
							toName = entryFn
						} else {
							toName = a.goNames(a.RWIns[(*rcvN.Value).(goIns).goID][0].(*ssa.Go))
						}
						if debugFlag {
							log.Debug("Channel comm edge from Goroutine ", fromName, " [", nGo, "] to Goroutine ", toName, " [", (*rcvN.Value).(goIns).goID, "]")
						}
						if err != nil {
							log.Fatal(err)
						}
						err1 := a.HBgraph.MakeEdge(rcvN, prevN) // create edge from Receive node to Send node
						if err1 != nil {
							log.Fatal(err1)
						}
					}
				}
			} else if rcvIns, chR := anIns.(*ssa.UnOp); chR && channelComm {
				if ch := a.getRcvChan(rcvIns); ch != "" {
					if sndN, matching := chanSends[ch]; matching &&
						(*(sndN.Value)).(goIns).goID != (*(prevN.Value)).(goIns).goID {
						err := a.HBgraph.MakeEdge(sndN, prevN) // create edge from Send node to Receive node
						var fromName, toName string
						if (*sndN.Value).(goIns).goID == 0 {
							fromName = entryFn
						} else {
							fromName = a.goNames(a.RWIns[(*sndN.Value).(goIns).goID][0].(*ssa.Go))
						}
						if nGo == 0 {
							toName = entryFn
						} else {
							toName = a.goNames(a.RWIns[nGo][0].(*ssa.Go))
						}
						if debugFlag {
							log.Debug("Channel comm edge from Goroutine ", fromName, " [", (*sndN.Value).(goIns).goID, "] to Goroutine ", toName, " [", nGo, "]")
						}
						if err != nil {
							log.Fatal(err)
						}
						err1 := a.HBgraph.MakeEdge(prevN, sndN) // create edge from Receive node to Send node
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
							err := a.HBgraph.MakeEdge(ifEndN, prevN)
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
	a.analysisStat.nGoroutine = goID + 1 // keep count of goroutine quantity
	if fn == nil {
		return
	}
	if !isSynthetic(fn) { // if function is NOT synthetic
		if !a.fromPkgsOfInterest(fn) {
			a.updateRecords(fn.Name(), goID, "POP  ", fn, nil)
			if len(a.storeFns) == 0 && len(a.workList) != 0 { // finished reporting current goroutine and workList isn't empty
				nextGoInfo := a.workList[0] // get the goroutine info at head of workList
				a.workList = a.workList[1:] // pop goroutine info from head of workList
				a.newGoroutine(nextGoInfo)
			}
			return
		}
		if fn.Name() == entryFn {
			if goID == 0 && len(a.storeFns) == 0 {
				a.levels[goID] = 0 // initialize level count at main entry
				a.loopIDs[goID] = 0
				a.updateRecords(fn.Name(), goID, "PUSH ", fn, nil)
				a.goStack = append(a.goStack, []fnCallInfo{}) // initialize first interior slice for main goroutine
			} else { //revisiting entry-point
				return
			}

		}
	}
	if _, ok := a.levels[goID]; !ok && goID > 0 { // initialize level counter for new goroutine
		a.levels[goID] = 1
	}
	if goID >= len(a.RWIns) { // initialize interior slice for new goroutine
		a.RWIns = append(a.RWIns, []ssa.Instruction{})
	}
	//fmt.Println(" ... ", fn.String(), " goID:", goID) //bz: debug, please comment off
	bVisit0 := fn.DomPreorder()
	var bVisit []*ssa.BasicBlock
	var pushBack []*ssa.BasicBlock // stack of .done blocks
	statement := "" // could be if, for or rangeiter
	for i, b := range bVisit0 {
		if len(pushBack) > 0 && !strings.Contains(b.Comment, statement) { // reach end of statement blocks
			bVisit = append(bVisit, pushBack...) // LIFO
			pushBack = []*ssa.BasicBlock{} // empty stack
			statement = "" // reinitialize
		}
		if strings.Contains(b.Comment, ".done") && i < len(bVisit0)-1 { // not the last block
			statement = strings.Split(b.Comment, ".done")[0]
			pushBack = append([]*ssa.BasicBlock{b}, pushBack...)
		} else {
			bVisit = append(bVisit, b)
		}
		if len(pushBack) > 0 && i == len(bVisit0)-1 { // reach end of statement blocks
			bVisit = append(bVisit, pushBack...) // LIFO
			pushBack = []*ssa.BasicBlock{} // empty stack
		}
	}

	var toDefer []ssa.Instruction // stack storing deferred calls
	var toUnlock []ssa.Value
	var toRUnlock []ssa.Value
	var readyChans []string
	var selIns *ssa.Select // current select statement
	var selCount int       // total cases in a select statement
	var activeCase bool
	var defCase bool
	var selDone bool
	var ifIns *ssa.If
	var ifEnds []ssa.Instruction
	for bInd, aBlock := range bVisit {
		activeCase, selDone = false, false
		if aBlock.Comment == "recover" {// ----> !!! SEE HERE: bz: the same as above, from line 279 to 293 (or even 304) can be separated out
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
		if aBlock.Comment == "for.body" || aBlock.Comment == "rangeindex.body" || aBlock.Comment == "rangeiter.body" {
			a.inLoop = true // TODO: consider nested loops
		}
		for ii, theIns := range aBlock.Instrs { // examine each instruction
			if theIns.String() == "rundefers" { // execute deferred calls at this index
				a.RWIns[goID] = append(a.RWIns[goID], theIns)
				for _, dIns := range toDefer {  // ----> !!! SEE HERE: bz: the same as above, from line 307 to 347 can be separated out
					deferIns := dIns.(*ssa.Defer)
					a.deferToRet[deferIns] = theIns
					if _, ok := deferIns.Call.Value.(*ssa.Builtin); ok {
						continue
					}
					if deferIns.Call.StaticCallee() == nil {
						continue
					} else if a.fromPkgsOfInterest(deferIns.Call.StaticCallee()) && deferIns.Call.StaticCallee().Pkg.Pkg.Name() != "sync" {
						fnName := deferIns.Call.Value.Name()
						fnName = checkTokenNameDefer(fnName, deferIns)
						if !a.exploredFunction(deferIns.Call.StaticCallee(), goID, dIns) {
							a.updateRecords(fnName, goID, "PUSH ", deferIns.Call.StaticCallee(), dIns)
							a.RWIns[goID] = append(a.RWIns[goID], dIns)
							a.visitAllInstructions(deferIns.Call.StaticCallee(), goID)
						}
					} else if deferIns.Call.StaticCallee().Name() == "Unlock" {
						lockLoc := deferIns.Call.Args[0]
						if !useNewPTA {
							a.mu.Lock()
							a.ptaCfg0.AddQuery(lockLoc)
							a.mu.Unlock()
						}
						toUnlock = append(toUnlock, lockLoc)
					} else if deferIns.Call.StaticCallee().Name() == "RUnlock" {
						RlockLoc := deferIns.Call.Args[0]
						if !useNewPTA {
							a.mu.Lock()
							a.ptaCfg0.AddQuery(RlockLoc)
							a.mu.Unlock()
						}
						toRUnlock = append(toRUnlock, RlockLoc)
					} else if deferIns.Call.Value.Name() == "Done" {
						a.RWIns[goID] = append(a.RWIns[goID], dIns)
						if !useNewPTA {
							a.mu.Lock()
							a.ptaCfg0.AddQuery(deferIns.Call.Args[0])
							a.mu.Unlock()
						}
					}
				}
				toDefer = []ssa.Instruction{}
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
						if _, ok1 := examIns.Val.(*ssa.Alloc); ok1 /*&& v.Comment == "complit"*/ {
							// declare&init
						} else {
							a.insStore(examIns, goID, theIns)
						}
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
			case *ssa.Alloc:
				a.RWIns[goID] = append(a.RWIns[goID], theIns)
				if a.inLoop {
					a.allocLoop[examIns.Parent()] = append(a.allocLoop[examIns.Parent()], examIns.Comment)
				}
			case *ssa.Go: // for spawning of goroutines
				if closure, ok := examIns.Call.Value.(*ssa.MakeClosure); ok && len(closure.Bindings) > 0 && a.inLoop { // if spawned in a loop
					for i, binding := range closure.Bindings {
						parentFn := examIns.Parent()
						if fvar, ok1 := binding.(*ssa.Alloc); ok1 && sliceContainsStr(a.allocLoop[parentFn], fvar.Comment) {
							a.bindingFV[examIns] = append(a.bindingFV[examIns], closure.Fn.(*ssa.Function).FreeVars[i]) // store freeVars declared within loop
						}
					}
				}
				loopID := 0
				if a.inLoop {
					loopID++
					a.insGo(examIns, goID, theIns, loopID) // loopID == 1 if goroutine in loop
					loopID++
				}
				a.insGo(examIns, goID, theIns, loopID) // loopID == 2 if goroutine in loop, loopID == 0 otherwise
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
				a.selectBloc[aBlock.Index] = examIns
			case *ssa.If:
				a.RWIns[goID] = append(a.RWIns[goID], theIns)
				ifIns = examIns
			default:
				a.RWIns[goID] = append(a.RWIns[goID], theIns) // TODO: consolidate
			}
			if ii == len(aBlock.Instrs)-1 && len(toUnlock) > 0 { // TODO: this can happen too early
				for _, l := range toUnlock {
					if a.lockSetContainsAt(a.lockSet, l, goID) != -1 {
						a.lockSet[goID][a.lockSetContainsAt(a.lockSet, l, goID)].locFreeze = false
					}
				}
			} else if ii == len(aBlock.Instrs)-1 && len(toRUnlock) > 0 { // TODO: modify for unlock in diff thread
				for _, l := range toRUnlock {
					if a.lockSetContainsAt(a.RlockSet, l, goID) != -1 {
						a.RlockSet[goID][a.lockSetContainsAt(a.RlockSet, l, goID)].locFreeze = false
					}
				}
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
		if (aBlock.Comment == "for.body" || aBlock.Comment == "rangeindex.body") && a.inLoop {
			a.inLoop = false
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
	if len(toUnlock) > 0 {// ----> !!! SEE HERE: bz: the same as above, from line 488 to 501 can be separated out
		for _, loc := range toUnlock {
			if z := a.lockSetContainsAt(a.lockSet, loc, goID); z >= 0 {
				log.Trace("Unlocking ", loc.String(), "  (", a.lockSet[goID][z].locAddr.Pos(), ") removing index ", z, " from: ", lockSetVal(a.lockSet, goID))
				a.lockSet[goID] = append(a.lockSet[goID][:z], a.lockSet[goID][z+1:]...)
			} else {
				z = a.lockSetContainsAt(a.lockSet, loc, a.goCaller[goID])
				if z == -1 {
					continue
				}
				a.lockSet[a.goCaller[goID]] = append(a.lockSet[a.goCaller[goID]][:z], a.lockSet[a.goCaller[goID]][z+1:]...)
			}
		}
	}
	if len(toRUnlock) > 0 {// ----> !!! SEE HERE: bz: the same as above, from line 502 to 509 can be separated out
		for _, rloc := range toRUnlock {
			if z := a.lockSetContainsAt(a.RlockSet, rloc, goID); z >= 0 {
				log.Trace("RUnlocking ", rloc.String(), "  (", a.RlockSet[goID][z].locAddr.Pos(), ") removing index ", z, " from: ", lockSetVal(a.RlockSet, goID))
				a.RlockSet[goID] = append(a.RlockSet[goID][:z], a.RlockSet[goID][z+1:]...)
			} //TODO : modify for unlock in diff thread
		}
	}
	if len(toDefer) > 0 {
		for _, dIns := range toDefer {  // ----> !!! SEE HERE: bz: the same as above, from line 307 to 347 can be separated out
			deferIns := dIns.(*ssa.Defer)
			if _, ok := deferIns.Call.Value.(*ssa.Builtin); ok {
				continue
			}
			if deferIns.Call.StaticCallee() == nil {
				continue
			} else if a.fromPkgsOfInterest(deferIns.Call.StaticCallee()) && deferIns.Call.StaticCallee().Pkg.Pkg.Name() != "sync" {
				fnName := deferIns.Call.Value.Name()
				fnName = checkTokenNameDefer(fnName, deferIns)
				if !a.exploredFunction(deferIns.Call.StaticCallee(), goID, dIns) {
					a.updateRecords(fnName, goID, "PUSH ", deferIns.Call.StaticCallee(), dIns)
					a.RWIns[goID] = append(a.RWIns[goID], dIns)
					a.visitAllInstructions(deferIns.Call.StaticCallee(), goID)
				}
			} else if deferIns.Call.StaticCallee().Name() == "Unlock" {
				lockLoc := deferIns.Call.Args[0]
				if !useNewPTA {
					a.mu.Lock()
					a.ptaCfg0.AddQuery(lockLoc)
					a.mu.Unlock()
				}
				toUnlock = append(toUnlock, lockLoc)
			} else if deferIns.Call.StaticCallee().Name() == "RUnlock" {
				RlockLoc := deferIns.Call.Args[0]
				if !useNewPTA {
					a.mu.Lock()
					a.ptaCfg0.AddQuery(RlockLoc)
					a.mu.Unlock()
				}
				toRUnlock = append(toRUnlock, RlockLoc)
			} else if deferIns.Call.Value.Name() == "Done" {
				a.RWIns[goID] = append(a.RWIns[goID], dIns)
				if !useNewPTA {
					a.mu.Lock()
					a.ptaCfg0.AddQuery(deferIns.Call.Args[0])
					a.mu.Unlock()
				}
			}
			toDefer = []ssa.Instruction{}
		}
	}
	// done with all instructions in function body, now pop the function
	fnName := fn.Name()
	if fnName == a.storeFns[len(a.storeFns)-1].fnIns.Name() {
		a.updateRecords(fnName, goID, "POP  ", fn, nil)
	}
	if len(a.storeFns) == 0 && len(a.workList) != 0 { // finished reporting current goroutine and workList isn't empty
		nextGoInfo := a.workList[0] // get the goroutine info at head of workList
		a.workList = a.workList[1:] // pop goroutine info from head of workList
		a.newGoroutine(nextGoInfo)
	}
}

func (a *analysis) goNames(goIns *ssa.Go) string {
	var goName string
	switch anonFn := goIns.Call.Value.(type) {
	case *ssa.MakeClosure: // go call for anonymous function
		goName = anonFn.Fn.Name()
	case *ssa.Function:
		goName = anonFn.Name()
	case *ssa.TypeAssert:
		switch anonFn.X.(type) {
		case *ssa.Parameter:
			a.pointerAnalysis(anonFn.X, 0, nil)
			if a.paramFunc != nil {
				goName = a.paramFunc.Name()
			}
		}
	}
	return goName
}

// newGoroutine goes through the goroutine, logs its info, and goes through the instructions within
func (a *analysis) newGoroutine(info goroutineInfo) {
	if a.goCalls[a.goCaller[info.goID]] != nil && info.goIns == a.goCalls[a.goCaller[info.goID]].goIns {
		return // recursive spawning of same goroutine
	}
	newFn := fnCallInfo{fnIns: info.entryMethod, ssaIns: info.ssaIns}
	a.storeFns = append(a.storeFns, newFn)
	if info.goID >= len(a.RWIns) { // initialize interior slice for new goroutine
		a.RWIns = append(a.RWIns, []ssa.Instruction{})
	}
	a.RWIns[info.goID] = append(a.RWIns[info.goID], info.ssaIns)
	newGoInfo := &goCallInfo{goIns: info.goIns, ssaIns: info.ssaIns}
	a.goCalls[info.goID] = newGoInfo
	if !allEntries {
		if a.loopIDs[info.goID] > 0 {
			a.goInLoop[info.goID] = true
			log.Debug(strings.Repeat("-", 35), "Goroutine ", info.entryMethod.Name(), " (in loop)", strings.Repeat("-", 35), "[", info.goID, "]")
		} else {
			log.Debug(strings.Repeat("-", 35), "Goroutine ", info.entryMethod.Name(), strings.Repeat("-", 35), "[", info.goID, "]")
		}
	}
	//if len(a.lockSet[a.goCaller[info.goID]]) > 0 {
	//	a.lockSet[info.goID] = a.lockSet[a.goCaller[info.goID]]
	//}
	if !allEntries {
		log.Debug(strings.Repeat(" ", a.levels[info.goID]), "PUSH ", info.entryMethod.Name(), " at lvl ", a.levels[info.goID])
		fnCall := fnCallIns{fnIns: info.entryMethod, goID: info.goID}
		stack := make([]fnCallInfo, len(a.storeFns))
		copy(stack, a.storeFns)
		a.stackMap[fnCall] = stackInfo{fnCalls: stack}
	}
	a.levels[info.goID]++
	switch info.goIns.Call.Value.(type) {
	case *ssa.MakeClosure:
		a.visitAllInstructions(info.goIns.Call.StaticCallee(), info.goID)
	case *ssa.TypeAssert:
		a.visitAllInstructions(a.paramFunc, info.goID)
	default:
		a.visitAllInstructions(info.goIns.Call.StaticCallee(), info.goID)
	}
}

// exploredFunction determines if we already visited this function
func (a *analysis) exploredFunction(fn *ssa.Function, goID int, theIns ssa.Instruction) bool {
	if a.efficiency && !a.fromPkgsOfInterest(fn) { // for temporary debugging purposes only
		return true
	}
	if sliceContainsInsAt(a.RWIns[goID], theIns) >= 0 {
		return true
	}
	theFn := fnCallInfo{fn, theIns}
	if a.efficiency && sliceContainsFnCall(a.storeFns, theFn) { // for temporary debugging purposes only
		return true
	}
	var visitedIns []ssa.Instruction
	if len(a.RWIns) > 0 {
		visitedIns = a.RWIns[goID]
	}
	csSlice, csStr := insToCallStack(visitedIns)
	if sliceContainsFnCtr(csSlice, fn) > trieLimit {
		return true
	}
	fnKey := fnInfo{
		fnName:     fn,
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
