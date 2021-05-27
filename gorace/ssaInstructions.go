package main

import (
	"github.com/april1989/origin-go-tools/go/ssa"
	log "github.com/sirupsen/logrus"
	"go/constant"
	"go/token"
	"go/types"
	"strings"
)


/*
bz: this file contains functions to handle all ssa.Instruction
 */


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
	case *ssa.FieldAddr:
		if _, isAlloc := insType.X.(*ssa.Alloc); isAlloc {
			return false
		} else if _, isFreeVar := insType.X.(*ssa.FreeVar); isFreeVar {
			return false
		}
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
	case *ssa.ChangeType:
		return true
	}
	return false
}


// insMakeChan takes make channel instructions and stores their name and buffer size
func (a *analysis) insMakeChan(examIns *ssa.MakeChan, insInd int) {
	var bufferLen int64
	if bufferInfo, ok := examIns.Size.(*ssa.Const); ok { // buffer length passed via constant
		temp, _ := constant.Int64Val(constant.ToInt(bufferInfo.Value))
		bufferLen = temp
	} else if _, ok1 := examIns.Size.(*ssa.Parameter); ok1 { // buffer length passed via function parameter
		bufferLen = 10 // TODO: assuming channel can buffer up to 10 values could ptaRes in false positives
	}
	instrs := examIns.Block().Instrs
	if insInd > 0 {
		switch ch := instrs[insInd-1].(type) {
		case *ssa.Alloc:
			a.chanBuf[ch.Comment] = int(bufferLen) // 0 length - unbuffered channel, otherwise - buffered channel
			a.chanToken[examIns.Name()] = ch.Comment
		default:
			a.chanToken[examIns.Name()] = ""
		}
	}
}

// insSend ???
func (a *analysis) insSend(examIns *ssa.Send, goID int, theIns ssa.Instruction) string {
	a.recordIns(goID, theIns)
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
	if !isLocalAddr(examIns.Addr) {
		if len(a.storeFns) > 1 {
			if a.storeFns[len(a.storeFns)-2].fnIns.Name() == "AfterFunc" { // ignore this write instruction as AfterFunc is analyzed elsewhere
				return
			}
		}
		a.recordIns(goID, theIns)
		//a.RWIns[goID] = append(a.RWIns[goID], theIns)
		a.updateLockMap(goID, theIns)
		if !useNewPTA {
			a.mu.Lock()
			a.ptaCfg0.AddQuery(examIns.Addr)
			a.mu.Unlock()
		}
	}
	if theFunc, storeFn := examIns.Val.(*ssa.Function); storeFn {
		a.traverseFn(theFunc, theFunc.Name(), goID, theIns)
	}
}

// insUnOp ???
func (a *analysis) insUnOp(examIns *ssa.UnOp, goID int, theIns ssa.Instruction) {
	if examIns.Op == token.MUL && !isLocalAddr(examIns.X) { // read op
		a.updateLockMap(goID, theIns)
		a.updateRLockMap(goID, theIns)
		if !useNewPTA {
			a.mu.Lock()
			a.ptaCfg0.AddQuery(examIns.X)
			a.mu.Unlock()
		}
		//if v, isGlobalVar := examIns.X.(*ssa.Global); isGlobalVar {
		//	if strct, isStruct := v.Type().(*types.Pointer).Elem().Underlying().(*types.Struct); isStruct {
		//		for i := 0; i < strct.NumFields(); i++ {
		//			f := strct.Field(i)
		//			switch f.Type().(type) {
		//			case *types.Interface: //bz: fit checkpointCommand field Action in /tests/runc_simple.go, is there other types that leads to func?
		//				a.pointerNewAnalysisOffset(v, i, goID, theIns) //bz: i here is the offset
		//			}
		//		}
		//
		//		//for fnKey, member := range v.Pkg.Members {//bz: why .... are you creating calls to all the functions in v.pkg.members????
		//		//	if memberFn, isFn := member.(*ssa.Function); isFn && fnKey != "main" && fnKey != "init" {
		//		//		if !a.exploredFunction(memberFn, goID, theIns) {
		//		//			a.updateRecords(memberFn.Name(), goID, "PUSH ", memberFn, theIns)
		//		//			a.visitAllInstructions(memberFn, goID)
		//		//		}
		//		//	}
		//		//}
		//	}
		//}
		a.recordIns(goID, theIns)
	} else if examIns.Op == token.ARROW { // channel receive op (not waited on by select)
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
		a.recordIns(goID, theIns)
	}
}

