package main

import (
	log "github.com/sirupsen/logrus"
	"go/token"
	"go/types"
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

// if callee contains incoming sync edges, merge with instructions after method call
func (b *SyncBlock) mergePostSnapshot(calleeSnapshot SyncSnapshot) {
	if len(calleeSnapshot.wgWaitList) > 0 {
		b.snapshot.wgWaitList = calleeSnapshot.wgWaitList
	}
	if len(calleeSnapshot.chanRecvOpList) > 0 {
		b.snapshot.chanRecvOpList = append(b.snapshot.chanRecvOpList, calleeSnapshot.chanRecvOpList...)
		b.parentSummary.chRecvOps = append(b.parentSummary.chRecvOps, chanOp{dir: types.RecvOnly, pos: token.NoPos, syncSucc: b})
	}
}

// if callee contains outgoing sync edges, merge with instructions before method call
func (b *SyncBlock) mergePreSnapshot(calleeSnapshot SyncSnapshot) {
	if len(calleeSnapshot.wgDoneList) > 0 {
		b.snapshot.wgDoneList = calleeSnapshot.wgDoneList
	}
	if len(calleeSnapshot.chanSendOpList) > 0 {
		b.snapshot.chanSendOpList = append(b.snapshot.chanSendOpList, calleeSnapshot.chanSendOpList...)
		b.parentSummary.chSendOps = append(b.parentSummary.chSendOps, chanOp{dir: types.SendOnly, pos: token.NoPos, syncPred: b})
	}
}

// whether or not the sync block has synchronization primitives
func (s *SyncSnapshot) hasSyncOp() bool {
	return len(s.lockOpList) > 0 || len(s.chanRecvOpList) > 0 || len(s.chanSendOpList) > 0 || len(s.wgDoneList) > 0 || len(s.wgWaitList) > 0
}

func (b *SyncBlock) hasAccessOrSyncOp() bool {
	return len(b.accesses) > 0 || b.snapshot.hasSyncOp()
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
