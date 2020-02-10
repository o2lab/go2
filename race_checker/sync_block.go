package main

import (
	log "github.com/sirupsen/logrus"
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

func (sb *SyncBlock) addAccessInfo(ins *ssa.Instruction, location ssa.Value, index int, comment string) {
	log.Debug("Added", *ins, Analysis.prog.Fset.Position((*ins).Pos()))
	info := accessInfo{
		write:       IsWrite(ins),
		atomic:      false,
		location:    location,
		instruction: ins,
		parent:      sb,
		index:       index,
		comment:     comment,
	}
	sb.accesses = append(sb.accesses, info)
	Analysis.analysisStat.nAccess += 1
	Analysis.ptaConfig.AddQuery(location)
}

func (sb *SyncBlock) GetParentFn() *ssa.Function {
	return sb.bb.Parent()
}
