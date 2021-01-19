package preprocessor

import (
	"github.com/o2lab/go2/go/ssa"
	"github.com/o2lab/go2/pointer"
	log "github.com/sirupsen/logrus"
	"go/token"
)

type FnSummary struct {
	fset         *token.FileSet
	preprocessor *Preprocessor
	AccessSet    map[ssa.Value]IsWrite
	HeadAllocs   []ssa.Value
	MutexSet     map[ssa.Value]bool
	ExitBlocks   []*ssa.BasicBlock
}

type IsWrite bool

func NewFnSummary(fset *token.FileSet, preprocessor *Preprocessor) *FnSummary {
	return &FnSummary{
		fset:         fset,
		preprocessor: preprocessor,
		AccessSet:    make(map[ssa.Value]IsWrite),
		MutexSet:     make(map[ssa.Value]bool),
	}
}

func (f *FnSummary) Preprocess(function *ssa.Function, ptaConfig *pointer.Config) {
	for _, block := range function.Blocks {
		for _, instr := range block.Instrs {
			log.Debugln(instr)
			f.visitIns(instr)
		}
		if block.Succs == nil {
			f.ExitBlocks = append(f.ExitBlocks, block)
		}
	}
	for _, v := range function.FreeVars {
		ptaConfig.AddQuery(v)
	}
	for _, v := range function.Params {
		if pointer.CanPoint(v.Type()) {
			ptaConfig.AddQuery(v)
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
	case *ssa.Go:
		escaped := captureEscapedVariables(instr)
		for _, value := range escaped {
			f.preprocessor.EscapedValues[instr] = append(f.preprocessor.EscapedValues[instr], value)
		}
	case *ssa.Defer:
		// Append a synthetic deferred instruction to the end of all reachable blocks from instr.Block().
		stack := []*ssa.BasicBlock{instr.Block()}
		seen := make([]bool, len(instr.Parent().Blocks))
		for len(stack) > 0 {
			block := stack[0]
			stack = stack[1:]
			if seen[block.Index] {
				continue
			}
			seen[block.Index] = true
			if len(block.Succs) == 0 {
				block.Instrs = append(block.Instrs, SyntheticDeferred{instr})
			} else {
				stack = append(stack, block.Succs...)
			}
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
		f.recordRead(instr) // indirect access
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
		f.HeadAllocs = append(f.HeadAllocs, instr)
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

func GetClosedChan(call *ssa.CallCommon) ssa.Value {
	if builtIn, ok := call.Value.(*ssa.Builtin); ok {
		if builtIn.Name() == "close" {
			return call.Args[0]
		}
	}
	return nil
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
