package main

import (
	log "github.com/sirupsen/logrus"
	"go/token"
	"go/types"
	"golang.org/x/tools/go/ssa"
)

type ChanSendDomain int
type ChanRecvDomain int

const (
	NoSend ChanSendDomain = iota
	NondetSend
	SingleSend
	NoRecv ChanRecvDomain = iota
	NondetRecv
	SingleRecv
)

type SyncBlock struct {
	bb            *ssa.BasicBlock
	parentSummary *functionSummary
	//instruction *ssa.Instruction
	start, end    int // start and end index in bb, excluding the instruction at index end
	fnList        []*ssa.Function
	accesses      []accessInfo
	mhbGoFuncList []*ssa.Function // list of functions that spawned after the syncBlock by Go
	preds         []*SyncBlock
	succs         []*SyncBlock
	snapshot      SyncSnapshot
}

// abstract domains
type SyncSnapshot struct {
	lockCount   int
	mhbWGDone   bool
	mhaWGWait   bool
	mhbChanSend ChanSendDomain
	mhaChanRecv ChanRecvDomain
}

func (s SyncSnapshot) hasSyncSideEffect() bool {
	return s.lockCount > 0 || s.mhaWGWait || s.mhbWGDone || s.mhbChanSend > NoSend || s.mhaChanRecv > NoRecv
}

func (b *SyncBlock) mergePostSnapshot(snapshot SyncSnapshot) {
	if snapshot.mhaWGWait {
		b.snapshot.mhaWGWait = true
	}
	if snapshot.mhaChanRecv > b.snapshot.mhaChanRecv {
		b.snapshot.mhaChanRecv = snapshot.mhaChanRecv
		if b.snapshot.mhaChanRecv == SingleRecv {
			b.parentSummary.chRecvOps = append(b.parentSummary.chRecvOps,
				chanOp{dir: types.RecvOnly, pos: token.NoPos, syncSucc: b})
		}
	}
	b.snapshot.lockCount += snapshot.lockCount
}

func (b *SyncBlock) mergePreSnapshot(snapshot SyncSnapshot) {
	if snapshot.mhbWGDone {
		b.snapshot.mhbWGDone = true
	}
	if snapshot.mhbChanSend > b.snapshot.mhbChanSend {
		b.snapshot.mhbChanSend = snapshot.mhbChanSend
		if b.snapshot.mhbChanSend == SingleSend {
			b.parentSummary.chSendOps = append(b.parentSummary.chSendOps,
				chanOp{dir: types.SendOnly, pos: token.NoPos, syncPred: b})
		}
	}
}

func (b *SyncBlock) hasAccessOrSyncOp() bool {
	return len(b.accesses) > 0 || b.snapshot.hasSyncSideEffect()
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
