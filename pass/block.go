package pass

import (
	"github.com/o2lab/go2/pointer"
	"github.com/o2lab/go2/preprocessor"
	log "github.com/sirupsen/logrus"
	"go/token"
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

func (bs *BlockState) mergeAccesses(calleePass *FnPass, crossThread bool) {
	for point, accesses := range calleePass.accessMeta {
		for _, access := range accesses {
			newAcc := *access
			newAcc.CrossThread = newAcc.CrossThread || crossThread
			// TODO: extend stack by the callsite
			//newAcc.Stack = append(CallStack{callee}, newAcc.Stack)
			bs.appendAcquiredValuesToAccess(&newAcc)
			bs.checkAndStoreAccess(point, &newAcc)
		}
	}
}

func (bs *BlockState) mergeEscapedValues(pass *FnPass) {
	for _, value := range pass.escapedValues {
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
			fun := GetFunctionFromCallCommon(instr.Common())
			if calleePass, ok := bs.pass.Visitor.passes[fun]; ok && fun != nil {
				// The order below is important.
				bs.mergeAccesses(calleePass, false)
				bs.mergeEscapedValues(calleePass)
				bs.acquireSyncOnFunc(fun)
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
		if fun := GetFunctionFromCallCommon(instr.Common()); fun != nil {
			if calleePass, ok := bs.pass.Visitor.passes[fun]; ok {
				bs.mergeAccesses(calleePass, true)
			}
		}
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

func GetFunctionFromCallCommon(common *ssa.CallCommon) *ssa.Function {
	if fun := common.StaticCallee(); fun != nil {
		return fun
	} else if method, ok := common.Value.(*ssa.Function); ok {
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
	ptr := bs.GetPointer(addr)
	aps := ptr.AccessPointSet()
	if aps == nil {
		return
	}

	access := &Access{
		Instr:          instruction,
		Write:          write,
		Addr:           addr,
		AccessPoints:   aps,
		Thread:         bs.pass.thread,
		Stack:          nil,
	}
	bs.appendAcquiredValuesToAccess(access)
	bs.storeAccessMetadata(aps, access)
}

func (bs *BlockState) appendAcquiredValuesToAccess(access *Access) {
	for value := range bs.acquireOps {
		access.AcquiredValues = append(access.AcquiredValues, value)
	}
}

func (bs *BlockState) storeAccessMetadata(set *pointer.AccessPointSet, access *Access) {
	for _, point := range set.ToSlice() {
		bs.checkAndStoreAccess(point, access)
	}
}

func (bs *BlockState) checkAndStoreAccess(point pointer.AccessPointId, access *Access) {
	for _, acc := range bs.pass.accessMeta[point] {
		if acc.CrossThread && !bs.MutualExclusive(access, acc) && access.WriteConflictsWith(acc) {
			bs.ReportRace(access, acc)
		}
	}
	bs.pass.accessMeta[point] = append(bs.pass.accessMeta[point], access)
}

func (bs *BlockState) MutualExclusive(a1, a2 *Access) bool {
	queries := bs.pass.Visitor.ptaResult.Queries
	for _, acq1 := range a1.AcquiredValues {
		for _, acq2 := range a2.AcquiredValues {
			if queries[acq1].MayAlias(queries[acq2]) {
				return true
			}
		}
	}
	return false
}

func (bs *BlockState) ReportRace(a1, a2 *Access) {
	fset := bs.pass.Visitor.program.Fset
	log.Println("========== DATA RACE ==========")
	log.Printf("  %s", a1.StringWithPos(fset))
	log.Println("  Call stack:")
	PrintStack(bs.pass.stack)
	log.Printf("  %s", a2.StringWithPos(fset))
	log.Println("  Call stack:")
	PrintStack(a2.Stack)
	log.Println("===============================")
}
