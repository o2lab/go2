package main

import (
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
	}
	return false
}

// updateRecords will print out the stack trace
func (a *analysis) updateRecords(fnName string, goID int, pushPop string, theFn *ssa.Function) {
	if pushPop == "POP  " {
		a.storeFns = a.storeFns[:len(a.storeFns)-1]
		a.levels[goID]--
	}
	if !allEntries {
		log.Debug(strings.Repeat(" ", a.levels[goID]), pushPop, fnName, " at lvl ", a.levels[goID])
	}
	if pushPop == "PUSH " {
		a.storeFns = append(a.storeFns, theFn)
		a.levels[goID]++
	}
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
	if !isLocalAddr(examIns.Addr) {
		if len(a.storeFns) > 1 {
			if a.storeFns[len(a.storeFns)-2].Name() == "AfterFunc" { // ignore this write instruction as AfterFunc is analyzed elsewhere
				return
			}
		}
		a.RWIns[goID] = append(a.RWIns[goID], theIns)
		a.updateLockMap(goID, theIns)
		if !useNewPTA {
			a.mu.Lock()
			a.ptaCfg0.AddQuery(examIns.Addr)
			a.mu.Unlock()
		}
	}
	if theFunc, storeFn := examIns.Val.(*ssa.Function); storeFn {
		if !a.exploredFunction(theFunc, goID, theIns) {
			a.updateRecords(theFunc.Name(), goID, "PUSH ", theFunc)
			a.RWIns[goID] = append(a.RWIns[goID], theIns)
			a.visitAllInstructions(theFunc, goID)
		}
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
		if v, globVar := examIns.X.(*ssa.Global); globVar {
			if strct, isStruct := v.Type().(*types.Pointer).Elem().Underlying().(*types.Struct); isStruct {
				for i := 0; i < strct.NumFields(); i++ {
					switch strct.Field(i).Type().String() { // requires further testing for when this can be involved in race
					default:
						//log.Debug(strct.Field(i))
					}
				}
				for fnKey, member := range v.Pkg.Members {
					if memberFn, isFn := member.(*ssa.Function); isFn && fnKey != "main" && fnKey != "init" {
						if !a.exploredFunction(memberFn, goID, theIns) {
							a.updateRecords(memberFn.Name(), goID, "PUSH ", memberFn)
							a.visitAllInstructions(memberFn, goID)
						}
					}
				}
			}
		}
		a.RWIns[goID] = append(a.RWIns[goID], theIns)
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
		a.RWIns[goID] = append(a.RWIns[goID], theIns)
	}
}

