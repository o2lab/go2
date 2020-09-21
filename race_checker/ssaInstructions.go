package main

import (
	log "github.com/sirupsen/logrus"
	"go/constant"
	"go/token"
	"go/types"
	"golang.org/x/tools/go/ssa"
	"strings"
	"strconv"
)

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

func isReadIns(ins ssa.Instruction) bool { // is the instruction a read access?
	switch insType := ins.(type) {
	case *ssa.UnOp:
		return true
	case *ssa.FieldAddr:
		return true
	case *ssa.Lookup:
		return true
	case *ssa.Call:
		if insType.Call.Value.Name() != "delete" && insType.Call.Value.Name() != "Wait" && insType.Call.Value.Name() != "Done" && !strings.HasPrefix(insType.Call.Value.Name(), "Add") && len(insType.Call.Args) > 0 {
			return true
		}
	default:
		_ = insType
	}
	return false
}

func isWriteIns(ins ssa.Instruction) bool { // is the instruction a write access?
	switch insType := ins.(type) {
	case *ssa.Store:
		return true
	case *ssa.Call:
		if insType.Call.Value.Name() == "delete" {
			return true
		} else if strings.HasPrefix(insType.Call.Value.Name(), "Add") {
			return true
		}
	}
	return false
}

func updateRecords(fnName string, goID int, pushPop string) {
	if pushPop == "POP  " {
		storeIns = storeIns[:len(storeIns)-1]
		levels[goID]--
	}
	log.Debug(strings.Repeat(" ", levels[goID]), pushPop, fnName, " at lvl ", levels[goID])
	if pushPop == "PUSH " {
		storeIns = append(storeIns, fnName)
		levels[goID]++
	}
}

func (a *analysis) insMakeChan(examIns *ssa.MakeChan) {
	var bufferLen int64
	if bufferInfo, ok := examIns.Size.(*ssa.Const); ok { // buffer length passed via constant
		temp, _ := constant.Int64Val(constant.ToInt(bufferInfo.Value))
		bufferLen = temp
	} else if _, ok := examIns.Size.(*ssa.Parameter); ok { // buffer length passed via function parameter
		bufferLen = 10 // TODO: assuming channel can buffer up to 10 values could result in false positives
	}
	if bufferLen < 2 { // unbuffered channel
		chanBufMap[examIns.Name()] = make([]*ssa.Send, 1)
	} else { // buffered channel
		chanBufMap[examIns.Name()] = make([]*ssa.Send, bufferLen)
	}
	insertIndMap[examIns.Name()] = 0 // initialize index
}

func (a *analysis) insSend(examIns *ssa.Send, goID int, theIns ssa.Instruction) {
	var chName string
	if _, ok := chanBufMap[examIns.Chan.Name()]; !ok { // if channel name can't be identified
		a.pointerAnalysis(examIns.Chan, goID, theIns) // identifiable name will be returned by pointer analysis via variable chanName
		chName = chanName
	} else {
		chName = examIns.Chan.Name()
	}
	for chanBufMap[chName][insertIndMap[chName]] != nil && insertIndMap[chName] < len(chanBufMap[chName])-1 {
		insertIndMap[chName]++ // iterate until reaching an index with nil send value stored
	}
	if insertIndMap[chName] == len(chanBufMap[chName])-1 && chanBufMap[chName][insertIndMap[chName]] != nil {
		// buffer length reached, channel will block
		// TODO: use HB graph to handle blocked channel?
	} else {
		chanBufMap[chName][insertIndMap[chName]] = examIns
	}
}

