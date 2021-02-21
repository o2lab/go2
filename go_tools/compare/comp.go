package compare

import (
	"fmt"
	"github.tamu.edu/April1989/go_tools/go/callgraph"
	"github.tamu.edu/April1989/go_tools/go/pointer"
	default_algo "github.tamu.edu/April1989/go_tools/go/pointer_default"
	"github.tamu.edu/April1989/go_tools/go/ssa"
	"strings"
)

/**
   This file compare the difference between my result and default go pta result,
   including call graph, queries and indirect queries
 */



var cgDiffs []*CGDiff
var queryDiffs []*QueryDiff
var LESS_OBJ_IN_MY_PTS int //bz: if my pts has less objs than the default pts

type CGDiff struct {
	default_caller *callgraph.Node
	mycaller       *pointer.Node
}

func (diff CGDiff) print() {
	if diff.mycaller == nil {
		fmt.Println("My CG: nil")
	}else{
		fmt.Println("My CG: (#targets: ", len(diff.mycaller.Out), ")")
		fmt.Println("  ", diff.mycaller.String())
		for _, out := range diff.mycaller.Out {
			fmt.Println("\t-> " + out.Callee.String())
		}
	}

	if diff.default_caller == nil {
		fmt.Println("Default CG: nil")
	}else{
		fmt.Println("Default CG: (#targets: ", len(diff.default_caller.Out), ")")
		fmt.Println("  ", diff.default_caller.String())
		for _, out := range diff.default_caller.Out {
			fmt.Println("\t-> " + out.Callee.String())
		}
	}
}

type QueryDiff struct {
	v              ssa.Value
	defaultPointer *default_algo.Pointer
	myPointers     []pointer.PointerWCtx
	mySize         int
}

func (diff QueryDiff) print() {
	fmt.Println("SSA: ", diff.v)
	if diff.myPointers == nil {
		fmt.Println("My Query: nil")
	}else{
		fmt.Println("My Query: (#obj: ", diff.mySize, ")")
		for _, p := range diff.myPointers {
			fmt.Println("  ", p.String(),":", p.PointsTo().Labels())
		}
	}

	if diff.defaultPointer == nil {
		fmt.Println("Default Query: nil")
	}else{
		fmt.Println("Default Query: (#obj: ", len(diff.defaultPointer.PointsTo().Labels()), ")")
		fmt.Println("  ", diff.defaultPointer.String(),":", diff.defaultPointer.PointsTo().Labels())

		if  diff.mySize < len(diff.defaultPointer.PointsTo().Labels()) {
			LESS_OBJ_IN_MY_PTS++
		}
	}
}

func Compare(result_default *default_algo.Result, result_my *pointer.ResultWCtx) {
	fmt.Println("\n\nCompare ... ")

	compareCG(result_default.CallGraph, result_my.CallGraph)
	compareQueries(result_default.Queries, result_my.Queries)
	compareQueries(result_default.IndirectQueries, result_my.IndirectQueries)
	LESS_OBJ_IN_MY_PTS = 1

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
		fmt.Println("\n\nQUERIES/INDIRECT QUERIES DIFFs: (", LESS_OBJ_IN_MY_PTS, " of my queries has less objs)")
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
	var traversed_callers []*callgraph.Node //default
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
			continue // no callee
		}else if len(default_outs) != len(outs) {
			//different num of callees
			createDiffForCG(default_caller, caller)
			continue
		}

		//len(default_outs) == len(outs)
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

		//bz: i want to know if any in default are not in mine later
		traversed_callers = append(traversed_callers, default_caller)
	}

	if len(traversed_callers) == len(default_cg.Nodes) {
		return
	}

	//let's further check
	for _, default_caller := range default_cg.Nodes {
		if traversedCG(traversed_callers, default_caller) {
			continue
		}else{
			//bz: we see different cgnode for the same function
			// maybe with different context, but cannot know from here
			// further check if they have the same function, if so, we already compared it with the same diff
			if furtherTraversedCG(traversed_callers, default_caller) {
				continue
			}
			createDiffForCG(default_caller, nil)
		}
	}
}

func furtherTraversedCG(traversed_callers []*callgraph.Node, caller *callgraph.Node) bool {
	for _, traversed_caller := range traversed_callers {
		if traversed_caller.Func == caller.Func {
			fmt.Println(" ... ", traversed_caller.Func, ": ", traversed_caller.ID, " vs ", caller.ID)
			return true
		}
	}
	return false
}

func traversedCG(traversed_callers []*callgraph.Node, caller *callgraph.Node) bool {
	for _, traversed_caller := range traversed_callers {
		if traversed_caller == caller {
			return true
		}
	}
	return false
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

func createDiffForQuery(v ssa.Value, default_pts *default_algo.Pointer, my_pts []pointer.PointerWCtx, my_size int) {
	diff := &QueryDiff{
		v:              v,
		defaultPointer: default_pts,
		myPointers:     my_pts,
		mySize:         my_size,
	}
	queryDiffs = append(queryDiffs, diff)
}

func compareQueries(default_queries map[ssa.Value]default_algo.Pointer, my_queries map[ssa.Value][]pointer.PointerWCtx) {
	var visited []ssa.Value //traversed in default
	for v, ps := range my_queries {
		visited = append(visited, v)

		//bz: we need to do like default: merge them to a canonical node/pts here
		var my_pts []string
		for _, p := range ps { //p -> types.Pointer: includes its context; SSA here is your *ssa.Value
			_pts := p.PointsTo().String()
			_pts = _pts[1 : len(_pts)-1]
			objs := strings.Split(_pts, ",")
			for _, obj := range objs {
				if obj == "" {
					continue
				}
				my_pts = append(my_pts, obj)
			}
		}

		default_pointer := existQKey(default_queries, v)
		if default_pointer == nil {
			//no such key in default
			createDiffForQuery(v, default_pointer, ps, len(my_pts))
			continue
		}

		var default_pts []string
		_pts := default_pointer.PointsTo().String()
		_pts = _pts[1 : len(_pts)-1]
		objs := strings.Split(_pts, ",")
		for _, obj := range objs {
			if obj == "" {
				continue
			}
			default_pts = append(default_pts, obj)
		}

		if len(default_pts) == 0 && len(my_pts) == 0 {
			continue //empty pts
		}else if len(default_pts) != len(my_pts) {
			createDiffForQuery(v, default_pointer, ps, len(my_pts))
			continue
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
			createDiffForQuery(v, default_pointer, ps, len(my_pts))
		}
	}

	if len(default_queries) == len(my_queries) {
		return
	}

	//bz: for queries that in default but not mine
	for v, default_p := range default_queries {
		if traversedQ(visited, v) {
			continue
		}else{
			createDiffForQuery(v, &default_p, nil, 0)
		}
	}
}

func traversedQ(visited []ssa.Value, v ssa.Value) bool {
	for _, done := range visited {
		if done == v {
			return true
		}
	}
	return false
}
