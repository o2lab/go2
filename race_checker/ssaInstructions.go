package main

import (
	"github.com/o2lab/race-checker/stats"
	log "github.com/sirupsen/logrus"
	"github.tamu.edu/April1989/go_tools/go/ssa"
	"go/constant"
	"go/token"
	"go/types"
	"strconv"
	"strings"
)

// checkTokenName will return original name of an input function rather than a token
func checkTokenName(fnName string, theIns *ssa.Call) string {
	if strings.HasPrefix(fnName, "t") { // function name begins with letter t
		if _, err := strconv.Atoi(string([]rune(fnName)[1:])); err == nil { // function name after first character look like an integer
			switch callVal := theIns.Call.Value.(type) {
			case *ssa.MakeClosure:
				fnName = callVal.Fn.Name()
			default:
				fnName = callVal.Type().String()
			}
		}
	}
	return fnName
}

// checkTokenNameDefer will return original name of an input defered function rather than a token
func checkTokenNameDefer(fnName string, theIns *ssa.Defer) string {
	if strings.HasPrefix(fnName, "t") { // function name begins with letter t
		if _, err := strconv.Atoi(string([]rune(fnName)[1:])); err == nil { // function name after first character look like an integer
			switch callVal := theIns.Call.Value.(type) {
			case *ssa.MakeClosure:
				fnName = callVal.Fn.Name()
			default:
				fnName = callVal.Type().String()
			}
		}
	}
	return fnName
}

// isReadIns determines if the instruction is a read access
func (a *analysis) isReadIns(ins ssa.Instruction) bool {
	switch insType := ins.(type) {
	case *ssa.UnOp:
		if rIns, ok := insType.X.(*ssa.Alloc); ok {
			if _, ok1 := a.chanRcvs[rIns.Comment]; ok1 {
				return false // a channel op, handled differently than typical read ins
			}
		}
		return true
	case *ssa.Lookup:
		return true
	case *ssa.Call:
		if len(insType.Call.Args) > 0 && insType.Call.Value.Name() != "Done" && insType.Call.Value.Name() != "Wait" {
			for _, anArg := range insType.Call.Args {
				if _, ok := anArg.(*ssa.UnOp); ok {
					return true
				}
			}
		}
	default:
		_ = insType
	}
	return false
}

// isWriteIns determines if the instruction is a write access
func isWriteIns(ins ssa.Instruction) bool {
	switch insType := ins.(type) {
	case *ssa.Store:
		return true
	case *ssa.Call:
		if insType.Call.Value.Name() == "delete" {
			return true
		} else if strings.HasPrefix(insType.Call.Value.Name(), "Add") && insType.Call.StaticCallee().Pkg.Pkg.Name() == "atomic" {

			return true
		}
	case *ssa.MapUpdate:
		return true
	}
	return false
}

// updateRecords will print out the stack trace
func (a *analysis) updateRecords(fnName string, goID int, pushPop string) {
	if pushPop == "POP  " {
		a.storeIns = a.storeIns[:len(a.storeIns)-1]
		a.levels[goID]--
	}
	log.Debug(strings.Repeat(" ", a.levels[goID]), pushPop, fnName, " at lvl ", a.levels[goID])
	if pushPop == "PUSH " {
		a.storeIns = append(a.storeIns, fnName)
		a.levels[goID]++
	}
}

// insMakeChan takes make channel instructions and stores their name and buffer size
func (a *analysis) insMakeChan(examIns *ssa.MakeChan, insInd int) {
	stats.IncStat(stats.NMakeChan)
	var bufferLen int64
	if bufferInfo, ok := examIns.Size.(*ssa.Const); ok { // buffer length passed via constant
		temp, _ := constant.Int64Val(constant.ToInt(bufferInfo.Value))
		bufferLen = temp
	} else if _, ok := examIns.Size.(*ssa.Parameter); ok { // buffer length passed via function parameter
		bufferLen = 10 // TODO: assuming channel can buffer up to 10 values could result in false positives
	}
	instrs := examIns.Block().Instrs
	if insInd > 0 {
		switch ch := instrs[insInd-1].(type) {
		case *ssa.Alloc:
			a.chanBuf[ch.Comment] = int(bufferLen) // 0 length - unbuffered channel, otherwise - buffered channel
		}
	}
}

