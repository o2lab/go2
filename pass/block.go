package pass

import (
	"github.com/o2lab/go2/preprocessor"
	log "github.com/sirupsen/logrus"
	"go/token"
	"golang.org/x/tools/go/pointer"
	"golang.org/x/tools/go/ssa"
)

type BlockState struct {
	pass       *FnPass
	refSet     map[pointer.Pointer]RefState
	acquireOps map[ssa.Value]bool
	releaseOps map[ssa.Value]bool
}

func (bs *BlockState) GetPointer(value ssa.Value) pointer.Pointer {
	return bs.pass.GetPointer(value)
}

func (bs *BlockState) GetRefState(value ssa.Value) (RefState, bool) {
	ptr := bs.GetPointer(value)
	if owned, ok := bs.refSet[ptr]; ok {
		return owned, true
	}
	if x, ok := value.(*ssa.FieldAddr); ok {
		return bs.GetRefState(x.X)
	} else {
		for p, owned := range bs.refSet {
			if ptr.MayAlias(p) {
				return owned, true
			}
		}
	}
	return Owned, false
}

func (bs *BlockState) mergeEscapedValues(function *ssa.Function) {
	for _, value := range bs.pass.Visitor.passes[function].escapedValues {
		bs.setRefState(value, Shared)
		bs.pass.escapedValues = append(bs.pass.escapedValues, value)
	}
}

func (bs *BlockState) acquireSyncOnFunc(function *ssa.Function) {
	for value, _ := range bs.acquireOps {
		bs.pass.funcAcquiredValues[function] = append(bs.pass.funcAcquiredValues[function], value)
	}
}

func (bs *BlockState) VisitInstruction(instruction ssa.Instruction) {
	switch instr := instruction.(type) {
	case *ssa.Call:
		if mutex := preprocessor.GetLockedMutex(instr.Common()); mutex != nil {
			log.Debugf("Lock on %s", mutex)
			bs.acquireOps[mutex] = true
		} else if mutex := preprocessor.GetUnlockedMutex(instr.Common()); mutex != nil {
			log.Debugf("Unlock on %s", mutex)
			delete(bs.acquireOps, mutex)
		} else {
			fun := GetFunctionFromCall(instr)
			if fun != nil {
				bs.acquireSyncOnFunc(fun)
				bs.mergeEscapedValues(fun)
			}
		}
	case *ssa.Alloc:
		if instr.Heap {
			ptr := bs.GetPointer(instr)
			bs.refSet[ptr] = Owned
		}
	case *ssa.UnOp:
		if instr.Op == token.MUL {
			ref, ok := bs.GetRefState(instr.X)
			if ok && ref != Owned {
				bs.makeAccess(instr, instr.X, false)
			}
		}
	case *ssa.Store:
		ref, ok := bs.GetRefState(instr.Addr)
		if ok && ref != Owned {
			bs.makeAccess(instr, instr.Addr, true)
		}
	case *ssa.Go:
		bs.pass.Visitor.goStacks[instr] = bs.pass.stack.Copy()
		for _, value := range bs.pass.Visitor.escapedValues[instr] {
			bs.setRefState(value, Shared)
			bs.addEscapeSite(value)
		}
	}
}

func (bs *BlockState) addEscapeSite(value ssa.Value) {
	bs.pass.escapedValues = append(bs.pass.escapedValues, value)

	ptr := bs.GetPointer(value)
	site := &EscapeSite{
		value: value,
		ptr:   ptr,
		stack: bs.pass.stack,
	}
	bs.pass.Visitor.escapeSites = append(bs.pass.Visitor.escapeSites, site)
}

func (bs *BlockState) setRefState(value ssa.Value, state RefState) {
	ptr0 := bs.GetPointer(value)
	for ptr1, ref := range bs.refSet {
		if state > ref && ptr0.MayAlias(ptr1) {
			bs.refSet[ptr1] = state
		}
	}
	bs.pass.Visitor.sharedPtrSet[ptr0] = true
	//for addr, _ := range bs.pass.summary.AccessSet {
	//	if fieldX, ok := addr.(*ssa.FieldAddr); ok {
	//		log.Infof("access of %s at %s", fieldX, fieldX.Pos())
	//		if bs.GetPointer(fieldX.X).MayAlias(ptr0) {
	//			bs.setRefState(fieldX.X, state)
	//		}
	//	}
	//}
}

func GetFunctionFromCall(call *ssa.Call) *ssa.Function {
	if fun := call.Common().StaticCallee(); fun != nil {
		return fun
	} else if method, ok := call.Common().Value.(*ssa.Function); ok {
		return method
	}
	return nil
}

func NewBlockState(pass *FnPass) *BlockState {
	return &BlockState{
		refSet:     make(map[pointer.Pointer]RefState),
		pass:       pass,
		acquireOps: make(map[ssa.Value]bool),
	}
}

func (bs *BlockState) copy() *BlockState {
	block := NewBlockState(bs.pass)
	for value, ref := range bs.refSet {
		block.refSet[value] = ref
	}
	for value, _ := range bs.acquireOps {
		block.acquireOps[value] = true
	}
	return block
}

func (bs *BlockState) makeAccess(instruction ssa.Instruction, addr ssa.Value, write bool) {
	var acquired []ssa.Value
	for value := range bs.acquireOps {
		acquired = append(acquired, value)
	}
	access := &Access{
		Instr:          instruction,
		Write:          write,
		Addr:           addr,
		AcquiredValues: acquired,
		Thread:         bs.pass.thread,
		Stack:          bs.pass.stack,
	}
	ptr := bs.GetPointer(addr)
	bs.addAccessBySite(ptr, access)
}

func (bs *BlockState) addAccessBySite(ptr pointer.Pointer, access *Access)  {
	for _, site := range bs.pass.Visitor.escapeSites {
		if ptr.MayAlias(site.ptr) {
			if access.Write {
				bs.pass.Visitor.writes[site] = append(bs.pass.Visitor.writes[site], access)
			} else {
				bs.pass.Visitor.reads[site] = append(bs.pass.Visitor.reads[site], access)
			}
		}
	}
}

