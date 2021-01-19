package pass

import (
	"github.com/o2lab/go2/go/callgraph"
	"github.com/o2lab/go2/go/ssa"
	"github.com/o2lab/go2/pointer"
	"github.com/o2lab/go2/preprocessor"
	log "github.com/sirupsen/logrus"
	"go/token"
)

type CFGVisitor struct {
	program            *ssa.Program
	ptaResult          *pointer.Result
	Accesses           map[pointer.Pointer][]*Access
	passes             map[*ssa.Function]*FnPass
	summaries          map[*ssa.Function]preprocessor.FnSummary
	FuncAcquiredValues map[*ssa.Function][]ssa.Value
	escapedValues      map[*ssa.Go][]ssa.Value
	goStacks           map[*ssa.Go]CallStack
	instrSiteMap       map[ssa.CallInstruction][]*callgraph.Edge
	aPointer           pointer.Pointer
	testOutput         map[token.Position][]string
}

func NewCFGVisitorState(ptaResult *pointer.Result, escapedValues map[*ssa.Go][]ssa.Value, program *ssa.Program, instrSiteMap map[ssa.CallInstruction][]*callgraph.Edge) *CFGVisitor {
	v := &CFGVisitor{
		ptaResult:          ptaResult,
		escapedValues:      escapedValues,
		passes:             make(map[*ssa.Function]*FnPass),
		FuncAcquiredValues: make(map[*ssa.Function][]ssa.Value),
		goStacks:           make(map[*ssa.Go]CallStack),
		program:            program,
		instrSiteMap:       instrSiteMap,
	}
	for _, p := range ptaResult.Queries {
		v.aPointer = p
		break
	}
	return v
}

func (v *CFGVisitor) SetTestOutput(out map[token.Position][]string) {
	v.testOutput = out
}

func (v *CFGVisitor) VisitFunction(function *ssa.Function, stack CallStack) {
	fnPass, ok := v.passes[function]
	if !ok {
		fnPass = NewFnPass(v, v.summaries[function], stack)
		v.passes[function] = fnPass
	}
	fnPass.extractBorrowedAccessSet(function)
	fnPass.backwardsDataflowAnalysis(function)
	fnPass.forwardDataflowAnalysis(function)
	fnPass.maskUnborrowedAccess()
}

func (pass *FnPass) extractBorrowedAccessSet(function *ssa.Function) {
	for _, v := range function.FreeVars {
		if ps := pass.valueToPointSet(v); ps != nil {
			pass.borrowedPointSet.UnionWith(&ps.Sparse)
			log.Debugf("freevar: %s", v)
		}
	}
	for _, v := range function.Params {
		if ps := pass.valueToPointSet(v); ps != nil {
			pass.borrowedPointSet.UnionWith(&ps.Sparse)
			log.Debugf("param: %s", v)
		}
	}
	log.Debugln("   => Borrowed:", pass.borrowedPointSet.AppendTo([]int{}))
}

func (pass *FnPass) maskUnborrowedAccess() {
	log.Debugln(pass.summary.HeadAllocs)
	var allocPointSet pointer.AccessPointSet
	for _, v := range pass.summary.HeadAllocs {
		if ps := pass.valueToPointSet(v); ps != nil {
			allocPointSet.UnionWith(&ps.Sparse)
		}
	}
	for p, _ := range pass.accessMeta {
		if allocPointSet.Has(p) {
			delete(pass.accessMeta, p) // removing during iteration is safe in Go
		}
	}
	pass.accessPointSet.IntersectionWith(&pass.borrowedPointSet.Sparse)
}
