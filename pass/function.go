package pass

import (
	"fmt"
	"github.com/o2lab/go2/pointer"
	"github.com/o2lab/go2/preprocessor"
	log "github.com/sirupsen/logrus"
	"go/token"
	"golang.org/x/tools/go/ssa"
)

type FnPass struct {
	BlockStates        map[int]*BlockState
	thread             ThreadDomain
	summary            preprocessor.FnSummary
	funcAcquiredValues map[*ssa.Function][]ssa.Value
	stack              CallStack
	escapedValues []ssa.Value
	Visitor            *CFGVisitor
}

type Access struct {
	Instr          ssa.Instruction
	Write          bool
	Addr           ssa.Value
	AcquiredValues []ssa.Value
	Thread         ThreadDomain
	Stack          CallStack
}

type RefState int

const (
	Owned RefState = iota
	Shared
	Inherent
)

type ThreadDomain struct {
	ID        int
	Reflexive bool
}

func (d ThreadDomain) String() string {
	if d.Reflexive {
		return fmt.Sprintf("%d#", d.ID)
	}
	return fmt.Sprintf("%d", d.ID)
}

func NewFnPass(visitor *CFGVisitor, domain ThreadDomain, summary preprocessor.FnSummary,
	funcAcq map[*ssa.Function][]ssa.Value, stack CallStack) *FnPass {
	return &FnPass{
		BlockStates:        make(map[int]*BlockState),
		Visitor:            visitor,
		thread:             domain,
		summary:            summary,
		funcAcquiredValues: funcAcq,
		stack:              stack.Copy(),
	}
}

func (pass *FnPass) GetPointer(value ssa.Value) pointer.Pointer {
	return pass.Visitor.ptaResult.Queries[value]
}

func (pass *FnPass) ThreadDomain() ThreadDomain {
	return pass.thread
}

func (pass *FnPass) Visit(function *ssa.Function) {
	stack := []int{0}
	entryState := NewBlockState(pass)
	for _, param := range function.Params {
		ptr := pass.GetPointer(param)
		entryState.refSet[ptr] = Inherent
	}
	for _, freevar := range function.FreeVars {
		ptr := pass.GetPointer(freevar)
		entryState.refSet[ptr] = Inherent
	}
	pass.BlockStates[0] = entryState
	for len(stack) > 0 {
		index := stack[len(stack)-1]
		block := function.Blocks[index]
		stack = stack[:len(stack)-1]
		log.Debugf("Block %d: %s", block.Index, block.Comment)
		blockState, ok := pass.BlockStates[index]
		if !ok {
			blockState = NewBlockState(pass)
			pass.BlockStates[index] = blockState
		}

		for _, ins := range block.Instrs {
			log.Debugf("  %s", ins)
			blockState.VisitInstruction(ins)
		}
		for _, dominee := range block.Dominees() {
			stack = append(stack, dominee.Index)
			// Inherent the state from parent.
			// TODO: how to deal with releaseOps?
			childState := blockState.copy()
			pass.BlockStates[dominee.Index] = childState
		}
	}
}

func (a *Access) MutualExclusive(b *Access, queries map[ssa.Value]pointer.Pointer) bool {
	for _, acq1 := range a.AcquiredValues {
		for _, acq2 := range b.AcquiredValues {
			if queries[acq1].MayAlias(queries[acq2]) {
				return true
			}
		}
	}
	return false
}

func (a *Access) WriteAndThreadConflictsWith(b *Access) bool {
	return (a.Write || b.Write) && (a.Thread.ID != b.Thread.ID)
}

func (a *Access) MayAlias(b *Access, q map[ssa.Value]pointer.Pointer) bool {
	return q[a.Addr].MayAlias(q[b.Addr])
}

func (a *Access) String() string {
	if a.Write {
		return fmt.Sprintf("Write of %s from %s @T%s", a.Addr, a.Instr, a.Thread)
	}
	return fmt.Sprintf("Read of %s from %s @T%s", a.Addr, a.Instr, a.Thread)
}

func (a *Access) StringWithPos(fset *token.FileSet) string {
	if a.Write {
		return fmt.Sprintf("Write of %s by T%s, Acquired: %+q, %s", a.Addr, a.Thread, a.AcquiredValues, fset.Position(a.Instr.Pos()))
	}
	return fmt.Sprintf("Read of %s by T%s, Acquired: %+q, %s", a.Addr, a.Thread, a.AcquiredValues, fset.Position(a.Instr.Pos()))
}

func PrintStack(stack CallStack) {
	for i := len(stack) - 1; i >= 0; i-- {
		e := stack[i]
		f := e.Callee.Func
		var pos token.Pos
		if e.Site != nil {
			pos = e.Site.Pos()
		} else {
			pos = f.Pos()
		}
		signature := fmt.Sprintf("%s", f.Name())
		log.Infof("    %-14s %v", signature, f.Prog.Fset.Position(pos))
	}
}