// insSend ???
func (a *analysis) insSend(examIns *ssa.Send, goID int, theIns ssa.Instruction) string {
	stats.IncStat(stats.NSend)
	a.RWIns[goID] = append(a.RWIns[goID], theIns)
	ch := examIns.Chan.Name()
	if _, ok := a.chanBuf[ch]; !ok {
		switch chN := examIns.Chan.(type) {
		case *ssa.UnOp:
			switch chName := chN.X.(type) {
			case *ssa.Alloc:
				ch = chName.Comment
			case *ssa.FreeVar:
				ch = chName.Name()
			case *ssa.FieldAddr:
				switch chName.X.(type) {
				case *ssa.Parameter:
					ch = chName.X.(*ssa.Parameter).Name()
				case *ssa.FieldAddr:
					ch = chName.X.(*ssa.FieldAddr).Name()
				}
			default:
				log.Trace("need to consider this case for channel name collection")
			}
			a.chanSnds[ch] = append(a.chanSnds[ch], examIns)
		default: // may need to consider other cases as well
			log.Trace("need to consider this case for channel send")
		}
	} else {
		a.chanSnds[ch] = append(a.chanSnds[ch], examIns)
	}
	return ch
}

// insStore  ???
func (a *analysis) insStore(examIns *ssa.Store, goID int, theIns ssa.Instruction) {
	stats.IncStat(stats.NStore)
	if !isLocalAddr(examIns.Addr) {
		if len(a.storeIns) > 1 {
			if a.storeIns[len(a.storeIns)-2] == "AfterFunc" { // ignore this write instruction as AfterFunc is analyzed elsewhere
				return
			}
		}
		a.RWIns[goID] = append(a.RWIns[goID], theIns)
		a.updateLockMap(goID, theIns)
		a.ptaConfig.AddQuery(examIns.Addr)
	}
	if theFunc, storeFn := examIns.Val.(*ssa.Function); storeFn {
		if !a.exploredFunction(theFunc, goID, theIns) {
			a.updateRecords(theFunc.Name(), goID, "PUSH ")
			a.RWIns[goID] = append(a.RWIns[goID], theIns)
			a.visitAllInstructions(theFunc, goID)
		}
	}
}

// insUnOp ???
func (a *analysis) insUnOp(examIns *ssa.UnOp, goID int, theIns ssa.Instruction) {
	stats.IncStat(stats.NUnOp)
	a.RWIns[goID] = append(a.RWIns[goID], theIns)
	if examIns.Op == token.MUL && !isLocalAddr(examIns.X) { // read op
		a.updateLockMap(goID, theIns)
		a.updateRLockMap(goID, theIns)
		a.ptaConfig.AddQuery(examIns.X)
	} else if examIns.Op == token.ARROW { // channel receive op (not waited on by select)
		stats.IncStat(stats.NChanRecv)
		ch := examIns.X.Name()
		if _, ok := a.chanBuf[ch]; !ok {
			switch chN := examIns.X.(type) {
			case *ssa.UnOp:
				switch chName := chN.X.(type) {
				case *ssa.Alloc:
					ch = chName.Comment
				case *ssa.FreeVar:
					ch = chName.Name()
				case *ssa.FieldAddr:
					switch chName.X.(type) {
					case *ssa.Parameter:
						ch = chName.X.(*ssa.Parameter).Name()
					case *ssa.FieldAddr:
						ch = chName.X.(*ssa.FieldAddr).Name()
					}
				default:
					log.Trace("need to consider this case for channel name collection")
				}
				a.chanRcvs[ch] = append(a.chanRcvs[ch], examIns)
			default: // may need to consider other cases as well
				log.Trace("need to consider this case for channel send")
			}
		} else {
			a.chanRcvs[ch] = append(a.chanRcvs[ch], examIns)
		}
	}
}

