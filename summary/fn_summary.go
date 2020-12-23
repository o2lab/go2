package summary

import (
	log "github.com/sirupsen/logrus"
	"go/token"
	"golang.org/x/tools/go/pointer"
	"golang.org/x/tools/go/ssa"
)

type FnSummary struct {
	fset      *token.FileSet
	AccessSet map[ssa.Value]IsWrite
	AllocSet  map[*ssa.Alloc]ssa.Instruction
	MutexSet  map[ssa.Value]bool
}

type IsWrite bool

func NewFnSummary(fset *token.FileSet) *FnSummary {
	return &FnSummary{
		fset:      fset,
		AccessSet: make(map[ssa.Value]IsWrite),
		AllocSet:  make(map[*ssa.Alloc]ssa.Instruction),
		MutexSet:  make(map[ssa.Value]bool),
	}
}

func (f *FnSummary) Summarize(function *ssa.Function, ptaConfig *pointer.Config) {
	for _, block := range function.Blocks {
		for _, instr := range block.Instrs {
			log.Debugln(instr)
			f.visitIns(instr)
		}
	}
	for loc, _ := range f.AccessSet {
		ptaConfig.AddQuery(loc)
	}
	for m, _ := range f.MutexSet {
		ptaConfig.AddQuery(m)
	}
}

func (f *FnSummary) visitIns(instruction ssa.Instruction) {
	switch instr := instruction.(type) {
	case *ssa.UnOp:
		f.visitUnOp(instr)
	case *ssa.Store:
		f.visitStore(instr)
	case *ssa.MapUpdate:
		f.visitMapUpdate(instr)
	case *ssa.Lookup:
		f.visitLookup(instr)
	case *ssa.Alloc:
		f.visitAlloc(instr)
	case *ssa.IndexAddr:
		log.Debugf("indexAddr %s", instr.X)
		f.recordRead(instr.X)
	case *ssa.FieldAddr:
		log.Debugf("fieldAddr %s", instr.X)
		f.recordRead(instr.X)
	case *ssa.Call:
		if o := GetLockedMutex(instr.Common()); o != nil {
			f.MutexSet[o] = true
		} else if o := GetUnlockedMutex(instr.Common()); o != nil {
			f.MutexSet[o] = true
		}
	}
}

func (f *FnSummary) visitStore(instr *ssa.Store) {
	log.Debugf("store %s %s", instr.Addr, f.fset.Position(instr.Addr.Pos()))
	f.recordWrite(instr.Addr)
}

func (f *FnSummary) visitUnOp(instr *ssa.UnOp) {
	switch instr.Op {
	case token.MUL:
		log.Debugf("deref %s %s", instr.X, f.fset.Position(instr.X.Pos()))
		f.recordRead(instr.X)
	case token.ARROW:
		log.Debugf("recv")
	}
}

func (f *FnSummary) visitMapUpdate(instr *ssa.MapUpdate) {
	log.Debugf("MapUpdate %s", instr.Map)
	f.recordWrite(instr.Map)
}

func (f *FnSummary) visitLookup(instr *ssa.Lookup) {
	log.Debugf("Lookup %s", instr.X)
	f.recordRead(instr.X)
}

func (f *FnSummary) visitAlloc(instr *ssa.Alloc) {
	log.Debugf("Alloc %s", instr)
	if instr.Heap {
		f.AllocSet[instr] = instr
	}
}

func (f *FnSummary) visitCallCommon(instr ssa.CallCommon) {
	args := instr.Args
	if instr.Method != nil {
		args = append(args, instr.Value)
	}
}

func (f *FnSummary) recordRead(loc ssa.Value) {
	if pointer.CanPoint(loc.Type()) {
		f.AccessSet[loc] = f.AccessSet[loc]
	}
}

func (f *FnSummary) recordWrite(loc ssa.Value) {
	if pointer.CanPoint(loc.Type()) {
		f.AccessSet[loc] = true
	}
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