func (a *analysis) insStore(examIns *ssa.Store, goID int, theIns ssa.Instruction) {
	if !isLocalAddr(examIns.Addr) {
		if len(storeIns) > 1 {
			if storeIns[len(storeIns)-2] == "AfterFunc" { // ignore this write instruction as AfterFunc is analyzed elsewhere
				return
			}
		}
		RWIns[goID] = append(RWIns[goID], theIns)
		if len(lockSet) > 0 {
			lockMap[theIns] = lockSet
		}
		if len(chanBufMap) > 0 {
			chanMap[theIns] = []string{}
			for aChan, sSends := range chanBufMap {
				if sSends[0] != nil && len(sSends) == 1 { // slice of channel sends contains exactly one value
					chanMap[theIns] = append(chanMap[theIns], aChan)
				}
			}
		}
		//addrNameMap[examIns.Addr.Name()] = append(addrNameMap[examIns.Addr.Name()], examIns.Addr) // map address name to address, used for checking points-to labels later
		//addrMap[examIns.Addr.Name()] = append(addrMap[examIns.Addr.Name()], RWInsInd{goID: goID, goInd: sliceContainsInsAt(RWIns[goID], theIns)}) // map address name to slice of instructions accessing the same address name
		a.ptaConfig.AddQuery(examIns.Addr)
	}
	if theFunc, storeFn := examIns.Val.(*ssa.Function); storeFn {
		fnName := theFunc.Name()
		if !a.exploredFunction(theFunc, goID, theIns) {
			updateRecords(fnName, goID, "PUSH ")
			RWIns[goID] = append(RWIns[goID], theIns)
			a.visitAllInstructions(theFunc, goID)
		}
	}
}

func (a *analysis) insUnOp(examIns *ssa.UnOp, goID int, theIns ssa.Instruction) {
	if examIns.Op == token.MUL && !isLocalAddr(examIns.X) { // read op
		RWIns[goID] = append(RWIns[goID], theIns)
		if len(lockSet) > 0 {
			lockMap[theIns] = lockSet
		}
		if len(chanBufMap) > 0 {
			chanMap[theIns] = []string{}
			for aChan, sSends := range chanBufMap {
				if sSends[0] != nil && len(sSends) == 1 { // slice of channel sends contains exactly one value
					chanMap[theIns] = append(chanMap[theIns], aChan)
				}
			}
		}
		addrNameMap[examIns.X.Name()] = append(addrNameMap[examIns.X.Name()], examIns.X)
		addrMap[examIns.X.Name()] = append(addrMap[examIns.X.Name()], RWInsInd{goID: goID, goInd: sliceContainsInsAt(RWIns[goID], theIns)})
		a.ptaConfig.AddQuery(examIns.X)
	} else if examIns.Op == token.ARROW { // channel receive op
		var chName string
		if _, ok := chanBufMap[examIns.X.Name()]; !ok { // if channel name can't be identified
			a.pointerAnalysis(examIns.X, goID, theIns)
			chName = chanName
		} else {
			chName = examIns.X.Name()
		}
		for i, aVal := range chanBufMap[chName] {
			if aVal != nil { // channel is not empty
				if len(chanBufMap[chName]) > i+1 {
					chanBufMap[chName][i] = chanBufMap[chName][i+1] // move buffered values one place over
				} else {
					chanBufMap[chName][i] = nil // empty channel upon channel recv
				}
			}
		}
	}
}

func (a *analysis) insFieldAddr(examIns *ssa.FieldAddr, goID int, theIns ssa.Instruction) {
	if !isLocalAddr(examIns.X) {
		RWIns[goID] = append(RWIns[goID], theIns)
		if len(lockSet) > 0 {
			lockMap[theIns] = lockSet
		}
		if len(chanBufMap) > 0 {
			chanMap[theIns] = []string{}
			for aChan, sSends := range chanBufMap {
				if sSends[0] != nil && len(sSends) == 1 { // slice of channel sends contains exactly one value
					chanMap[theIns] = append(chanMap[theIns], aChan)
				}
			}
		}
		//addrNameMap[examIns.X.Name()] = append(addrNameMap[examIns.X.Name()], examIns.X)    // map address name to address, used for checking points-to labels later
		//addrMap[examIns.X.Name()] = append(addrMap[examIns.X.Name()], RWInsInd{goID: goID, goInd: sliceContainsInsAt(RWIns[goID], theIns)}) // map address name to slice of instructions accessing the same address name
		a.ptaConfig.AddQuery(examIns.X)
	}
}

