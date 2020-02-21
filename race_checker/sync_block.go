package main

import (
	log "github.com/sirupsen/logrus"
	"golang.org/x/tools/go/ssa"
)

type ChanSendDomain int
type ChanRecvDomain int

//const (
//	NoSend ChanSendDomain = iota
//	NondetSend
//	SingleSend
//	NoRecv ChanRecvDomain = iota
//	NondetRecv
//	SingleRecv
//)

type SyncBlock struct {
	bb            *ssa.BasicBlock
	parentSummary *functionSummary
	//instruction *ssa.Instruction
	start, end    int // start and end index in bb, excluding the instruction at index end
	fnList        []*ssa.Function
	accesses      []accessInfo
	mhbGoFuncList []*functionSummary // list of functions that spawned after the syncBlock by Go
	preds         []*SyncBlock
	succs         []*SyncBlock
	//	fast          FastSnapshot
	snapshot SyncSnapshot
}

type MutexOp struct {
	loc   ssa.Value
	write bool
	block *SyncBlock
}

// precise domains for sync operations
type SyncSnapshot struct {
	lockOpList     map[ssa.Value]MutexOp
	chanSendOpList []chanOp
	chanRecvOpList []chanOp
	wgWaitList     []wgOp
	wgDoneList     []wgOp
}

// abstract domains (under-approximation)
//type FastSnapshot struct {
//	lockCount   int
//	mhbWGDone   bool
//	mhaWGWait   bool
//	mhbChanSend ChanSendDomain
//	mhaChanRecv ChanRecvDomain
//}

//func (s FastSnapshot) hasSyncSideEffect() bool {
//	return s.lockCount > 0 || s.mhaWGWait || s.mhbWGDone || s.mhbChanSend > NoSend || s.mhaChanRecv > NoRecv
//}
//
//func (b *SyncBlock) mergePostSnapshot(fast FastSnapshot) {
//	if fast.mhaWGWait {
//		b.fast.mhaWGWait = true
//	}
//	if fast.mhaChanRecv > b.fast.mhaChanRecv {
//		b.fast.mhaChanRecv = fast.mhaChanRecv
//		if b.fast.mhaChanRecv == SingleRecv {
//			b.parentSummary.chRecvOps = append(b.parentSummary.chRecvOps,
//				chanOp{dir: types.RecvOnly, pos: token.NoPos, syncSucc: b})
//		}
//	}
//	b.fast.lockCount += fast.lockCount
//}
//
//func (b *SyncBlock) mergePreSnapshot(fast FastSnapshot) {
//	if fast.mhbWGDone {
//		b.fast.mhbWGDone = true
//	}
//	if fast.mhbChanSend > b.fast.mhbChanSend {
//		b.fast.mhbChanSend = fast.mhbChanSend
//		if b.fast.mhbChanSend == SingleSend {
//			b.parentSummary.chSendOps = append(b.parentSummary.chSendOps,
//				chanOp{dir: types.SendOnly, pos: token.NoPos, syncPred: b})
//		}
//	}
//}

func (b *SyncBlock) hasAccessOrSyncOp() bool {
	return len(b.accesses) > 0 // || b.fast.hasSyncSideEffect()
}

func (b *SyncBlock) addAccessInfo(ins *ssa.Instruction, location ssa.Value, index int, comment string) {
	log.Debug("Added", *ins, Analysis.prog.Fset.Position((*ins).Pos()))
	info := accessInfo{
		write:       IsWrite(ins),
		atomic:      false,
		location:    location,
		instruction: ins,
		parent:      b,
		index:       index,
		comment:     comment,
	}
	b.accesses = append(b.accesses, info)
	Analysis.analysisStat.nAccess += 1
	Analysis.ptaConfig.AddQuery(location)
}

func (b *SyncBlock) GetParentFn() *ssa.Function {
	return b.bb.Parent()
}

func IsReturnBlock(block *ssa.BasicBlock) bool {
	lastInstr := block.Instrs[len(block.Instrs)-1]
	_, ok := lastInstr.(*ssa.Return)
	return ok
}
