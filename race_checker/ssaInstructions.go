package main

import (
	"fmt"
	"github.com/o2lab/race-checker/stats"
	log "github.com/sirupsen/logrus"
	"go/constant"
	"go/token"
	"go/types"
	"golang.org/x/tools/go/ssa"
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
		if ins, ok := insType.X.(*ssa.Alloc); ok {
			if _, ok1 := a.selectedChans[ins.Comment]; ok1{
				return false // a channel receive, handled differently than typical read ins
			} else {
				return true
			}
		} else {
			return true
		}
	case *ssa.FieldAddr:
		return true
	case *ssa.Lookup:
		return true
	case *ssa.Call:
		if len(insType.Call.Args) > 0 {
			for _, anArg := range insType.Call.Args {
				if _, ok := anArg.(*ssa.FieldAddr); ok {
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
func (a *analysis) insMakeChan(examIns *ssa.MakeChan) {
	stats.IncStat(stats.NMakeChan)
	var bufferLen int64
	if bufferInfo, ok := examIns.Size.(*ssa.Const); ok { // buffer length passed via constant
		temp, _ := constant.Int64Val(constant.ToInt(bufferInfo.Value))
		bufferLen = temp
	} else if _, ok := examIns.Size.(*ssa.Parameter); ok { // buffer length passed via function parameter
		bufferLen = 10 // TODO: assuming channel can buffer up to 10 values could result in false positives
	}
	if bufferLen < 2 { // unbuffered channel
		a.chanBufMap[examIns.Name()] = make([]*ssa.Send, 1)
	} else { // buffered channel
		a.chanBufMap[examIns.Name()] = make([]*ssa.Send, bufferLen)
	}
	a.insertIndMap[examIns.Name()] = 0 // initialize index
}

// insSend ???
func (a *analysis) insSend(examIns *ssa.Send, goID int, theIns ssa.Instruction) {
	stats.IncStat(stats.NSend)
	a.RWIns[goID] = append(a.RWIns[goID], theIns)
	var chName string
	if _, ok := a.chanBufMap[examIns.Chan.Name()]; !ok { // if channel name can't be identified
		a.pointerAnalysis(examIns.Chan, goID, theIns) // identifiable name will be returned by pointer analysis via variable chanName
		chName = a.chanName
	} else {
		chName = examIns.Chan.Name()
	}
	if len(a.chanBufMap[chName]) > 0 {
		for a.insertIndMap[chName] < len(a.chanBufMap[chName]) && a.chanBufMap[chName][a.insertIndMap[chName]] != nil {
			a.insertIndMap[chName]++ // iterate until reaching an index with nil send value stored
		}
		if a.insertIndMap[chName] == len(a.chanBufMap[chName])-1 && a.chanBufMap[chName][a.insertIndMap[chName]] != nil {
			// buffer length reached, channel will block
			// TODO: use HB graph to handle blocked channel?
		} else if a.insertIndMap[chName] < len(a.chanBufMap[chName]) {
			a.chanBufMap[chName][a.insertIndMap[chName]] = examIns
		}
	}
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
		if len(a.chanBufMap) > 0 {
			a.chanMap[theIns] = []string{}
			for aChan, sSends := range a.chanBufMap {
				if sSends[0] != nil && len(sSends) == 1 { // exactly one instance of channel send up to now
					a.chanMap[theIns] = append(a.chanMap[theIns], aChan) // unbuffered channels create HB among multiple channel sends
				}
			}
		}
		a.ptaConfig.AddQuery(examIns.Addr)
	}
	if theFunc, storeFn := examIns.Val.(*ssa.Function); storeFn {
		fnName := theFunc.Name()
		if !a.exploredFunction(theFunc, goID, theIns) {
			a.updateRecords(fnName, goID, "PUSH ")
			a.RWIns[goID] = append(a.RWIns[goID], theIns)
			a.visitAllInstructions(theFunc, goID)
		}
	}
}

// insUnOp ???
func (a *analysis) insUnOp(examIns *ssa.UnOp, goID int, theIns ssa.Instruction) {
	stats.IncStat(stats.NUnOp)
	if examIns.Op == token.MUL && !isLocalAddr(examIns.X) { // read op
		a.RWIns[goID] = append(a.RWIns[goID], theIns)
		a.updateLockMap(goID, theIns)
		a.updateRLockMap(goID, theIns)
		if len(a.chanBufMap) > 0 {
			a.chanMap[theIns] = []string{}
			for aChan, sSends := range a.chanBufMap {
				if sSends[0] != nil && len(sSends) == 1 { // slice of channel sends contains exactly one value
					a.chanMap[theIns] = append(a.chanMap[theIns], aChan)
				}
			}
		}
		a.ptaConfig.AddQuery(examIns.X)
	} else if examIns.Op == token.ARROW { // channel receive op
		stats.IncStat(stats.NChanRecv)
		var chName string
		if _, ok := a.chanBufMap[examIns.X.Name()]; !ok { // if channel name can't be identified
			a.pointerAnalysis(examIns.X, goID, theIns)
			chName = a.chanName
		} else {
			chName = examIns.X.Name()
		}
		for i, aVal := range a.chanBufMap[chName] {
			if aVal != nil { // channel is not empty
				if len(a.chanBufMap[chName]) > i+1 {
					a.chanBufMap[chName][i] = a.chanBufMap[chName][i+1] // move buffered values one place over
				} else {
					a.chanBufMap[chName][i] = nil // empty channel upon channel recv
				}
			}
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
		if len(a.chanBufMap) > 0 {
			a.chanMap[theIns] = []string{}
			for aChan, sSends := range a.chanBufMap {
				if sSends[0] != nil && len(sSends) == 1 { // slice of channel sends contains exactly one value
					a.chanMap[theIns] = append(a.chanMap[theIns], aChan)
				}
			}
		}
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
			if len(a.chanBufMap) > 0 {
				a.chanMap[theIns] = []string{}
				for aChan, sSends := range a.chanBufMap {
					if sSends[0] != nil && len(sSends) == 1 { // slice of channel sends contains exactly one value
						a.chanMap[theIns] = append(a.chanMap[theIns], aChan)
					}
				}
			}
			a.ptaConfig.AddQuery(readIns.X)
		}
	case *ssa.Parameter:
		if !isLocalAddr(readIns) {
			a.RWIns[goID] = append(a.RWIns[goID], theIns)
			a.updateLockMap(goID, theIns)
			a.updateRLockMap(goID, theIns)
			if len(a.chanBufMap) > 0 {
				a.chanMap[theIns] = []string{}
				for aChan, sSends := range a.chanBufMap {
					if sSends[0] != nil && len(sSends) == 1 { // slice of channel sends contains exactly one value
						a.chanMap[theIns] = append(a.chanMap[theIns], aChan)
					}
				}
			}
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
				if len(a.chanBufMap) > 0 {
					a.chanMap[theIns] = []string{}
					for aChan, sSends := range a.chanBufMap {
						if sSends[0] != nil && len(sSends) == 1 { // slice of channel sends contains exactly one value
							a.chanMap[theIns] = append(a.chanMap[theIns], aChan)
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
func (a *analysis) insCall(examIns *ssa.Call, goID int, theIns ssa.Instruction) {
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
					if len(a.chanBufMap) > 0 {
						a.chanMap[theIns] = []string{}
						for aChan, sSends := range a.chanBufMap {
							if sSends[0] != nil && len(sSends) == 1 { // slice of channel sends contains exactly one value
								a.chanMap[theIns] = append(a.chanMap[theIns], aChan)
							}
						}
					}
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
			a.paramFunc = examIns.Call.Args[1]
		}
		for _, checkArgs := range examIns.Call.Args {
			switch access := checkArgs.(type) {
			case *ssa.FieldAddr:
				if !isLocalAddr(access.X) && strings.HasPrefix(examIns.Call.Value.Name(), "Add") {
					a.RWIns[goID] = append(a.RWIns[goID], theIns)
					a.updateLockMap(goID, theIns)
					a.updateRLockMap(goID, theIns)
					if len(a.chanBufMap) > 0 {
						a.chanMap[theIns] = []string{}
						for aChan, sSends := range a.chanBufMap {
							if sSends[0] != nil && len(sSends) == 1 { // slice of channel sends contains exactly one value
								a.chanMap[theIns] = append(a.chanMap[theIns], aChan)
							}
						}
					}
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
			a.ptaConfig.AddQuery(lockLoc)
			if lockLoc.String() == "&t21.mu [#7]" || lockLoc.String() == "&t22.mu [#7]" || lockLoc.String() == "&cs.mu [#16]" { // lock and unlock pair located within same if.then block
				fmt.Println("need to catch this lock")
			} else {
				if goID == 0 { // main goroutine
					if !sliceContains(a.lockSet, lockLoc) && !a.mapFreeze { // if lock is not already in active lockset
						if lockLoc.String() == "&t21.mu [#7]" {
							fmt.Println(lockLoc.String(), " <-- this lock needs investigation")
						} else {
							a.lockSet = append(a.lockSet, lockLoc)
							log.Trace("Locking   ", lockLoc.String(), "  (",  lockLoc.Pos(), ")  lockset now contains: ", lockSetVal(a.lockSet))
						}
					}
				} else { // worker goroutine
					if !sliceContains(a.goLockset[goID], lockLoc) && !a.mapFreeze { // if lock is not already in active lockset
						a.goLockset[goID] = append(a.goLockset[goID], lockLoc)
						log.Trace("Locking   ", lockLoc.String(), "  (",  lockLoc.Pos(), ")  lockset now contains: ", lockSetVal(a.goLockset[goID]))
					}
				}
			}
		case "Unlock":
			stats.IncStat(stats.NUnlock)
			lockLoc := examIns.Call.Args[0]
			a.ptaConfig.AddQuery(lockLoc)
			if goID == 0 { // main goroutine
				if p := a.lockSetContainsAt(a.lockSet, lockLoc); p >= 0 {
					if examIns.Block().Comment != "if.then" { // remove from active lock-set
						log.Trace("Unlocking ", lockLoc.String(), "  (", lockLoc.Pos(), ") removing index ", p, " from: ", lockSetVal(a.lockSet))
						a.lockSet = a.deleteFromLockSet(a.lockSet, p)
					} else { // do NOT remove from active lock-set yet
						a.mapFreeze = true
					}
				}
			} else { // worker goroutine
				if p := a.lockSetContainsAt(a.goLockset[goID], lockLoc); p >= 0 {
					if examIns.Block().Comment != "if.then" { // remove from active lock-set
						log.Trace("Unlocking ", lockLoc.String(), "  (", lockLoc.Pos(), ") removing index ", p, " from: ", lockSetVal(a.goLockset[goID]))
						a.goLockset[goID] = a.deleteFromLockSet(a.goLockset[goID], p)
					} else { // do NOT remove from active lock-set yet
						a.mapFreeze = true
					}
				}
			}
		case "RLock":
			RlockLoc := examIns.Call.Args[0]          // identifier for address of lock
			a.ptaConfig.AddQuery(RlockLoc)
			if goID == 0 {
				if !sliceContains(a.RlockSet, RlockLoc) { // if lock is not already in active lock-set
					a.RlockSet = append(a.RlockSet, RlockLoc)
					log.Trace("RLocking   ", RlockLoc.String(), "  (",  RlockLoc.Pos(), ")  Rlockset now contains: ", lockSetVal(a.RlockSet))
				}
			} else {
				if !sliceContains(a.RlockSet, RlockLoc) { // if lock is not already in active lock-set
					a.goRLockset[goID] = append(a.goRLockset[goID], RlockLoc)
					log.Trace("RLocking   ", RlockLoc.String(), "  (",  RlockLoc.Pos(), ")  Rlockset now contains: ", lockSetVal(a.goRLockset[goID]))
				}
			}
		case "RUnlock":
			RlockLoc := examIns.Call.Args[0]
			a.ptaConfig.AddQuery(RlockLoc)
			if goID == 0 {
				if p := a.lockSetContainsAt(a.RlockSet, RlockLoc); p >= 0 {
					log.Trace("RUnlocking ", RlockLoc.String(), "  (", RlockLoc.Pos(), ") removing index ", p, " from: ", lockSetVal(a.RlockSet))
					a.RlockSet = a.deleteFromLockSet(a.RlockSet, p)
				}
			} else {
				if p := a.lockSetContainsAt(a.goRLockset[goID], RlockLoc); p >= 0 {
					log.Trace("RUnlocking ", RlockLoc.String(), "  (", RlockLoc.Pos(), ") removing index ", p, " from: ", lockSetVal(a.goRLockset[goID]))
					a.goRLockset[goID] = a.deleteFromLockSet(a.goRLockset[goID], p)
				}
			}
		case "Wait":
			stats.IncStat(stats.NWaitGroupWait)
			a.RWIns[goID] = append(a.RWIns[goID], theIns)
		case "Done":
			stats.IncStat(stats.NWaitGroupDone)
			a.RWIns[goID] = append(a.RWIns[goID], theIns)
		}
	} else {
		return
	}
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
	a.goStack = append(a.goStack, []string{}) // initialize interior slice
	a.goCaller[newGoID] = goID                // map caller goroutine
	a.goStack[newGoID] = append(a.goStack[newGoID], a.storeIns...)
	a.workList = append(a.workList, info) // store encountered goroutines
	log.Debug(strings.Repeat(" ", a.levels[goID]), "spawning Goroutine ----->  ", fnName)
	if goID == 0 { // this is a child spawned by main goroutine and there are currently active locks
		a.goLockset[newGoID] = append(a.goLockset[newGoID], a.lockSet...) // inherit active lockset from parent goroutine
	} else {
		a.goLockset[newGoID] = append(a.goLockset[newGoID], a.goLockset[goID]...) // inherit from parent goroutine
	}
}

func (a *analysis) insMapUpdate(examIns *ssa.MapUpdate, goID int, theIns ssa.Instruction) {
	stats.IncStat(stats.NStore)
	a.RWIns[goID] = append(a.RWIns[goID], theIns)
	a.updateLockMap(goID, theIns)
	if len(a.chanBufMap) > 0 {
		a.chanMap[theIns] = []string{}
		for aChan, sSends := range a.chanBufMap {
			if sSends[0] != nil && len(sSends) == 1 { // slice of channel sends contains exactly one value
				a.chanMap[theIns] = append(a.chanMap[theIns], aChan)
			}
		}
	}
	switch ptType := examIns.Map.(type) {
	case *ssa.UnOp:
		a.ptaConfig.AddQuery(ptType.X)
	default:
	}
}

func (a *analysis) insSelect(examIns *ssa.Select, goID int, theIns ssa.Instruction) ([]int, []string) {
	a.RWIns[goID] = append(a.RWIns[goID], theIns)
	caseStatus := make([]int, len(examIns.States))
	readyChans := []string{}
	for i, states := range examIns.States {
		if rcv, ok := states.Chan.(*ssa.UnOp); ok { // value available in channel receive
			if recv, ok := rcv.X.(*ssa.Alloc); ok {
				a.selectedChans[recv.Comment] = theIns // space holder for map value, will be replaced with last instruction in clause
				readyChans = append(readyChans, recv.Comment)
			}
			caseStatus[i] = 1
		}
	}
	return caseStatus, readyChans
}

func (a *analysis) updateLockMap(goID int, theIns ssa.Instruction) {
	if goID == 0 { // main goroutine
		if len(a.lockSet) > 0 && !a.mapFreeze {
			a.lockMap[theIns] = append(a.lockMap[theIns], a.lockSet...)
		}
	} else { // worker goroutine
		if len(a.goLockset[goID]) > 0 && !a.mapFreeze {
			a.lockMap[theIns] = append(a.lockMap[theIns], a.goLockset[goID]...)
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