// insFieldAddr ???
func (a *analysis) insFieldAddr(examIns *ssa.FieldAddr, goID int, theIns ssa.Instruction) {
	stats.IncStat(stats.NFieldAddr)
	if !isLocalAddr(examIns.X) {
		a.RWIns[goID] = append(a.RWIns[goID], theIns)
		a.updateLockMap(goID, theIns)
		a.updateRLockMap(goID, theIns)
		a.ptaConfig.AddQuery(examIns.X)
	}
}

// insLookUp ???
func (a *analysis) insLookUp(examIns *ssa.Lookup, goID int, theIns ssa.Instruction) {
	stats.IncStat(stats.NLookup)
	switch readIns := examIns.X.(type) {
	case *ssa.UnOp:
		if readIns.Op == token.MUL && !isLocalAddr(readIns.X) {
			a.RWIns[goID] = append(a.RWIns[goID], theIns)
			a.updateLockMap(goID, theIns)
			a.updateRLockMap(goID, theIns)
			a.ptaConfig.AddQuery(readIns.X)
		}
	case *ssa.Parameter:
		if !isLocalAddr(readIns) {
			a.RWIns[goID] = append(a.RWIns[goID], theIns)
			a.updateLockMap(goID, theIns)
			a.updateRLockMap(goID, theIns)
			a.ptaConfig.AddQuery(readIns)
		}
	}
}

// insChangeType
func (a *analysis) insChangeType(examIns *ssa.ChangeType, goID int, theIns ssa.Instruction) {
	stats.IncStat(stats.NChangeType)
	switch mc := examIns.X.(type) {
	case *ssa.MakeClosure: // yield closure value for *Function and free variable values supplied by Bindings
		theFn := mc.Fn.(*ssa.Function)
		if fromPkgsOfInterest(theFn) {
			fnName := mc.Fn.Name()
			if !a.exploredFunction(theFn, goID, theIns) {
				a.updateRecords(fnName, goID, "PUSH ")
				a.RWIns[goID] = append(a.RWIns[goID], theIns)
				a.updateLockMap(goID, theIns)
				a.updateRLockMap(goID, theIns)
				a.ptaConfig.AddQuery(examIns.X)
				a.visitAllInstructions(theFn, goID)
			}
		}
	default:
		return
	}
}

// insMakeInterface ???
func (a *analysis) insMakeInterface(examIns *ssa.MakeInterface, goID int, theIns ssa.Instruction) {
	stats.IncStat(stats.NMakeInterface)
	if strings.Contains(examIns.X.String(), "complit") {
		return
	}
	switch insType := examIns.X.(type) {
	case *ssa.Call:
		a.pointerAnalysis(examIns.X, goID, theIns)
	case *ssa.Parameter:
		if _, ok := insType.Type().(*types.Basic); !ok {
			a.pointerAnalysis(examIns.X, goID, theIns)
		}
	case *ssa.UnOp:
		if _, ok := insType.X.(*ssa.Global); !ok {
			a.pointerAnalysis(examIns.X, goID, theIns)
		}
	default:
		return
	}
}

