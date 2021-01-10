package pass

import (
	"github.com/o2lab/go2/pointer"
	"github.com/o2lab/go2/preprocessor"
	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/ssa"
	"strings"
)

const ClosureBound = 3

type CallStack []*callgraph.Edge

func (cs CallStack) Copy() CallStack {
	stack := make(CallStack, len(cs))
	copy(stack, cs)
	return stack
}

type GlobalContext struct {
	threads     map[int]ThreadContext
	passes      map[*ssa.Function]*FnPass
	goStacks    map[*ssa.Go]CallStack
	threadCount int

	ptaResult    *pointer.Result
	sharedPtrSet map[pointer.Pointer]bool
	accesses     map[pointer.Pointer][]*Access
	summaries    map[*ssa.Function]preprocessor.FnSummary
}

type ThreadContext struct {
	stack              CallStack
	funcAcquiredValues map[ssa.Value][]ssa.Value
	refSet             map[ssa.Value]RefState
}

func (gc *GlobalContext) GraphVisitFiltered(g *callgraph.Graph, excluded map[string]bool) {
	seen := make(map[*callgraph.Node]int)
	var visit func(n *callgraph.Node)
	visit = func(n *callgraph.Node) {
		if seen[n] < ClosureBound {
			seen[n] = seen[n] + 1
			for _, e := range n.Out {
				callee := e.Callee
				if callee.Func.Pkg != nil {
					if pathRoot := strings.Split(callee.Func.Pkg.Pkg.Path(), "/")[0]; excluded[pathRoot] {
						return
					}
				}
				//gc.visitFun(n.Func)
				visit(callee)
			}
		}
		return
	}
	visit(g.Root)
	return
}

func NewGlobalContext(result *pointer.Result) *GlobalContext {
	return &GlobalContext{
		threads:   make(map[int]ThreadContext),
		passes:    make(map[*ssa.Function]*FnPass),
		ptaResult: result,
	}
}

func (gc *GlobalContext) visitGo(goFunc *ssa.Function) {
	gc.threadCount++
	//gc.threads[gc.threadCount] = &ThreadContext{}
}

func (gc *ThreadContext) visitFun(function *ssa.Function) {

}