func (a *analysis) insLookUp(examIns *ssa.Lookup, goID int, theIns ssa.Instruction) {
	switch readIns := examIns.X.(type) {
	case *ssa.UnOp:
		if readIns.Op == token.MUL && !isLocalAddr(readIns.X) {
			RWIns[goID] = append(RWIns[goID], theIns)
			if len(lockSet) > 0 {
				lockMap[theIns] = lockSet
			}
			if len(chanBufMap) > 0 {
				chanMap[theIns] = []string{}
				for aChan, sSends := range chanBufMap {
					if sSends[0] != nil && len(sSends) == 1 { // slice of channel sends contains exactly one value
						chanMap[theIns] = append(chanMap[theIns], aChan)
					}
				}
			}
			addrNameMap[readIns.X.Name()] = append(addrNameMap[readIns.X.Name()], readIns.X)
			addrMap[readIns.X.Name()] = append(addrMap[readIns.X.Name()], RWInsInd{goID: goID, goInd: sliceContainsInsAt(RWIns[goID], theIns)})
			a.ptaConfig.AddQuery(readIns.X)
		}
	case *ssa.Parameter:
		if !isLocalAddr(readIns) {
			RWIns[goID] = append(RWIns[goID], theIns)
			if len(lockSet) > 0 {
				lockMap[theIns] = lockSet
			}
			if len(chanBufMap) > 0 {
				chanMap[theIns] = []string{}
				for aChan, sSends := range chanBufMap {
					if sSends[0] != nil && len(sSends) == 1 { // slice of channel sends contains exactly one value
						chanMap[theIns] = append(chanMap[theIns], aChan)
					}
				}
			}
			addrNameMap[readIns.Name()] = append(addrNameMap[readIns.Name()], readIns)
			addrMap[readIns.Name()] = append(addrMap[readIns.Name()], RWInsInd{goID: goID, goInd: sliceContainsInsAt(RWIns[goID], theIns)})
			a.ptaConfig.AddQuery(readIns)
		}
	}
}

func (a *analysis) insChangeType(examIns *ssa.ChangeType, goID int, theIns ssa.Instruction) {
	switch mc := examIns.X.(type) {
	case *ssa.MakeClosure: // yield closure value for *Function and free variable values supplied by Bindings
		theFn := mc.Fn.(*ssa.Function)
		if fromPkgsOfInterest(theFn) {
			fnName := mc.Fn.Name()
			if !a.exploredFunction(theFn, goID, theIns) {
				updateRecords(fnName, goID, "PUSH ")
				RWIns[goID] = append(RWIns[goID], theIns)
				if len(lockSet) > 0 {
					lockMap[theIns] = lockSet
				}
				if len(chanBufMap) > 0 {
					chanMap[theIns] = []string{}
					for aChan, sSends := range chanBufMap {
						if sSends[0] != nil && len(sSends) == 1 { // slice of channel sends contains exactly one value
							chanMap[theIns] = append(chanMap[theIns], aChan)
						}
					}
				}
				a.ptaConfig.AddQuery(examIns.X)
				a.visitAllInstructions(theFn, goID)
			}
		}
	default:
		return
	}
}

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

