package main

import (
	log "github.com/sirupsen/logrus"
	"go/token"
	"go/types"
	"golang.org/x/tools/go/ssa"
)

func IsWrite(instr *ssa.Instruction) bool {
	_, write := (*instr).(*ssa.Store)
	return write
}

// SyncMode indicates read/write to shared memory.
type SyncMode int

// Read/write to shared memory is indicated via one of the following acquire/release constants.
const (
	AcqOnly SyncMode = iota
	RelOnly
	AcqRel
)

func (m SyncMode) IsAcq() bool {
	switch m {
	case AcqOnly:
		fallthrough
	case AcqRel:
		return true
	}
	return false
}

func (m SyncMode) IsRel() bool {
	switch m {
	case RelOnly:
		fallthrough
	case AcqRel:
		return true
	}
	return false
}

// chanOp abstracts an ssa.Send, ssa.Unop(ARROW), or a SelectState.
type chanOp struct {
	ch         ssa.Value
	dir        types.ChanDir // SendOnly=send, RecvOnly=recv, SendRecv=close
	pos        token.Pos     // seems not used for now
	syncPred   *SyncBlock
	syncSucc   *SyncBlock
	fromSelect *ssa.Select
}

type wgOp struct {
	wg       ssa.Value
	isWait   bool // Negative isWait indicates "Done" operation
	pos      token.Pos
	syncPred *SyncBlock
	syncSucc *SyncBlock
}

type accessInfo struct {
	write       bool
	atomic      bool
	location    ssa.Value
	instruction *ssa.Instruction
	parent      *SyncBlock
	index       int // index in SyncBlock
	comment     string
}

type InstructionVisitor struct {
	allocated      map[*ssa.Alloc]bool
	sb             *SyncBlock
	parentSummary  *functionSummary
	lastNonEmptySB *SyncBlock
}

func (op chanOp) Mode() SyncMode {
	switch op.dir {
	case types.SendOnly:
		return RelOnly
	case types.RecvOnly:
		return AcqOnly
	}
	return AcqRel
}

func areSendRecvPair(op1, op2 chanOp) bool {
	if op1.dir == types.SendOnly && op2.dir == types.RecvOnly {
		return Analysis.sameAddress(op1.ch, op2.ch)
	}
	return false
}

func areDoneWaitPair(op1, op2 wgOp) bool {
	if !op1.isWait && op2.isWait {
		return Analysis.sameAddress(op1.wg, op2.wg)
	}
	return false
}

func (v *InstructionVisitor) makeSyncBlock(bb *ssa.BasicBlock, index int) *SyncBlock {
	if v.sb.hasAccessOrSyncOp() {
		v.sb.end = index
		v.parentSummary.syncBlocks = append(v.parentSummary.syncBlocks, v.sb)
		if v.lastNonEmptySB != nil {
			v.lastNonEmptySB.succs = []*SyncBlock{v.sb}
			v.sb.preds = []*SyncBlock{v.lastNonEmptySB}
		}
		v.lastNonEmptySB = v.sb
		v.parentSummary.bb2sbList[bb.Index] = append(v.parentSummary.bb2sbList[bb.Index], v.sb)
	}
	v.sb = &SyncBlock{
		start:         index,
		bb:            bb,
		parentSummary: v.parentSummary,
		//fast:          FastSnapshot{mhbChanSend: v.sb.fast.mhbChanSend, mhaChanRecv: v.sb.fast.mhaChanRecv},
		snapshot: SyncSnapshot{lockOpList: make(map[ssa.Value]MutexOp)},
	}
	return v.sb
}

