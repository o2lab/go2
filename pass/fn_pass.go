package pass

import (
	"fmt"
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

type FnPass struct {
	blockStates  map[int]*BlockState
	ptaResult    *pointer.Result
	sharedPtrSet map[pointer.Pointer]bool
	accesses     map[pointer.Pointer][]*Access
	thread       ThreadDomain
}

type Access struct {
	Instr          ssa.Instruction
	Write          bool
	Addr           ssa.Value
	AcquiredValues []ssa.Value
	Thread         ThreadDomain
}

type RefState int

const (
	Owned RefState = iota
	Shared
	Inherent
)

type ThreadDomain int

const (
	MainThreadOnly ThreadDomain = iota
	Parallel
)

func NewFnPass(ptaResult *pointer.Result, sharedPtrSet map[pointer.Pointer]bool,
	access map[pointer.Pointer][]*Access, domain ThreadDomain) *FnPass {
	return &FnPass{
		blockStates:  make(map[int]*BlockState),
		ptaResult:    ptaResult,
		sharedPtrSet: sharedPtrSet,
		accesses:     access,
		thread:       domain,
	}
}

func (pass *FnPass) GetPointer(value ssa.Value) pointer.Pointer {
	return pass.ptaResult.Queries[value]
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
	}
	return Owned, false
}

func (pass *FnPass) LiftThreadDomain() {
	pass.thread = Parallel
}

func (pass *FnPass) ThreadDomain() ThreadDomain {
	return pass.thread
}

func (pass *FnPass) Visit(function *ssa.Function) {
	//seen := make(map[int]bool)
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
	pass.blockStates[0] = entryState
	for len(stack) > 0 {
		index := stack[len(stack)-1]
		block := function.Blocks[index]
		stack = stack[:len(stack)-1]
		log.Debugf("Block %d: %s", block.Index, block.Comment)
		blockState, ok := pass.blockStates[index]
		if !ok {
			blockState = NewBlockState(pass)
			pass.blockStates[index] = blockState
		}

		for _, ins := range block.Instrs {
			log.Debugf("  %s", ins)
			blockState.Visit(ins)
		}
		for _, dominee := range block.Dominees() {
			stack = append(stack, dominee.Index)
			// Inherent the state from parent.
			// TODO: how to deal with releaseOps?
			childState := blockState.copy()
			pass.blockStates[dominee.Index] = childState
		}
	}
}

func (bs *BlockState) Visit(instruction ssa.Instruction) {
	switch instr := instruction.(type) {
	case *ssa.Call:
		if mutex := GetLockedMutex(instr.Common()); mutex != nil {
			log.Debugf("Lock on %s", mutex)
			bs.acquireOps[mutex] = true
		} else if mutex := GetUnlockedMutex(instr.Common()); mutex != nil {
			log.Debugf("Unlock on %s", mutex)
			delete(bs.acquireOps, mutex)
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
		escaped := captureEscapedVariables(instr)
		for _, value := range escaped {
			bs.setRefState(value, Shared)
		}
	}
}

func (bs *BlockState) setRefState(value ssa.Value, state RefState) {
	ptr0 := bs.GetPointer(value)
	for ptr1, ref := range bs.refSet {
		if state > ref && ptr0.MayAlias(ptr1) {
			bs.refSet[ptr1] = state
		}
	}
	bs.pass.sharedPtrSet[ptr0] = true
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
	}
	ptr := bs.GetPointer(addr)
	bs.pass.accesses[ptr] = append(bs.pass.accesses[ptr], access)
}

func captureEscapedVariables(goCall *ssa.Go) []ssa.Value {
	common := goCall.Common()
	var escaped []ssa.Value
	if makeClosureIns, ok := common.Value.(*ssa.MakeClosure); ok {
		for _, value := range makeClosureIns.Bindings {
			if pointer.CanPoint(value.Type()) {
				escaped = append(escaped, value)
			}
		}
	} else if common.Method != nil && pointer.CanPoint(common.Value.Type()) {
		escaped = append(escaped, common.Value)
	}

	for _, arg := range common.Args {
		if pointer.CanPoint(arg.Type()) {
			escaped = append(escaped, arg)
		}
	}
	log.Debugf("Escaped: %+q", escaped)
	return escaped
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
	return (a.Write || b.Write) && (a.Thread == Parallel || b.Thread == Parallel)
}

func (a *Access) String() string {
	if a.Write {
		return fmt.Sprintf("Write of %s from %s @T%d", a.Addr, a.Instr, a.Thread)
	}
	return fmt.Sprintf("Read of %s from %s @T%d", a.Addr, a.Instr, a.Thread)
}

func (a *Access) StringWithPos(fset *token.FileSet) string {
	if a.Write {
		return fmt.Sprintf("Write of %s at %s @T%d", a.Addr, fset.Position(a.Instr.Pos()), a.Thread)
	}
	return fmt.Sprintf("Read of %s at %s @T%d", a.Addr, fset.Position(a.Instr.Pos()), a.Thread)
}

func IsSyncFunc(function *ssa.Function, name string) bool {
	if function.Pkg != nil && function.Pkg.Pkg.Path() == "sync" {
		return function.Name() == name
	}
	return false
}

func IsMutexLockFunc(function *ssa.Function) bool {
	return IsSyncFunc(function, "Lock")
}

func IsMutexUnlockFunc(function *ssa.Function) bool {
	return IsSyncFunc(function, "Unlock")
}

func GetLockedMutex(call *ssa.CallCommon) ssa.Value {
	calleeFn := call.StaticCallee()
	if calleeFn == nil {
		return nil
	}
	if IsMutexLockFunc(calleeFn) {
		return call.Args[0]
	}
	return nil
}

func GetUnlockedMutex(call *ssa.CallCommon) ssa.Value {
	calleeFn := call.StaticCallee()
	if calleeFn == nil {
		return nil
	}
	if IsMutexUnlockFunc(calleeFn) {
		return call.Args[0]
	}
	return nil
}