// insCall analyzes method calls
func (a *analysis) insCall(examIns *ssa.Call, goID int, theIns ssa.Instruction) (unlockOps []ssa.Value, runlockOps []ssa.Value) {
	stats.IncStat(stats.NCall)
	if examIns.Call.StaticCallee() == nil && examIns.Call.Method == nil {
		if _, ok := examIns.Call.Value.(*ssa.Builtin); !ok {
			a.pointerAnalysis(examIns.Call.Value, goID, theIns)
		} else if examIns.Call.Value.Name() == "delete" { // built-in delete op
			if theVal, ok := examIns.Call.Args[0].(*ssa.UnOp); ok {
				if theVal.Op == token.MUL && !isLocalAddr(theVal.X) {
					a.RWIns[goID] = append(a.RWIns[goID], theIns)
					a.updateLockMap(goID, theIns)
					a.updateRLockMap(goID, theIns)
					if !a.useNewPTA {
						a.ptaConfig.AddQuery(theVal.X)
					}
				}
			}
		} else if examIns.Call.Value.Name() == "close" { // closing a channel
			for _, arg := range examIns.Call.Args {
				switch ch := arg.(type) {
				case *ssa.UnOp:
					switch chN := ch.X.(type) {
					case *ssa.Alloc:
						if _, exCh := a.chanBuf[chN.Comment]; exCh {
							delete(a.chanBuf, chN.Comment)
							delete(a.chanSnds, chN.Comment)
						}
					case *ssa.FieldAddr:
						// TODO: detect chan struct declaration
					}
				}
			}
		} else {
			return
		}
	} else if examIns.Call.Method != nil && examIns.Call.Method.Pkg() != nil { // calling an method
		if examIns.Call.Method.Pkg().Name() != "sync" {
			if _, ok := examIns.Call.Value.(*ssa.Builtin); !ok {
				a.pointerAnalysis(examIns.Call.Value, goID, theIns)
			} else {
				return
			}
		}
	} else if examIns.Call.StaticCallee() == nil {
		//log.Debug("***********************special case*****************************************")
		return
	} else if fromPkgsOfInterest(examIns.Call.StaticCallee()) && examIns.Call.StaticCallee().Pkg.Pkg.Name() != "sync" { // calling a function
		if examIns.Call.Value.Name() == "AfterFunc" && examIns.Call.StaticCallee().Pkg.Pkg.Name() == "time" { // calling time.AfterFunc()
			a.paramFunc = examIns.Call.Args[1]
		}
		for _, checkArgs := range examIns.Call.Args {
			switch access := checkArgs.(type) {
			case *ssa.FieldAddr:
				if !isLocalAddr(access.X) && strings.HasPrefix(examIns.Call.Value.Name(), "Add") {
					a.RWIns[goID] = append(a.RWIns[goID], theIns)
					a.updateLockMap(goID, theIns)
					a.updateRLockMap(goID, theIns)
					if !a.useNewPTA {
						a.ptaConfig.AddQuery(access.X)
					}
				}
			default:
				continue
			}
		}
		if examIns.Call.StaticCallee().Blocks == nil {
			return
		}
		fnName := examIns.Call.Value.Name()
		fnName = checkTokenName(fnName, examIns)
		if !a.exploredFunction(examIns.Call.StaticCallee(), goID, theIns) {
			a.updateRecords(fnName, goID, "PUSH ")
			a.RWIns[goID] = append(a.RWIns[goID], theIns)
			a.visitAllInstructions(examIns.Call.StaticCallee(), goID)
		}
	} else if examIns.Call.StaticCallee().Pkg.Pkg.Name() == "sync" {
		switch examIns.Call.Value.Name() {
		case "Range":
			fnName := examIns.Call.Value.Name()
			if !a.exploredFunction(examIns.Call.StaticCallee(), goID, theIns) {
				a.updateRecords(fnName, goID, "PUSH ")
				a.RWIns[goID] = append(a.RWIns[goID], theIns)
				a.visitAllInstructions(examIns.Call.StaticCallee(), goID)
			}
		case "Lock":
			stats.IncStat(stats.NLock)
			lockLoc := examIns.Call.Args[0]         // identifier for address of lock
			if !a.useNewPTA {
				a.ptaConfig.AddQuery(lockLoc)
			}
			if goID == 0 { // main goroutine
				if !sliceContains(a.lockSet, lockLoc) { // if lock is not already in active lockset
					a.lockSet = append(a.lockSet, lockLoc)
					log.Trace("Locking   ", lockLoc.String(), "  (",  lockLoc.Pos(), ")  lockset now contains: ", lockSetVal(a.lockSet))
				}
			} else { // worker goroutine
				if !sliceContains(a.goLockset[goID], lockLoc) { // if lock is not already in active lockset
					a.goLockset[goID] = append(a.goLockset[goID], lockLoc)
					log.Trace("Locking   ", lockLoc.String(), "  (",  lockLoc.Pos(), ")  lockset now contains: ", lockSetVal(a.goLockset[goID]))
				}
			}
		case "Unlock":
			stats.IncStat(stats.NUnlock)
			lockLoc := examIns.Call.Args[0]
			if !a.useNewPTA {
				a.ptaConfig.AddQuery(lockLoc)
			}
			unlockOps = append(unlockOps, lockLoc)
			a.mapFreeze = true
		case "RLock":
			RlockLoc := examIns.Call.Args[0]          // identifier for address of lock
			if !a.useNewPTA {
				a.ptaConfig.AddQuery(RlockLoc)
			}
			if goID == 0 {
				if !sliceContains(a.RlockSet, RlockLoc) { // if lock is not already in active lock-set
					a.RlockSet = append(a.RlockSet, RlockLoc)
					log.Trace("RLocking   ", RlockLoc.String(), "  (",  RlockLoc.Pos(), ")  Rlockset now contains: ", lockSetVal(a.RlockSet))
				}
			} else {
				if !sliceContains(a.goRLockset[goID], RlockLoc) { // if lock is not already in active lock-set
					a.goRLockset[goID] = append(a.goRLockset[goID], RlockLoc)
					log.Trace("RLocking   ", RlockLoc.String(), "  (",  RlockLoc.Pos(), ")  Rlockset now contains: ", lockSetVal(a.goRLockset[goID]))
				}
			}
		case "RUnlock":
			RlockLoc := examIns.Call.Args[0]
			if !a.useNewPTA {
				a.ptaConfig.AddQuery(RlockLoc)
			}
			runlockOps = append(runlockOps, RlockLoc)
			a.mapFreeze = true
		case "Wait":
			stats.IncStat(stats.NWaitGroupWait)
			a.RWIns[goID] = append(a.RWIns[goID], theIns)
			if !a.useNewPTA {
				a.ptaConfig.AddQuery(examIns.Call.Args[0])
			}
		case "Done":
			stats.IncStat(stats.NWaitGroupDone)
			a.RWIns[goID] = append(a.RWIns[goID], theIns)
			if !a.useNewPTA {
				a.ptaConfig.AddQuery(examIns.Call.Args[0])
			}
		}
	} else {
		return
	}
	return unlockOps, runlockOps
}