func (v *InstructionVisitor) isLocalAddr(location ssa.Value) bool {
	if location.Pos() == token.NoPos {
		return true
	}
	if locPara, ok := location.(*ssa.Parameter); ok {
		_, ok := locPara.Type().(*types.Pointer)
		return !ok
	}
	switch loc := location.(type) {
	// Ignore checking accesses at alloc sites
	case *ssa.FieldAddr:
		return v.isLocalAddr(loc.X)
	case *ssa.IndexAddr:
		return v.isLocalAddr(loc.X)
	case *ssa.Call:
		if loc.Call.Value.Name() == "append" {
			lastArg := loc.Call.Args[len(loc.Call.Args)-1]
			if locSlice, ok := lastArg.(*ssa.Slice); ok {
				if locAlloc, ok := locSlice.X.(*ssa.Alloc); ok {
					if _, ok := v.allocated[locAlloc]; ok || !locAlloc.Heap || locAlloc.Comment == "complit" {
						return true
					}
				}
			}
		}
	case *ssa.UnOp:
		return v.isLocalAddr(loc.X)
	case *ssa.Alloc:
		if _, ok := v.allocated[loc]; ok || !loc.Heap || loc.Comment == "complit" {
			return true
		}
	}
	return false
}

func (v *InstructionVisitor) visit(instruction ssa.Instruction, bb *ssa.BasicBlock, index int) {
	switch instrT := instruction.(type) {
	case *ssa.Alloc:
		v.allocated[instrT] = true
	case *ssa.UnOp:
		// read by pointer-dereference
		if instrT.Op == token.MUL && !v.isLocalAddr(instrT.X) {
			v.sb.addAccessInfo(&instruction, instrT.X, index, instrT.X.Name())
		} else if instrT.Op == token.ARROW {
			// chan recv, mode Acq
			succ := v.makeSyncBlock(bb, index)
			succ.snapshot.chanRecvOpList = append(succ.snapshot.chanRecvOpList,
				chanOp{ch: instrT.X, dir: types.RecvOnly, pos: instrT.Pos(), syncSucc: succ})
			Analysis.ptaConfig.AddQuery(instrT.X)
		}
	case *ssa.MapUpdate:
		if locMap, ok := instrT.Map.(*ssa.UnOp); ok {
			if !v.isLocalAddr(locMap.X) {
				v.sb.addAccessInfo(&instruction, locMap.X, index, locMap.X.Name())
			}
		}
	case *ssa.Store:
		// write op
		if !v.isLocalAddr(instrT.Addr) {
			v.sb.addAccessInfo(&instruction, instrT.Addr, index, instrT.Addr.Name())
		}
	case *ssa.Go:
		pred := v.sb
		sb := v.makeSyncBlock(bb, index)
		v.parentSummary.sb2GoInsMap[sb] = instrT
		if childSummary, ok := instrT.Call.Value.(*ssa.Function); ok {
			pred.mhbGoFuncList = append(pred.mhbGoFuncList, Analysis.fn2SummaryMap[childSummary])
		}
		for _, arg := range instrT.Call.Args {
			if locAlloc, ok := arg.(*ssa.Alloc); ok {
				delete(v.allocated, locAlloc)
			}
		}
		// binding vars are leaked to instrT.Fn
		if closure, ok := instrT.Call.Value.(*ssa.MakeClosure); ok {
			for _, binding := range closure.Bindings {
				if locAlloc, ok := binding.(*ssa.Alloc); ok {
					delete(v.allocated, locAlloc)
				}
			}
		}
	case *ssa.Send:
		pred := v.sb
		v.makeSyncBlock(bb, index)
		pred.snapshot.chanSendOpList = append(pred.snapshot.chanSendOpList,
			chanOp{ch: instrT.Chan, dir: types.SendOnly, pos: instrT.Pos(), syncPred: pred})
		Analysis.ptaConfig.AddQuery(instrT.Chan)
	case *ssa.Select:
		var pred, succ *SyncBlock
		if instrT.Blocking {
			pred = v.sb
			succ = v.makeSyncBlock(bb, index)
		}
		for _, st := range instrT.States {
			if st.Dir == types.SendOnly {
				v.parentSummary.chSendOps = append(v.parentSummary.chSendOps, chanOp{ch: st.Chan, dir: st.Dir, pos: st.Pos, syncPred: pred, syncSucc: succ, fromSelect: instrT})
			} else if st.Dir == types.RecvOnly {
				v.parentSummary.chRecvOps = append(v.parentSummary.chRecvOps, chanOp{ch: st.Chan, dir: st.Dir, pos: st.Pos, syncPred: pred, syncSucc: succ, fromSelect: instrT})
			}
		}
		v.parentSummary.selectStmts = append(v.parentSummary.selectStmts, instrT)
	case ssa.CallInstruction:
		cc := instrT.Common()
		// chan close
		if b, ok := cc.Value.(*ssa.Builtin); ok {
			if b.Name() == "close" {
				v.makeSyncBlock(bb, index)
				v.parentSummary.chCloseOps = append(v.parentSummary.chCloseOps, chanOp{ch: cc.Args[0], dir: types.SendRecv, pos: cc.Pos()})
			}
		} else if fn, ok := cc.Value.(*ssa.Function); ok && fromPkgsOfInterest(fn) {
			v.sb.fnList = append(v.sb.fnList, fn)
		}
	case *ssa.Call:
		if fn, ok := instrT.Common().Value.(*ssa.Function); ok {
			if fn.Pkg != nil && fn.Pkg.Pkg != nil && fn.Pkg.Pkg.Name() == "sync" {
				if fn.Name() == "Lock" {
					v.makeSyncBlock(bb, index)
					//v.sb.fast.lockCount++
					// FIXME: handle reader locks. Currently all locks are assumed to be writers.
					lockAddr := instrT.Call.Args[0]
					lockOp := MutexOp{loc: lockAddr, write: true, block: v.sb}
					Analysis.ptaConfig.AddQuery(lockAddr)
					v.sb.snapshot.lockOpList[lockAddr] = lockOp
				} else if fn.Name() == "Unlock" {
					v.makeSyncBlock(bb, index)
					//v.sb.fast.lockCount--
					lockAddr := instrT.Call.Args[0]
					delete(v.sb.snapshot.lockOpList, lockAddr)
				} else if fn.Name() == "Wait" {
					succ := v.makeSyncBlock(bb, index)
					v.sb.snapshot.wgWaitList = append(v.sb.snapshot.wgWaitList,
						wgOp{wg: instrT.Call.Args[0], isWait: true, pos: instrT.Pos(), syncSucc: succ})
					Analysis.ptaConfig.AddQuery(instrT.Call.Args[0])
				} else if fn.Name() == "Done" {
					pred := v.sb
					v.makeSyncBlock(bb, index)
					pred.snapshot.wgDoneList = append(pred.snapshot.wgDoneList,
						wgOp{wg: instrT.Call.Args[0], isWait: false, pos: instrT.Pos(), syncPred: pred})
					Analysis.ptaConfig.AddQuery(instrT.Call.Args[0])
				}
			} else if fn.Pkg.Pkg.Name() == "main" {
				recverType := fn.Signature.Recv().Type()
				switch recverType.(type) {
				case *types.Pointer:
					// for method calls with pointer receiver
					if summary, ok := Analysis.fn2SummaryMap[fn]; ok {
						v.sb.mergePreSnapshot(summary.snapshot)
						v.makeSyncBlock(bb, index)
						v.sb.mergePostSnapshot(summary.snapshot)
					} else {
						log.Debug("Summary not found for ", fn)
					}
				}
			} else if closure, ok := instrT.Common().Value.(*ssa.MakeClosure); ok {
				if fn, ok := closure.Fn.(*ssa.Function); ok {
					if summary, ok := Analysis.fn2SummaryMap[fn]; ok {
						//apply callee's summary
						v.sb.mergePreSnapshot(summary.snapshot)
						v.makeSyncBlock(bb, index)
						v.sb.mergePostSnapshot(summary.snapshot)
					} else {
						log.Debug("Summary not found for ", fn)
					}
				}
			}
		}
		//case *ssa.Defer:
		//	signalStr := instrT.Call.Value.String()
		//	if strings.HasSuffix(signalStr, ").Lock") && len(instrT.Call.Args) == 1 {
		//		v.makeSyncBlock(bb, index)
		//	}
		//	if strings.HasSuffix(signalStr, ").Unlock") && len(instrT.Call.Args) == 1 {
		//		v.makeSyncBlock(bb, index)
		//	}
	}
}
