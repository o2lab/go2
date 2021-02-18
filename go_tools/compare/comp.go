package compare

import (
	"fmt"
	"github.tamu.edu/April1989/go_tools/go/callgraph"
	"github.tamu.edu/April1989/go_tools/go/pointer"
	default_algo "github.tamu.edu/April1989/go_tools/go/pointer_default"
	"github.tamu.edu/April1989/go_tools/go/ssa"
	"strings"
)

var cgDiffs []*CGDiff
var queryDiffs []*QueryDiff

type CGDiff struct {
	default_caller *callgraph.Node
	mycaller       *pointer.Node
}

func (diff CGDiff) print() {
	fmt.Println("My CG: ")
	fmt.Println("  ", diff.mycaller.String())
	for _, out := range diff.mycaller.Out {
		fmt.Println("\t-> " + out.Callee.String())
	}
	fmt.Println("Default CG: ")
	fmt.Println("  ", diff.default_caller.String())
	for _, out := range diff.default_caller.Out {
		fmt.Println("\t-> " + out.Callee.String())
	}
}

type QueryDiff struct {
	v              ssa.Value
	defaultPointer *default_algo.Pointer
	myPointers     []pointer.PointerWCtx
}

func (diff QueryDiff) print() {
	fmt.Println("My Query: ")
	for _, p := range diff.myPointers {
		fmt.Println("  ", p.String())
	}
	fmt.Println("Default Query: ")
	fmt.Println("  ", diff.defaultPointer.String())
}

func Compare(result_default *default_algo.Result, result_my *pointer.ResultWCtx) {
	fmt.Println("\n\nCompare ... ")

	compareCG(result_default.CallGraph, result_my.CallGraph)
	compareQueries(result_default.Queries, result_my.Queries)
	compareQueries(result_default.IndirectQueries, result_my.IndirectQueries)

	//output all cg diff
	if len(cgDiffs) > 0 {
		fmt.Println("CG DIFFs: ")
		for i, cg_diff := range cgDiffs {
			fmt.Print(i, ".\n")
			cg_diff.print()
		}
	}
	//output all query/indirect query diff
	if len(queryDiffs) > 0 {
		fmt.Println("QUERIES/INDIRECT QUERIES DIFFs: ")
		for i, q_diff := range queryDiffs {
			fmt.Print(i, ".\n")
			q_diff.print()
		}
	}

	if len(cgDiffs) == 0 && len(queryDiffs) == 0 {
		fmt.Println("Done ... NO DIFF BETWEEN DEFAULT AND MINE. ")
	}
}

func existCGKey(default_cg *callgraph.Graph, mycaller string) (*callgraph.Node, []*callgraph.Edge) {
	for fn, cgn := range default_cg.Nodes {
		caller := fn.String()
		if mycaller == caller {
			return cgn, cgn.Out
		}
	}
	return nil, nil
}

func existCGVal(default_outs []*callgraph.Edge, mycallee string) bool {
	for _, default_out := range default_outs {
		callee := default_out.Callee.Func.String()
		if mycallee == callee {
			return true
		}
	}
	return false
}

func createDiffForCG(default_caller *callgraph.Node, mycaller *pointer.Node) {
	diff := &CGDiff{
		default_caller: default_caller,
		mycaller:       mycaller,
	}
	cgDiffs = append(cgDiffs, diff)
}

func compareCG(default_cg *callgraph.Graph, my_cg *pointer.GraphWCtx) {
	callers := my_cg.Nodes
	for _, caller := range callers { // my caller with ctx
		mycaller := caller.GetFunc().String()
		default_caller, default_outs := existCGKey(default_cg, mycaller)
		if default_caller == nil {
			//no such key in default
			createDiffForCG(default_caller, caller)
			continue
		}

		outs := caller.Out   // caller --> callee
		if len(default_outs) == 0 && len(outs) == 0 {
			continue
		}

		hasDiff := false
		for _, out := range outs { //my callees with ctx
			mycallee := out.Callee.GetFunc().String()
			if existCGVal(default_outs, mycallee) {
				continue
			} else {
				//no such val in default
				hasDiff = true
			}
		}

		if hasDiff {
			createDiffForCG(default_caller, caller)
		}
	}
}

func existQKey(default_queries map[ssa.Value]default_algo.Pointer, v ssa.Value) *default_algo.Pointer {
	if default_val, ok := default_queries[v]; ok {
		return &default_val
	}
	return nil
}

func existQVal(default_pts []string, obj string) bool {
	for _, default_obj := range default_pts {
		if default_obj == obj {
			return true
		}
	}
	return false
}

func createDiffForQuery(v ssa.Value, default_pts *default_algo.Pointer, my_pts []pointer.PointerWCtx) {
	diff := &QueryDiff{
		v:              v,
		defaultPointer: default_pts,
		myPointers:     my_pts,
	}
	queryDiffs = append(queryDiffs, diff)
}

func compareQueries(default_queries map[ssa.Value]default_algo.Pointer, my_queries map[ssa.Value][]pointer.PointerWCtx) {
	for v, ps := range my_queries {
		//bz: we need to do like default: merge them to a canonical node
		var my_pts []string
		for _, p := range ps { //p -> types.Pointer: includes its context; SSA here is your *ssa.Value
			_pts := p.PointsTo().String()
			_pts = _pts[1 : len(_pts)-1]
			objs := strings.Split(_pts, ",")
			for _, obj := range objs {
				my_pts = append(my_pts, obj)
			}
		}

		default_pointer := existQKey(default_queries, v)
		if default_pointer == nil {
			//no such key in default
			createDiffForQuery(v, default_pointer, ps)
			continue
		}

		var default_pts []string
		_pts := default_pointer.PointsTo().String()
		_pts = _pts[1 : len(_pts)-1]
		objs := strings.Split(_pts, ",")
		for _, obj := range objs {
			default_pts = append(default_pts, obj)
		}

		hasDiff := false
		for _, my_obj := range my_pts {
			if existQVal(default_pts, my_obj) {
				continue
			} else {
				//no such val in default
				hasDiff = true
			}
		}

		if hasDiff {
			createDiffForQuery(v, default_pointer, ps)
		}
	}
}