// insGo analyzes go calls
func (a *analysis) insGo(examIns *ssa.Go, goID int, theIns ssa.Instruction) {
	stats.IncStat(stats.NGo)
	var fnName string
	switch anonFn := examIns.Call.Value.(type) {
	case *ssa.MakeClosure: // go call for anonymous function
		fnName = anonFn.Fn.Name()
	case *ssa.Function:
		fnName = anonFn.Name()
	case *ssa.TypeAssert:
		switch paramType := a.paramFunc.(type) {
		case *ssa.Function:
			fnName = paramType.Name()
		case *ssa.MakeClosure:
			fnName = paramType.Fn.Name()
		}
	}

	newGoID := goID + 1 // increment goID for child goroutine
	if len(a.workList) > 0 {
		newGoID = a.workList[len(a.workList)-1].goID + 1
	}
	a.RWIns[goID] = append(a.RWIns[goID], theIns)
	if goID == 0 && a.insDRA == 0 { // this is first *ssa.Go instruction in main goroutine
		a.insDRA = len(a.RWIns[goID]) // race analysis will begin at this instruction
	}

	var info = goroutineInfo{examIns, fnName, newGoID}
	a.goID2info[newGoID] = info //record
	a.goStack = append(a.goStack, []string{}) // initialize interior slice
	a.goCaller[newGoID] = goID                // map caller goroutine
	a.goStack[newGoID] = append(a.goStack[newGoID], a.storeIns...)
	a.workList = append(a.workList, info) // store encountered goroutines
	log.Debug(strings.Repeat(" ", a.levels[goID]), "spawning Goroutine ----->  ", fnName)
}