// insFieldAddr ???
func (a *analysis) insFieldAddr(examIns *ssa.FieldAddr, goID int, theIns ssa.Instruction) {
	if !isLocalAddr(examIns.X) {
		a.RWIns[goID] = append(a.RWIns[goID], theIns)
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
			a.RWIns[goID] = append(a.RWIns[goID], theIns)
			a.updateLockMap(goID, theIns)
			a.updateRLockMap(goID, theIns)
			//a.ptaConfig0.AddQuery(readIns.X)
		}
	case *ssa.Parameter:
		if !isLocalAddr(readIns) {
			a.RWIns[goID] = append(a.RWIns[goID], theIns)
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
		if a.fromPkgsOfInterest(theFn) {
			fnName := mc.Fn.Name()
			if !a.exploredFunction(theFn, goID, theIns) {
				a.updateRecords(fnName, goID, "PUSH ", theFn)
				a.RWIns[goID] = append(a.RWIns[goID], theIns)
				a.updateLockMap(goID, theIns)
				a.updateRLockMap(goID, theIns)
				if !useNewPTA {
					a.mu.Lock()
					a.ptaCfg0.AddQuery(examIns.X)
					a.mu.Unlock()
				}
				a.visitAllInstructions(theFn, goID)
			}
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
			if theVal, ok := examIns.Call.Args[0].(*ssa.UnOp); ok {
				if theVal.Op == token.MUL && !isLocalAddr(theVal.X) {
					a.RWIns[goID] = append(a.RWIns[goID], theIns)
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
	} else if a.fromPkgsOfInterest(examIns.Call.StaticCallee()) && examIns.Call.StaticCallee().Pkg.Pkg.Name() != "sync" { // calling a function
		//if examIns.Call.Value.Name() == "AfterFunc" && examIns.Call.StaticCallee().Pkg.Pkg.Name() == "time" { // calling time.AfterFunc()
		//	a.paramFunc = examIns.Call.Args[1]
		//}
		for _, checkArgs := range examIns.Call.Args {
			switch access := checkArgs.(type) {
			case *ssa.FieldAddr:
				if !isLocalAddr(access.X) && strings.HasPrefix(examIns.Call.Value.Name(), "Add") {
					a.RWIns[goID] = append(a.RWIns[goID], theIns)
					a.updateLockMap(goID, theIns)
					a.updateRLockMap(goID, theIns)
					if !useNewPTA {
						a.mu.Lock()
						a.ptaCfg0.AddQuery(access.X)
						a.mu.Unlock()
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
			a.updateRecords(fnName, goID, "PUSH ", examIns.Call.StaticCallee())
			a.RWIns[goID] = append(a.RWIns[goID], theIns)
			a.visitAllInstructions(examIns.Call.StaticCallee(), goID)
		}
	} else if examIns.Call.StaticCallee().Pkg.Pkg.Name() == "sync" {
		switch examIns.Call.Value.Name() {
		case "Range":
			fnName := examIns.Call.Value.Name()
			if !a.exploredFunction(examIns.Call.StaticCallee(), goID, theIns) {
				a.updateRecords(fnName, goID, "PUSH ", examIns.Call.StaticCallee())
				a.RWIns[goID] = append(a.RWIns[goID], theIns)
				a.visitAllInstructions(examIns.Call.StaticCallee(), goID)
			}
		case "Lock":
			lockLoc := examIns.Call.Args[0]         // identifier for address of lock
			if !useNewPTA {
				a.mu.Lock()
				a.ptaCfg0.AddQuery(lockLoc)
				a.mu.Unlock()
			}
			lock := &lockInfo{locAddr: lockLoc, locFreeze: false, parentFn: theIns.Parent(), locBlocInd: theIns.Block().Index}
			a.lockSet[goID] = append(a.lockSet[goID], lock)
			log.Trace("Locking   ", lockLoc.String(), "  (",  lockLoc.Pos(), ")  lockset now contains: ", lockSetVal(a.lockSet, goID))

		case "Unlock":
			lockLoc := examIns.Call.Args[0]
			if !useNewPTA {
				a.mu.Lock()
				a.ptaCfg0.AddQuery(lockLoc)
				a.mu.Unlock()
			}
			lockOp := a.lockSetContainsAt(a.lockSet, lockLoc, goID) // index of locking operation
			if lockOp != -1 {
				if a.lockSet[goID][lockOp].parentFn == theIns.Parent() && a.lockSet[goID][lockOp].locBlocInd == theIns.Block().Index { // common block
					a.lockSet[goID] = append(a.lockSet[goID][:lockOp], a.lockSet[goID][lockOp+1:]...) // remove from lockset
				} else {
					unlockOps = append(unlockOps, lockLoc)
					a.lockSet[goID][lockOp].locFreeze = true
				}
			}
		case "RLock":
			RlockLoc := examIns.Call.Args[0]          // identifier for address of lock
			if !useNewPTA {
				a.mu.Lock()
				a.ptaCfg0.AddQuery(RlockLoc)
				a.mu.Unlock()
			}
			if a.lockSetContainsAt(a.RlockSet, RlockLoc, goID) == -1 { // if lock is not already in active lock-set
				lock := &lockInfo{locAddr: RlockLoc, locFreeze: false}
				a.RlockSet[goID] = append(a.RlockSet[goID], lock)
				log.Trace("RLocking   ", RlockLoc.String(), "  (",  RlockLoc.Pos(), ")  Rlockset now contains: ", lockSetVal(a.RlockSet, goID))
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
			a.RWIns[goID] = append(a.RWIns[goID], theIns)
			if !useNewPTA {
				a.mu.Lock()
				a.ptaCfg0.AddQuery(examIns.Call.Args[0])
				a.mu.Unlock()
			}
		case "Done":
			a.RWIns[goID] = append(a.RWIns[goID], theIns)
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

// insGo analyzes go calls
func (a *analysis) insGo(examIns *ssa.Go, goID int, theIns ssa.Instruction, loopID int) {
	fnName := a.goNames(examIns)
	newGoID := goID + 1 // increment goID for child goroutine
	if len(a.workList) > 0 { // spawned by subroutine
		newGoID = a.workList[len(a.workList)-1].goID + 1
	}
	if loopID > 0 {
		a.loopIDs[newGoID] = loopID
	} else {
		a.loopIDs[newGoID] = 0
	}
	a.RWIns[goID] = append(a.RWIns[goID], theIns)
	if goID == 0 && a.insDRA == 0 { // this is first *ssa.Go instruction in main goroutine
		a.insDRA = len(a.RWIns[goID]) // race analysis will begin at this instruction
	}

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
		}
	default:
		entryMethod = examIns.Call.StaticCallee()
	}

	var info = goroutineInfo{examIns, entryMethod,newGoID}
	a.goStack = append(a.goStack, []*ssa.Function{}) // initialize interior slice
	a.goCaller[newGoID] = goID                // map caller goroutine
	a.goStack[newGoID] = append(a.goStack[newGoID], a.storeFns...)
	a.workList = append(a.workList, info) // store encountered goroutines
	if !allEntries {
		if loopID > 0 {
			log.Debug(strings.Repeat(" ", a.levels[goID]), "spawning Goroutine (in loop) ----->  ", fnName)
		} else {
			log.Debug(strings.Repeat(" ", a.levels[goID]), "spawning Goroutine ----->  ", fnName)
		}
	}
}

func (a *analysis) insMapUpdate(examIns *ssa.MapUpdate, goID int, theIns ssa.Instruction) {
	a.RWIns[goID] = append(a.RWIns[goID], theIns)
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
			a.pointerAnalysis(ch, goID, theIns)
			readyChans[i] = a.chanToken[a.chanName]
			a.selReady[examIns] = append(a.selReady[examIns], readyChans[i])
			if _, ex := a.selUnknown[examIns]; !ex {
				a.selUnknown[examIns] = make([]string, len(examIns.States) + defaultCase)
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
	for _, l := range a.lockSet[goID] {
		if !l.locFreeze {
			a.lockMap[theIns] = append(a.lockMap[theIns], l.locAddr)
		}
	}
}

func (a *analysis) updateRLockMap(goID int, theIns ssa.Instruction) {
	for _, l := range a.RlockSet[goID] {
		if !l.locFreeze {
			a.RlockMap[theIns] = append(a.RlockMap[theIns], l.locAddr)
		}
	}
}