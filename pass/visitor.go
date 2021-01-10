package pass

import (
	"github.com/o2lab/go2/preprocessor"
	"golang.org/x/tools/go/pointer"
	"golang.org/x/tools/go/ssa"
)

type CFGVisitor struct {
	ptaResult          *pointer.Result
	sharedPtrSet       map[pointer.Pointer]bool
	Accesses           map[pointer.Pointer][]*Access
	passes             map[*ssa.Function]*FnPass
	summaries          map[*ssa.Function]preprocessor.FnSummary
	domains            map[*ssa.Function]ThreadDomain
	FuncAcquiredValues map[*ssa.Function][]ssa.Value
	escapedValues map[*ssa.Go][]ssa.Value
	escapeSites        []*EscapeSite
	reads              map[*EscapeSite][]*Access
	writes             map[*EscapeSite][]*Access
	goStacks           map[*ssa.Go]CallStack
}

func NewCFGVisitorState(ptaResult *pointer.Result, sharedPtrSet map[pointer.Pointer]bool,
	domains map[*ssa.Function]ThreadDomain, escapedValues map[*ssa.Go][]ssa.Value) *CFGVisitor {
	return &CFGVisitor{
		ptaResult:          ptaResult,
		sharedPtrSet:       sharedPtrSet,
		escapedValues:escapedValues,
		passes:             make(map[*ssa.Function]*FnPass),
		domains:            domains,
		FuncAcquiredValues: make(map[*ssa.Function][]ssa.Value),
		goStacks:           make(map[*ssa.Go]CallStack),
	}
}

func (v *CFGVisitor) VisitFunction(function *ssa.Function, stack CallStack) {
	fnPass, ok := v.passes[function]
	if !ok {
		fnPass = NewFnPass(v, v.domains[function], v.summaries[function], v.FuncAcquiredValues, stack)
		v.passes[function] = fnPass
	}
	fnPass.Visit(function)
}

type EscapeSite struct {
	value ssa.Value
	ptr   pointer.Pointer
	stack CallStack
}