// insFieldAddr ???
func (a *analysis) insFieldAddr(examIns *ssa.FieldAddr, goID int, theIns ssa.Instruction) {
	if !isLocalAddr(examIns.X) {
		a.recordIns(goID, theIns)
		//a.RWIns[goID] = append(a.RWIns[goID], theIns)
		a.updateLockMap(goID, theIns)
		a.updateRLockMap(goID, theIns)
		if !useNewPTA {
			a.mu.Lock()
			a.ptaCfg0.AddQuery(examIns.X)
			a.mu.Unlock()
		}
	}
}

// insLookUp ???
func (a *analysis) insLookUp(examIns *ssa.Lookup, goID int, theIns ssa.Instruction) {
	switch readIns := examIns.X.(type) {
	case *ssa.UnOp:
		if readIns.Op == token.MUL && !isLocalAddr(readIns.X) {
			a.recordIns(goID, theIns)
			a.updateLockMap(goID, theIns)
			a.updateRLockMap(goID, theIns)
		}
	case *ssa.Parameter:
		if !isLocalAddr(readIns) {
			a.recordIns(goID, theIns)
			a.updateLockMap(goID, theIns)
			a.updateRLockMap(goID, theIns)
			if !useNewPTA {
				a.mu.Lock()
				a.ptaCfg0.AddQuery(readIns)
				a.mu.Unlock()
			}
		}
	}
}

// insChangeType
func (a *analysis) insChangeType(examIns *ssa.ChangeType, goID int, theIns ssa.Instruction) {
	switch mc := examIns.X.(type) {
	case *ssa.MakeClosure: // yield closure value for *Function and free variable values supplied by Bindings
		theFn := mc.Fn.(*ssa.Function)
		fnName := mc.Fn.Name()
		a.traverseFn(theFn, fnName, goID, theIns)
		if !useNewPTA {
			a.mu.Lock()
			a.ptaCfg0.AddQuery(examIns.X)
			a.mu.Unlock()
		}
	default:
		return
	}
}