func (a *analysis) insMapUpdate(examIns *ssa.MapUpdate, goID int, theIns ssa.Instruction) {
	stats.IncStat(stats.NStore)
	a.RWIns[goID] = append(a.RWIns[goID], theIns)
	a.updateLockMap(goID, theIns)
	switch ptType := examIns.Map.(type) {
	case *ssa.UnOp:
		if !a.useNewPTA {
			a.ptaConfig.AddQuery(ptType.X)
		}
	default:
	}
}

func (a *analysis) insSelect(examIns *ssa.Select, goID int, theIns ssa.Instruction) []string {
	a.RWIns[goID] = append(a.RWIns[goID], theIns)
	defaultCase := 0
	if !examIns.Blocking { defaultCase++ } // non-blocking select
	readyChans := make([]string, len(examIns.States) + defaultCase) // name of ready channels
	for i, state := range examIns.States { // check readiness of each case
		switch ch := state.Chan.(type) {
		case *ssa.UnOp: // channel is ready
			switch chName := ch.X.(type) {
			case *ssa.Alloc:
				readyChans[i] = chName.Comment
			case *ssa.FreeVar:
				readyChans[i] = chName.Name()
			case *ssa.FieldAddr:
				switch chName.X.(type) {
				case *ssa.Parameter:
					readyChans[i] = chName.X.(*ssa.Parameter).Name()
				case *ssa.FieldAddr:
					readyChans[i] = chName.X.(*ssa.FieldAddr).Name()
				}
			default:
				log.Trace("need to consider this case for channel name collection")
			}
			if !sliceContainsRcv(a.chanRcvs[readyChans[i]], ch) {
				a.chanRcvs[readyChans[i]] = append(a.chanRcvs[readyChans[i]], ch)
			}
			if !sliceContainsStr(a.selReady[examIns], readyChans[i]) {
				a.selReady[examIns] = append(a.selReady[examIns], readyChans[i])
			}
		case *ssa.Parameter:
			// TODO: need to identify channel readiness when passed in as input parameter
			if state.Dir == 1 { // send Only

			} else if state.Dir == 2 { // receive Only

			} else { // state.Dir == 0, send receive

			}
		case *ssa.TypeAssert:

		case *ssa.Call: // timeOut
			readyChans[i] = "timeOut"
		case *ssa.MakeChan: // channel NOT ready
		default: // may need to consider other cases as well
			log.Trace("need to consider this case for channel readiness")
		}
	}
	if defaultCase > 0 { // default case is always ready
		readyChans[len(readyChans)-1] = "defaultCase"
	}
	return readyChans
}

func (a *analysis) updateLockMap(goID int, theIns ssa.Instruction) {
	if goID == 0 { // main goroutine
		if len(a.lockSet) > 0 && !a.mapFreeze {
			a.lockMap[theIns] = append(a.lockMap[theIns], a.lockSet...)
		} else if len(a.lockSet) > 0 && a.mapFreeze {
			a.lockMap[theIns] = append(a.lockMap[theIns], a.lockSet[:len(a.lockSet)-1]...) // may not always be last lock
		}
	} else { // worker goroutine
		if len(a.goLockset[goID]) > 0 && !a.mapFreeze {
			a.lockMap[theIns] = append(a.lockMap[theIns], a.goLockset[goID]...)
		} else if len(a.goLockset[goID]) > 0 && a.mapFreeze {
			a.lockMap[theIns] = append(a.lockMap[theIns], a.goLockset[goID][:len(a.goLockset[goID])-1]...)
		}
	}
}

func (a *analysis) updateRLockMap(goID int, theIns ssa.Instruction) {
	if goID == 0 { // main goroutine
		if len(a.RlockSet) > 0 {
			a.RlockMap[theIns] = append(a.RlockMap[theIns], a.RlockSet...)
		}
	} else { // worker goroutine
		if len(a.goRLockset[goID]) > 0 {
			a.RlockMap[theIns] = append(a.RlockMap[theIns], a.goRLockset[goID]...)
		}
	}
}