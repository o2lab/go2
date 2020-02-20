package main

import (
	"golang.org/x/tools/go/ssa"
)

type functionSummary struct {
	chSendOps       []chanOp
	chRecvOps       []chanOp
	wgWaitOps       []wgOp
	wgDoneOps       []wgOp
	syncBlocks      []*SyncBlock
	goroutineRank   int
	function        *ssa.Function
	sb2GoInsMap     map[*SyncBlock]*ssa.Go
	chanOpMHBMap    map[*chanOp][]SyncBlock
	bb2sbList       map[int][]*SyncBlock
	fast            FastSnapshot
	snapshot        SyncSnapshot
	selectDoneBlock []*ssa.BasicBlock
	selectStmts     []*ssa.Select
	returnBlocks    []*ssa.BasicBlock
}

func (s *functionSummary) MakePredAndSuccClosure() {
	for _, sb := range s.syncBlocks {
		if len(sb.preds) > 0 {
			sb.preds = append(sb.preds, sb.preds[0].preds...)
		}
		for _, bb := range s.function.Blocks {
			if bb != sb.bb && bb.Dominates(sb.bb) {
				for _, sb_ := range s.bb2sbList[bb.Index] {
					sb.preds = append(sb.preds, sb_)
				}
			}
		}
	}
	for i := len(s.syncBlocks) - 1; i >= 0; i-- {
		sb := s.syncBlocks[i]
		if len(sb.succs) > 0 {
			sb.succs = append(sb.succs, sb.succs[0].succs...)
		}
		for _, bb := range s.function.Blocks {
			if bb != sb.bb && sb.bb.Dominates(bb) {
				for _, sb_ := range s.bb2sbList[bb.Index] {
					sb.succs = append(sb.succs, sb_)
				}
			}
		}
	}
}

func (s *functionSummary) selectChildBlocks(block *ssa.BasicBlock, nStates int) []*ssa.BasicBlock {
	res := make([]*ssa.BasicBlock, 0, nStates)
	for _, bb := range s.function.Blocks[block.Index+1:] {
		if bb.Comment == "select.body" {
			nStates--
			res = append(res, bb)
			if nStates == 0 {
				break
			}
		}
	}
	return res
}