// insMakeInterface ???
func (a *analysis) insMakeInterface(examIns *ssa.MakeInterface, goID int, theIns ssa.Instruction) {
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
	if examIns.Call.StaticCallee() == nil && examIns.Call.Method == nil {
		if _, ok := examIns.Call.Value.(*ssa.Builtin); !ok {
			a.pointerAnalysis(examIns.Call.Value, goID, theIns)
		} else if examIns.Call.Value.Name() == "delete" { // built-in delete op
			if theVal, ok0 := examIns.Call.Args[0].(*ssa.UnOp); ok0 {
				if theVal.Op == token.MUL && !isLocalAddr(theVal.X) {
					a.recordIns(goID, theIns)
					//a.RWIns[goID] = append(a.RWIns[goID], theIns)
					a.updateLockMap(goID, theIns)
					a.updateRLockMap(goID, theIns)
					if !useNewPTA {
						a.mu.Lock()
						a.ptaCfg0.AddQuery(theVal.X)
						a.mu.Unlock()
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
	} else if examIns.Call.StaticCallee().Pkg.Pkg.Name() != "sync" { // calling a function
		//if examIns.Call.Value.Name() == "AfterFunc" && examIns.Call.StaticCallee().Pkg.Pkg.Name() == "time" { // calling time.AfterFunc()
		//	a.paramFunc = examIns.Call.Args[1]
		//}
		for _, checkArgs := range examIns.Call.Args {
			switch access := checkArgs.(type) {
			case *ssa.FieldAddr:
				if !isLocalAddr(access.X) && strings.HasPrefix(examIns.Call.Value.Name(), "Add") {
					a.recordIns(goID, theIns)
					a.updateLockMap(goID, theIns)
					a.updateRLockMap(goID, theIns)
					if !useNewPTA {
						a.mu.Lock()
						a.ptaCfg0.AddQuery(access.X)
						a.mu.Unlock()
					}
				}
			case *ssa.UnOp:
				if !isLocalAddr(access.X) {
					a.recordIns(goID, theIns)
					a.updateLockMap(goID, theIns)
					a.updateRLockMap(goID, theIns)
				}
			//case *ssa.Const:
			//	a.RWIns[goID] = append(a.RWIns[goID], theIns)
			//	a.updateLockMap(goID, theIns)
			//	a.updateRLockMap(goID, theIns)

			//bz: this should not be pushed -> this just stores a func pointer in parameters (or wrapped in MakeClosure), this func pointer will be invoked later not here
			//case *ssa.Function:
			//	a.updateRecords(access.Name(), goID, "PUSH ", access, theIns)
			//	a.RWIns[goID] = append(a.RWIns[goID], theIns)
			//	a.visitAllInstructions(access, goID)
			//case *ssa.MakeClosure:
			//	a.updateRecords(access.Fn.(*ssa.Function).Name(), goID, "PUSH ", access.Fn.(*ssa.Function), theIns)
			//	a.RWIns[goID] = append(a.RWIns[goID], theIns)
			//	a.visitAllInstructions(access.Fn.(*ssa.Function), goID)
			default:
				continue
			}
		}
		if examIns.Call.StaticCallee().Blocks == nil {
			return
		}
		fnName := examIns.Call.Value.Name()
		fnName = checkTokenName(fnName, theIns)
		a.traverseFn(examIns.Call.StaticCallee(), fnName, goID, theIns)
	} else if examIns.Call.StaticCallee().Pkg != nil && examIns.Call.StaticCallee().Pkg.Pkg.Name() == "sync" {
		switch examIns.Call.Value.Name() {
		case "Range":
			fnName := examIns.Call.Value.Name()
			a.traverseFn(examIns.Call.StaticCallee(), fnName, goID, theIns)
		case "Lock":
			lockLoc := examIns.Call.Args[0] // identifier for address of lock
			if !useNewPTA {
				a.mu.Lock()
				a.ptaCfg0.AddQuery(lockLoc)
				a.mu.Unlock()
			}
			lock := &lockInfo{locAddr: lockLoc, locFreeze: false, parentFn: theIns.Parent(), locBloc: theIns.Block()}
			a.lockSet[goID] = append(a.lockSet[goID], lock)
			log.Trace("Locking   ", lockLoc.String(), "  (", lockLoc.Pos(), ")  lockset now contains: ", lockSetVal(a.lockSet, goID))

		case "Unlock":
			lockLoc := examIns.Call.Args[0]
			if !useNewPTA {
				a.mu.Lock()
				a.ptaCfg0.AddQuery(lockLoc)
				a.mu.Unlock()
			}
			lockOp := a.lockSetContainsAt(a.lockSet, lockLoc, goID) // index of locking operation
			if lockOp != -1 {
				//if a.lockSet[goID][lockOp].parentFn == theIns.Parent() && a.lockSet[goID][lockOp].locBlocInd == theIns.Block().Index { // common block
				//	log.Trace("Unlocking   ", lockLoc.String(), "  (", a.lockSet[goID][lockOp].locAddr.Pos(), ") removing index ", lockOp, " from: ", lockSetVal(a.lockSet, goID))
				//	a.lockSet[goID] = append(a.lockSet[goID][:lockOp], a.lockSet[goID][lockOp+1:]...) // remove from lockset
				//} else {
				//	unlockOps = append(unlockOps, lockLoc)
				//	a.lockSet[goID][lockOp].locFreeze = true
				//}
				if strings.Contains(theIns.Block().Comment, "if") { // unlock located in if statement
					unlockOps = append(unlockOps, lockLoc)
					a.lockSet[goID][lockOp].locFreeze = true
				} else {
					log.Trace("Unlocking   ", lockLoc.String(), "  (", a.lockSet[goID][lockOp].locAddr.Pos(), ") removing index ", lockOp, " from: ", lockSetVal(a.lockSet, goID))
					a.lockSet[goID] = append(a.lockSet[goID][:lockOp], a.lockSet[goID][lockOp+1:]...) // remove from lockset
				}
			}
		case "RLock":
			RlockLoc := examIns.Call.Args[0] // identifier for address of lock
			if !useNewPTA {
				a.mu.Lock()
				a.ptaCfg0.AddQuery(RlockLoc)
				a.mu.Unlock()
			}
			if a.lockSetContainsAt(a.RlockSet, RlockLoc, goID) == -1 { // if lock is not already in active lock-set
				lock := &lockInfo{locAddr: RlockLoc, locFreeze: false}
				a.RlockSet[goID] = append(a.RlockSet[goID], lock)
				log.Trace("RLocking   ", RlockLoc.String(), "  (", RlockLoc.Pos(), ")  Rlockset now contains: ", lockSetVal(a.RlockSet, goID))
			}
		case "RUnlock":
			RlockLoc := examIns.Call.Args[0]
			if !useNewPTA {
				a.mu.Lock()
				a.ptaCfg0.AddQuery(RlockLoc)
				a.mu.Unlock()
			}
			runlockOps = append(runlockOps, RlockLoc)
			if a.lockSetContainsAt(a.RlockSet, RlockLoc, goID) == -1 {
				if a.lockSetContainsAt(a.RlockSet, RlockLoc, a.goCaller[goID]) != -1 {
					a.RlockSet[a.goCaller[goID]][a.lockSetContainsAt(a.RlockSet, RlockLoc, a.goCaller[goID])].locFreeze = true
				}
			} else {
				a.RlockSet[goID][a.lockSetContainsAt(a.RlockSet, RlockLoc, goID)].locFreeze = true
			}
		case "Wait":
			a.recordIns(goID, theIns)
			//a.RWIns[goID] = append(a.RWIns[goID], theIns)
			if !useNewPTA {
				a.mu.Lock()
				a.ptaCfg0.AddQuery(examIns.Call.Args[0])
				a.mu.Unlock()
			}
		case "Done":
			a.recordIns(goID, theIns)
			//a.RWIns[goID] = append(a.RWIns[goID], theIns)
			if !useNewPTA {
				a.mu.Lock()
				a.ptaCfg0.AddQuery(examIns.Call.Args[0])
				a.mu.Unlock()
			}
		}
	} else {
		return
	}
	return unlockOps, runlockOps
}

// insGo analyzes go calls //bz: return new goid
func (a *analysis) insGo(examIns *ssa.Go, goID int, theIns ssa.Instruction, loopID int) int {
	fnName := a.goNames(examIns)
	var entryMethod *ssa.Function
	switch fn := examIns.Call.Value.(type) {
	case *ssa.MakeClosure:
		entryMethod = examIns.Call.StaticCallee()
	case *ssa.TypeAssert:
		switch fn.X.(type) {
		case *ssa.Parameter:
			a.getParam = true
			a.pointerAnalysis(fn.X, goID, theIns)
		}
		if a.paramFunc != nil {
			entryMethod = a.paramFunc
			fnName = entryMethod.Name()
		} else {
			return -1
		}
	default:
		entryMethod = examIns.Call.StaticCallee()
	}
	if entryMethod == nil {
		return -1
	}
	newGoID := goID + 1      // increment goID for child goroutine
	if len(a.workList) > 0 { // spawned by subroutine
		newGoID = a.workList[len(a.workList)-1].goID + 1
	}
	if loopID > 0 {
		a.loopIDs[newGoID] = loopID
	} else {
		a.loopIDs[newGoID] = 0
	}
	a.recordIns(goID, theIns)
	if goID == 0 && a.insMono == -1 { // this is first *ssa.Go instruction in main goroutine
		a.insMono = len(a.RWIns[goID]) // race analysis will begin at this instruction
	}
	var info = goroutineInfo{theIns, examIns, entryMethod, newGoID}
	a.goStack = append(a.goStack, []*fnCallInfo{}) // initialize interior slice
	a.goCaller[newGoID] = goID                       // map caller goroutine
	a.goStack[newGoID] = append(a.goStack[newGoID], a.storeFns...)
	theGoCall := &fnCallInfo{ssaIns: theIns, fnIns: entryMethod}
	a.goStack[newGoID] = append(a.goStack[newGoID], theGoCall)
	a.workList = append(a.workList, info) // store encountered goroutines
	if DEBUG {
		if loopID > 0 {
			log.Debug(strings.Repeat(" ", a.levels[goID]), "spawning Goroutine (in loop) ----->  ", fnName)
		} else {
			log.Debug(strings.Repeat(" ", a.levels[goID]), "spawning Goroutine ----->  ", fnName)
		}
	}

	return newGoID
}

func (a *analysis) insMapUpdate(examIns *ssa.MapUpdate, goID int, theIns ssa.Instruction) {
	a.recordIns(goID, theIns)
	//a.RWIns[goID] = append(a.RWIns[goID], theIns)
	a.updateLockMap(goID, theIns)
	switch ptType := examIns.Map.(type) {
	case *ssa.UnOp:
		if !useNewPTA {
			a.mu.Lock()
			a.ptaCfg0.AddQuery(ptType.X)
			a.mu.Unlock()
		}
	default:
	}
}

func (a *analysis) insSelect(examIns *ssa.Select, goID int, theIns ssa.Instruction) []string {
	a.recordIns(goID, theIns)
	defaultCase := 0
	if !examIns.Blocking {
		defaultCase++
	} // non-blocking select
	readyChans := make([]string, len(examIns.States)+defaultCase) // name of ready channels
	for i, state := range examIns.States {                        // check readiness of each case
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
			a.pointerAnalysis(ch, goID, theIns)
			readyChans[i] = a.chanToken[a.chanName]
			a.selReady[examIns] = append(a.selReady[examIns], readyChans[i])
			if _, ex := a.selUnknown[examIns]; !ex {
				a.selUnknown[examIns] = make([]string, len(examIns.States)+defaultCase)
			}
			if state.Dir == 1 { // send Only
				a.selUnknown[examIns][i] = a.chanName
			} else if state.Dir == 2 { // receive Only
				a.selUnknown[examIns][i] = a.chanName
			} else { // state.Dir == 0, send receive

			}
		case *ssa.TypeAssert: // tests for asserted type

		case *ssa.Call: // timeOut
			readyChans[i] = "timeOut"
			if !examIns.Blocking {
				defaultCase-- // non-blocking due to time-out not default
			}
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
	a.lockMap[theIns] = make([]ssa.Value, len(a.lockSet[goID]))


	for i, l := range a.lockSet[goID] {
		if !l.locFreeze {
			a.lockMap[theIns][i] = l.locAddr
		}
	}
}

func (a *analysis) updateRLockMap(goID int, theIns ssa.Instruction) {
	a.RlockMap[theIns] = make([]ssa.Value, len(a.RlockSet[goID]))
	for i, l := range a.RlockSet[goID] {
		if !l.locFreeze {
			a.RlockMap[theIns][i] = l.locAddr
		}
	}
}


// lockSetContainsAt returns the index of e in s
func (a *analysis) lockSetContainsAt(s map[int][]*lockInfo, e ssa.Value, goID int) int {
	var aPos, bPos token.Pos
	for i, k := range s[goID] {
		var locA, locB ssa.Value
		switch aType := k.locAddr.(type) {
		case *ssa.Global:
			aPos = aType.Pos()
		case *ssa.FieldAddr:
			if aType1, ok1 := aType.X.(*ssa.UnOp); ok1 {
				if aType2, ok2 := aType1.X.(*ssa.FieldAddr); ok2 {
					aPos = aType2.X.Pos()
					locA = aType2.X
				} else {
					aPos = aType1.X.Pos()
					locA = aType1.X
				}
			} else {
				aPos = aType.X.Pos()
				locA = aType.X
			}
		}
		switch eType := e.(type) {
		case *ssa.Global:
			bPos = eType.Pos()
		case *ssa.FieldAddr:
			if eType1, ok2 := eType.X.(*ssa.UnOp); ok2 {
				if eType2, ok3 := eType1.X.(*ssa.FieldAddr); ok3 {
					bPos = eType2.X.Pos()
					locB = eType2.X
				} else {
					bPos = eType1.X.Pos()
					locB = eType1.X
				}
			} else {
				bPos = eType.X.Pos()
				locB = eType.X
			}
		}
		if aPos == bPos {
			return i
		}
		if a.sameAddress(locA, locB, goID, goID) {
			return i
		}
	}
	return -1
}