func (a *analysis) insCall(examIns *ssa.Call, goID int, theIns ssa.Instruction) {
	if examIns.Call.StaticCallee() == nil && examIns.Call.Method == nil {
		if _, ok := examIns.Call.Value.(*ssa.Builtin); !ok {
			a.pointerAnalysis(examIns.Call.Value, goID, theIns)
		} else if examIns.Call.Value.Name() == "delete" { // built-in delete op
			if theVal, ok := examIns.Call.Args[0].(*ssa.UnOp); ok {
				if theVal.Op == token.MUL && !isLocalAddr(theVal.X) {
					RWIns[goID] = append(RWIns[goID], theIns)
					if len(lockSet) > 0 {
						lockMap[theIns] = lockSet
					}
					if len(chanBufMap) > 0 {
						chanMap[theIns] = []string{}
						for aChan, sSends := range chanBufMap {
							if sSends[0] != nil && len(sSends) == 1 { // slice of channel sends contains exactly one value
								chanMap[theIns] = append(chanMap[theIns], aChan)
							}
						}
					}
					addrNameMap[theVal.X.Name()] = append(addrNameMap[theVal.X.Name()], theVal)
					addrMap[theVal.X.Name()] = append(addrMap[theVal.X.Name()], RWInsInd{goID: goID, goInd: sliceContainsInsAt(RWIns[goID], theIns)})
					a.ptaConfig.AddQuery(theVal.X)
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
			paramFunc = examIns.Call.Args[1]
		}
		for _, checkArgs := range examIns.Call.Args {
			switch access := checkArgs.(type) {
			case *ssa.FieldAddr:
				if !isLocalAddr(access.X) && strings.HasPrefix(examIns.Call.Value.Name(), "Add") {
					RWIns[goID] = append(RWIns[goID], theIns)
					if len(lockSet) > 0 {
						lockMap[theIns] = lockSet
					}
					if len(chanBufMap) > 0 {
						chanMap[theIns] = []string{}
						for aChan, sSends := range chanBufMap {
							if sSends[0] != nil && len(sSends) == 1 { // slice of channel sends contains exactly one value
								chanMap[theIns] = append(chanMap[theIns], aChan)
							}
						}
					}
					addrNameMap[access.X.Name()] = append(addrNameMap[access.X.Name()], access.X)                                                     // map address name to address, used for checking points-to labels later
					addrMap[access.X.Name()] = append(addrMap[access.X.Name()], RWInsInd{goID: goID, goInd: sliceContainsInsAt(RWIns[goID], theIns)}) // map address name to slice of instructions accessing the same address name
					a.ptaConfig.AddQuery(access.X)
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
			updateRecords(fnName, goID, "PUSH ")
			RWIns[goID] = append(RWIns[goID], theIns)
			a.visitAllInstructions(examIns.Call.StaticCallee(), goID)
		}
	} else if examIns.Call.StaticCallee().Pkg.Pkg.Name() == "sync" {
		switch examIns.Call.Value.Name() {
		case "Range":
			fnName := examIns.Call.Value.Name()
			if !a.exploredFunction(examIns.Call.StaticCallee(), goID, theIns) {
				updateRecords(fnName, goID, "PUSH ")
				RWIns[goID] = append(RWIns[goID], theIns)
				a.visitAllInstructions(examIns.Call.StaticCallee(), goID)
			}
		case "Lock":
			lockLoc := examIns.Call.Args[0]       // identifier for address of lock
			if !sliceContains(lockSet, lockLoc) { // if lock is not already in active lockset
				lockSet = append(lockSet, lockLoc)
			}
		case "Unlock":
			lockLoc := examIns.Call.Args[0]
			if p := a.lockSetContainsAt(lockSet, lockLoc); p >= 0 {
				lockSet = deleteFromLockSet(lockSet, p)
			}
		case "Wait":
			RWIns[goID] = append(RWIns[goID], theIns)
		case "Done":
			RWIns[goID] = append(RWIns[goID], theIns)
			//case "Add": // adds delta to the WG
			//	fmt.Println("eee")// TODO: handle cases when WG counter is incremented by value greater than 1
		}
	} else {
		return
	}
}

func (a *analysis) insGo(examIns *ssa.Go, goID int, theIns ssa.Instruction) {
	var fnName string
	switch anonFn := examIns.Call.Value.(type) {
	case *ssa.MakeClosure: // go call for anonymous function
		fnName = anonFn.Fn.Name()
	case *ssa.Function:
		fnName = anonFn.Name()
	case *ssa.TypeAssert:
		fnName = paramFunc.(*ssa.MakeClosure).Fn.Name()
	}
	newGoID := goID + 1 // increment goID for child goroutine
	if len(workList) > 0 {
		newGoID = workList[len(workList)-1].goID + 1
	}
	RWIns[goID] = append(RWIns[goID], theIns)
	var info = goroutineInfo{examIns, fnName, newGoID}
	goStack = append(goStack, []string{}) // initialize interior slice
	goCaller[newGoID] = goID              // map caller goroutine
	goStack[newGoID] = append(goStack[newGoID], storeIns...)
	workList = append(workList, info) // store encountered goroutines
	log.Debug(strings.Repeat(" ", levels[goID]), "spawning Goroutine ----->  ", fnName)
}