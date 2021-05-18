// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pointer

import (
	"bytes"
	"fmt"
	"github.com/april1989/origin-go-tools/container/intsets"
	"github.com/april1989/origin-go-tools/go/callgraph"
	"github.com/april1989/origin-go-tools/go/ssa"
	"github.com/april1989/origin-go-tools/go/types/typeutil"
	"go/token"
	"io"
	"os"
	"strconv"
	"strings"
)

// A Config formulates a pointer analysis problem for Analyze. It is
// only usable for a single invocation of Analyze and must not be
// reused.
type Config struct {
	// Mains contains the set of 'main' packages to analyze
	// Clients must provide the analysis with at least one
	// package defining a main() function.
	//
	// Non-main packages in the ssa.Program that are not
	// dependencies of any main package may still affect the
	// analysis result, because they contribute runtime types and
	// thus methods.
	// TODO(adonovan): investigate whether this is desirable.
	Mains []*ssa.Package

	// Reflection determines whether to handle reflection
	// operators soundly, which is currently rather slow since it
	// causes constraint to be generated during solving
	// proportional to the number of constraint variables, which
	// has not yet been reduced by presolver optimisation.
	Reflection bool

	// BuildCallGraph determines whether to construct a callgraph.
	// If enabled, the graph will be available in Result.CallGraph.
	BuildCallGraph bool

	// The client populates Queries[v] or IndirectQueries[v]
	// for each ssa.Value v of interest, to request that the
	// points-to sets pts(v) or pts(*v) be computed.  If the
	// client needs both points-to sets, v may appear in both
	// maps.
	//
	// (IndirectQueries is typically used for Values corresponding
	// to source-level lvalues, e.g. an *ssa.Global.)
	//
	// The analysis populates the corresponding
	// Result.{Indirect,}Queries map when it creates the pointer
	// variable for v or *v.  Upon completion the client can
	// inspect that map for the results.
	//
	// TODO(adonovan): this API doesn't scale well for batch tools
	// that want to dump the entire solution.  Perhaps optionally
	// populate a map[*ssa.DebugRef]Pointer in the Result, one
	// entry per source expression.
	//
	Queries         map[ssa.Value]struct{}
	IndirectQueries map[ssa.Value]struct{}
	extendedQueries map[ssa.Value][]*extendedQuery

	// If Log is non-nil, log messages are written to it.
	// Logging is extremely verbose.
	Log io.Writer

	//bz: kcfa
	CallSiteSensitive bool
	//bz: origin-sensitive -> go routine as origin-entry
	Origin bool
	//bz: shared config by context-sensitive
	K          int      //how many level? the most recent callsite/origin?
	LimitScope bool     //only apply kcfa to app methods
	DEBUG      bool     //print out debug info
	Scope      []string //analyzed scope -> from user input: -path
	Exclusion  []string //excluded packages from this analysis -> from race_checker if any
	TrackMore  bool     //bz: track pointers with all types
	DoCallback bool     //bz: we synthesize for callback fn in app

	imports []string //bz: internal use: store all import pkgs in a main
	Level   int      //bz: level == 0: traverse all app and lib, but with different ctx; level == 1: traverse 1 level lib call; level == 2: traverse 2 leve lib calls; no other option now
}

//bz: user API: race checker
func (c *Config) GetImports() []string {
	return c.imports
}

type track uint32

const (
	trackChan  track = 1 << iota // track 'chan' references
	trackMap                     // track 'map' references
	trackPtr                     // track regular pointers
	trackSlice                   // track slice references

	trackAll = ^track(0)
)

// AddQuery adds v to Config.Queries.
// Precondition: CanPoint(v.Type()).
func (c *Config) AddQuery(v ssa.Value) {
	if !CanPoint(v.Type()) {
		panic(fmt.Sprintf("%s is not a pointer-like value: %s", v, v.Type()))
	}
	if c.Queries == nil {
		c.Queries = make(map[ssa.Value]struct{})
	}
	c.Queries[v] = struct{}{}
}

// AddQuery adds v to Config.IndirectQueries.
// Precondition: CanPoint(v.Type().Underlying().(*types.Pointer).Elem()).
func (c *Config) AddIndirectQuery(v ssa.Value) {
	if c.IndirectQueries == nil {
		c.IndirectQueries = make(map[ssa.Value]struct{})
	}
	if !CanPoint(mustDeref(v.Type())) {
		panic(fmt.Sprintf("%s is not the address of a pointer-like value: %s", v, v.Type()))
	}
	c.IndirectQueries[v] = struct{}{}
}

// AddExtendedQuery adds an extended, AST-based query on v to the
// analysis. The query, which must be a single Go expression, allows
// destructuring the value.
//
// The query must operate on a variable named 'x', which represents
// the value, and result in a pointer-like object. Only a subset of
// Go expressions are permitted in queries, namely channel receives,
// pointer dereferences, field selectors, array/slice/map/tuple
// indexing and grouping with parentheses. The specific indices when
// indexing arrays, slices and maps have no significance. Indices used
// on tuples must be numeric and within bounds.
//
// All field selectors must be explicit, even ones usually elided
// due to promotion of embedded fields.
//
// The query 'x' is identical to using AddQuery. The query '*x' is
// identical to using AddIndirectQuery.
//
// On success, AddExtendedQuery returns a Pointer to the queried
// value. This Pointer will be initialized during analysis. Using it
// before analysis has finished has undefined behavior.
//
// Example:
// 	// given v, which represents a function call to 'fn() (int, []*T)', and
// 	// 'type T struct { F *int }', the following query will access the field F.
// 	c.AddExtendedQuery(v, "x[1][0].F")
func (c *Config) AddExtendedQuery(v ssa.Value, query string) (*Pointer, error) {
	ops, _, err := parseExtendedQuery(v.Type(), query)
	if err != nil {
		return nil, fmt.Errorf("invalid query %q: %s", query, err)
	}
	if c.extendedQueries == nil {
		c.extendedQueries = make(map[ssa.Value][]*extendedQuery)
	}

	ptr := &Pointer{}
	c.extendedQueries[v] = append(c.extendedQueries[v], &extendedQuery{ops: ops, ptr: ptr})
	return ptr, nil
}

func (c *Config) prog() *ssa.Program {
	for _, main := range c.Mains {
		return main.Prog
	}
	panic("empty scope")
}

type Warning struct {
	Pos     token.Pos
	Message string
}

// A Result contains the results of a pointer analysis.
// See Config for how to request the various Result components.
//
// bz: updated
type Result struct {
	a           *analysis
	testMainCtx []*callsite      //bz: used when this is for test (a.isMain == false), in order to confirm the correct ctx as main
	CallGraph   *callgraph.Graph // discovered call graph
	////bz: default
	//Queries         map[ssa.Value]Pointer // pts(v) for each v in Config.Queries.
	//IndirectQueries map[ssa.Value]Pointer // pts(*v) for each v in Config.IndirectQueries.
	//bz: we replaced default to include context
	Queries         map[ssa.Value][]PointerWCtx // pts(v) for each v in setValueNode().
	IndirectQueries map[ssa.Value][]PointerWCtx // pts(*v) for each v in setValueNode().
	GlobalQueries   map[ssa.Value][]PointerWCtx // pts(v) for each freevar in setValueNode(). -> bz: used by api, will not expose to users
	Warnings        []Warning                   // warnings of unsoundness
}

//bz: same as default , but we want contexts
// most of its apis are hidden from users
type ResultWCtx struct {
	a         *analysis  // bz: we need a lot from here...
	main      *cgnode    // bz: the cgnode for main method; can be nil if analyzing tests
	CallGraph *GraphWCtx // discovered call graph

	//bz: if DiscardQueries the following will be empty
	Queries         map[ssa.Value][]PointerWCtx // pts(v) for each v in setValueNode().
	IndirectQueries map[ssa.Value][]PointerWCtx // pts(*v) for each v in setValueNode().
	GlobalQueries   map[ssa.Value][]PointerWCtx // pts(v) for each freevar in setValueNode().
	ExtendedQueries map[ssa.Value][]PointerWCtx // not used now
	Warnings        []Warning                   // warnings of unsoundness

	DEBUG bool // bz: print out debug info; used in race checker to debug
}

//bz:
func (r *ResultWCtx) getCGNodebyFuncGoInstr(fn *ssa.Function, goInstr *ssa.Go) *cgnode {
	cgns := r.getCGNodebyFunc(fn)
	for _, cgn := range cgns {
		if matchMyContext(cgn, goInstr) {
			return cgn
		}
	}
	if r.DEBUG {
		if goInstr == nil {
			fmt.Println(" **** no match *cgnode for " + fn.String() + " goID: main **** ")
		} else {
			fmt.Println(" **** no match *cgnode for " + fn.String() + " goID: " + goInstr.String() + " **** ")
		}
	}
	return nil
}

//bz: user API (DiscardQueries = true):
//we find the instr.register (instead of the things on the rhs) in this fn under this goInstr routine
//most of time used in sameAddress(from race_checker)
func (r *ResultWCtx) pointsTo2(v ssa.Value, goInstr *ssa.Go, fn *ssa.Function) PointerWCtx {
	if strings.Contains("&t0.mu [#0]", v.String()) {
		fmt.Print() //TODO: bz: lock problem
	}
	cgn := r.getCGNodebyFuncGoInstr(fn, goInstr)
	if cgn == nil {
		if r.DEBUG {
			fmt.Println(" ****  Pointer Analysis: " + v.String() + " has no match *cgnode (" + fn.String() + ") **** ")
		}
	} else {
		nodeid := cgn.localval[v]
		if nodeid != 0 {
			return PointerWCtx{a: r.a, n: nodeid, cgn: cgn}
		}

		//check if in a.localobj
		nodeid = cgn.localobj[v]
		if nodeid != 0 {
			return PointerWCtx{a: r.a, n: nodeid, cgn: cgn}
		}
	}

	//check if in a.globalval
	nodeid := r.a.globalval[v]
	if nodeid != 0 { //v exist
		//n := r.a.nodes[nodeid]
		return PointerWCtx{a: r.a, n: nodeid, cgn: nil}
	}

	//check if in a.globalobj
	nodeid = r.a.globalobj[v]
	if nodeid != 0 { //v exist
		//n := r.a.nodes[nodeid]
		return PointerWCtx{a: r.a, n: nodeid, cgn: nil}
	}
	if r.DEBUG {
		fmt.Println(" ****  Pointer Analysis: " + v.String() + " has no match in a.globalval/globalobj **** ")
	}
	return PointerWCtx{a: nil}
}

//bz: whether goID is match with the contexts in this cgn
//TODO: this does not match parent context if callsite.length > 1 (k > 1)
func matchMyContext(cgn *cgnode, go_instr *ssa.Go) bool {
	if go_instr == nil {
		//bz: check shared contour
		if cgn.callersite != nil && cgn.callersite[0] == nil {
			return true
		}
	}
	if cgn.callersite == nil || cgn.callersite[0] == nil {
		return false
	}
	if cgn.callersite[0].goInstr == go_instr {
		return true
	}
	if cgn.actualCallerSite == nil {
		return false
	}
	//double check actualCallerSite
	for _, actualCS := range cgn.actualCallerSite {
		if actualCS[0] == nil {
			return false
		}
		if actualCS[0].goInstr == go_instr {
			return true
		}
	}
	return false
}

//bz: user API: used when DiscardQueries == true
func (r *ResultWCtx) getFunc2(pointer PointerWCtx) *ssa.Function {
	pts := pointer.PointsTo()
	if pts.pts.Len() > 1 {
		if r.DEBUG {
			fmt.Println(" ****  Pointer Analysis: " + pointer.String() + " has multiple targets **** ") //panic
		}
	}
	tarID := pts.pts.Min()
	tar := r.a.nodes[tarID]
	if tar == nil {
		return nil
	}
	return tar.obj.cgn.fn
}

//bz: user API: used when DiscardQueries == true
//from call graph
func (r *ResultWCtx) getInvokeFunc(call *ssa.Call, pointer PointerWCtx, goInstr *ssa.Go) []*ssa.Function {
	fn := call.Parent()
	cgn := r.getCGNodebyFuncGoInstr(fn, goInstr)
	if cgn == nil {
		return nil
	}

	var result []*ssa.Function
	caller := r.CallGraph.GetNodeWCtx(cgn)
	for _, out := range caller.Out {
		if call == out.Site {
			callee := out.Callee.GetFunc()
			result = append(result, callee)
		}
	}
	return result
}

//bz: user API: tmp solution for missing invoke callee target if func wrapped in parameters
//alloc should be a freevar
func (r *ResultWCtx) getFreeVarFunc(caller *ssa.Function, call *ssa.Call, goInstr *ssa.Go) *cgnode {
	cg := r.a.result.CallGraph
	nodes := cg.GetNodesForFn(caller)
	if nodes == nil {
		if r.DEBUG {
			fmt.Println(" ****  Pointer Analysis: " + caller.String() + " has no contexts **** ") //panic
		}
		return nil
	} else if len(nodes) > 1 {
		if r.DEBUG {
			fmt.Println(" ****  Pointer Analysis: " + caller.String() + " has multiple contexts **** ") //panic
		}
	}

	for _, node := range nodes { //caller
		for _, out := range node.Out { //outgoing call edge
			if out.Site == call { //the same call site
				return out.Callee.cgn
			}
		}
	}

	return nil
}

//bz: user API: to handle special case -> extract target (cgn) from call graph
func (r *ResultWCtx) getFunc(p ssa.Value, call *ssa.Call, goInstr *ssa.Go) *ssa.Function {
	parentFn := p.Parent()
	parent_cgns := r.getCGNodebyFunc(parentFn)
	//match the ctx
	var parent_cgn *cgnode
	for _, cand := range parent_cgns {
		if cand.callersite[0] == nil { //shared contour
			if len(parent_cgns) == 1 {
				// + only one target
				parent_cgn = cand
				break
			}
			continue
		}
		//otherwise
		cand_goInstr := cand.callersite[0].goInstr
		if cand_goInstr == goInstr {
			parent_cgn = cand
			break
		}
	}
	if parent_cgn == nil {
		return nil //should not be ...
	}

	parent_cgnode := r.CallGraph.GetNodeWCtx(parent_cgn)
	outedges := parent_cgnode.Out
	for _, outedge := range outedges {
		callinstr := outedge.Site
		if callinstr == call { //this is the call edge
			return outedge.Callee.cgn.fn
		}
	}

	return nil
}

//bz: user API: return *cgnode by *ssa.Function
func (r *ResultWCtx) getCGNodebyFunc(fn *ssa.Function) []*cgnode {
	return r.CallGraph.Fn2CGNode[fn]
}

//bz: user API: return the main method with type *Node
func (r *ResultWCtx) getMain() *Node {
	return r.CallGraph.Nodes[r.main]
}

//bz: user API: return []PointerWCtx for a ssa.Value,
//user does not need to distinguish different queries anymore
//input: ssa.Value;
//output: PointerWCtx
//panic: if no record for such input
func (r *ResultWCtx) PointsTo(v ssa.Value) []PointerWCtx {
	pointers := r.pointsToFreeVar(v)
	if pointers != nil {
		return pointers
	}
	pointers = r.pointsToRegular(v)
	if pointers != nil {
		return pointers
	}
	if r.DEBUG {
		fmt.Println(" ****  Pointer Analysis did not record for this ssa.Value: " + v.String() + " **** ") //panic
	}
	return nil
}

//bz: user API: return PointerWCtx for a ssa.Value used under context of *ssa.GO,
//input: ssa.Value, *ssa.GO;
//output: PointerWCtx; this can be empty with nothing if we cannot match any
func (r *ResultWCtx) pointsToByGo(v ssa.Value, goInstr *ssa.Go) PointerWCtx {
	ptss := r.pointsToFreeVar(v)
	if ptss != nil {
		return ptss[0] //bz: should only have one value
	}
	if goInstr == nil {
		return r.pointsToByMain(v)
	}
	ptss = r.pointsToRegular(v) //return type: []PointerWCtx
	for _, pts := range ptss {
		if pts.MatchMyContext(goInstr, nil) {
			return pts
		}
	}
	if r.DEBUG {
		fmt.Println(" ****  Pointer Analysis cannot match this ssa.Value: " + v.String() + " with this *ssa.GO: " + goInstr.String() + " **** ") //panic
	}
	return PointerWCtx{a: nil}
}

//bz: user API: return PointerWCtx for a ssa.Value used under the main context
func (r *ResultWCtx) pointsToByMain(v ssa.Value) PointerWCtx {
	ptss := r.pointsToFreeVar(v)
	if ptss != nil {
		return ptss[0] //bz: should only have one value
	}
	ptss = r.pointsToRegular(v) //return type: []PointerWCtx
	for _, pts := range ptss {
		if pts.cgn == nil || pts.cgn.callersite == nil || pts.cgn.callersite[0] == nil {
			continue //from extended query or shared contour
		}
		if pts.cgn.callersite[0].targets == r.main.callersite[0].targets {
			return pts
		}
	}
	if r.DEBUG {
		fmt.Println(" ****  Pointer Analysis cannot match this ssa.Value: " + v.String() + " with main thread **** ") //panic
	}
	return PointerWCtx{a: nil}
}

//bz: return []PointerWCtx for query and indirect query and extended query1Ò
func (r *ResultWCtx) pointsToRegular(v ssa.Value) []PointerWCtx {
	pointers := r.Queries[v]
	if pointers != nil {
		return pointers
	}
	pointers = r.IndirectQueries[v]
	if pointers != nil {
		return pointers
	}
	pointers = r.ExtendedQueries[v]
	if pointers != nil {
		return pointers
	}
	//fmt.Println(" ****  Pointer Analysis did not record for this ssa.Value: " + v.String() + " **** (PointsToRegular)") //panic
	return nil
}

//bz: return []PointerWCtx for a free var,
func (r *ResultWCtx) pointsToFreeVar(v ssa.Value) []PointerWCtx {
	if globalv, ok := v.(*ssa.Global); ok {
		pointers := r.GlobalQueries[globalv]
		return pointers
	} else if freev, ok := v.(*ssa.FreeVar); ok {
		pointers := r.GlobalQueries[freev]
		return pointers
	} else if op, ok := v.(*ssa.UnOp); ok {
		pointers := r.GlobalQueries[op.X] //bz: X is the freeVar
		return pointers
	}
	//fmt.Println(" ****  Pointer Analysis did not record for this ssa.Value: " + v.String() + " **** (PointsToFreeVar)") //panic
	return nil
}

//bz: just in case we did not record for v
//TODO: (incomplete) iterate all a.nodes to find it ....
func (r *ResultWCtx) pointsToFurther(v ssa.Value) []PointerWCtx {
	for _, p := range r.a.nodes {
		if p.solve.pts.IsEmpty() {
			continue //not a pointer or empty pts
		}
	}
	return nil
}

//bz: for mine data
func (r *ResultWCtx) CountMyReachUnreachFunctions(doDetail bool) (map[*ssa.Function]*ssa.Function, map[*ssa.Function]*ssa.Function,
	map[int]int, map[int]int, map[*ssa.Function]*ssa.Function) {
	//cgns
	reachCGs := make(map[int]int)
	initIDs := make(map[int]int) //reachable, see below
	//fn
	reaches := make(map[*ssa.Function]*ssa.Function)
	reachApps := make(map[*ssa.Function]*ssa.Function) //reachable app functions
	unreaches := make(map[*ssa.Function]*ssa.Function)
	inits := make(map[*ssa.Function]*ssa.Function)             //functions with name format: xxx/xxx.xx.init
	var initEdges []*Edge                                      //out going cg edge from inits
	dangleInits := make(map[*ssa.Function]*ssa.Function)       //init function but cannot be reached by main
	var dangleInitsEdges []*Edge                               //out going cg edge from dangleInits
	initReaches := make(map[*ssa.Function]*ssa.Function)       //non init function that are only reachable by init functions -> they have shared contour
	dangleInitReaches := make(map[*ssa.Function]*ssa.Function) // fn only reachable by dangleInits, like implicits above
	//reachable origin #
	reachOrgs := make(map[*ssa.Go]*ssa.Go)

	var checks []*Edge
	//start from root
	root := r.CallGraph.Root
	reaches[root.GetFunc()] = root.GetFunc()
	reachCGs[root.ID] = root.ID
	for _, out := range root.Out {
		checks = append(checks, out)
	}

	//all reachable in cg, from root
	for len(checks) > 0 {
		var tmp []*Edge
		for _, check := range checks {
			if _, ok := reachCGs[check.Callee.ID]; ok {
				continue //checked already
			}

			reachCGs[check.Callee.ID] = check.Callee.ID
			for _, out := range check.Callee.Out {
				tmp = append(tmp, out)
			}

			cs := check.Callee.cgn.callersite[0]
			if cs != nil {
				if _go := cs.goInstr; _go != nil {
					reachOrgs[_go] = _go
				}
			}

			fn := check.Callee.GetFunc()
			fnName := fn.Name()
			if strings.HasPrefix(fnName, "init") {
				if _, ok := initIDs[check.Callee.ID]; !ok {
					initIDs[check.Callee.ID] = check.Callee.ID
				}
			}
		}
		checks = tmp
	}

	for _, node := range r.CallGraph.Nodes {
		if _, ok := reachCGs[node.ID]; ok { //reached -> store its fn
			if _, ok2 := reaches[node.GetFunc()]; ok2 {
				continue //already stored
			}
			reaches[node.GetFunc()] = node.GetFunc()
			continue
		} else {
			//collect implicit reachable fns
			//these functions in implicits are not from direct function call,
			//e.g.
			//; *t43 = init$2
			//	create n367 func() interface{} for fmt.init$2
			//	---- makeFunctionObject fmt.init$2
			//	create n368 func() interface{} for func.cgnode
			//	create n369 interface{} for func.results
			//	----
			//	globalobj[fmt.init$2] = n368
			//	addr n367 <- {&n368}
			//	val[init$2] = n367  (*ssa.Function)
			//	copy n366 <- n367
			//===>>>> most such cases happen in xxx.init function, and no call edge created for this,
			//  since they are prepared for potential future calls, however, it may or may NOT be called ...
			//  SO, if they are not reachable from cg, they are not called and should count as unreached???
			if _, ok2 := unreaches[node.GetFunc()]; ok2 {
				continue //already stored
			}
			unreaches[node.GetFunc()] = node.GetFunc()
		}
	}

	//let's check inits
	for _, node := range r.CallGraph.Nodes {
		fn := node.GetFunc()
		if _, ok := initIDs[node.ID]; ok { //reachable init fn
			if _, ok := inits[fn]; !ok {
				inits[fn] = fn
				for _, out := range node.Out {
					initEdges = append(initEdges, out)
				}
			}
		} else { //unreachable init fn ???
			fnName := fn.Name()
			if strings.HasPrefix(fnName, "init") {
				if _, ok := dangleInits[fn]; !ok {
					dangleInits[fn] = fn
					for _, out := range node.Out {
						dangleInitsEdges = append(dangleInitsEdges, out)
					}
				}
			}
		}
	}

	//do initReaches
	checks = initEdges
	for len(checks) > 0 {
		var tmp []*Edge
		for _, check := range checks {
			cgn := check.Callee.cgn
			fn := cgn.fn
			fnName := fn.Name()
			if strings.HasPrefix(fnName, "init") {
				continue //skip init fns
			}
			if _, ok := initReaches[fn]; ok {
				continue //checked already
			}

			//if cgn has shared contour
			if !cgn.IsSharedContour() {
				continue
			}

			//if cgn only reachable by init functions
			reach_by_other := false
			node := r.CallGraph.GetNodeWCtx(cgn)
			for _, in := range node.In {
				callerName := in.Caller.cgn.fn.Name()
				if !strings.HasPrefix(callerName, "init") {
					reach_by_other = true
				}
			}
			if reach_by_other {
				continue
			}

			initReaches[fn] = fn
			for _, out := range check.Callee.Out {
				tmp = append(tmp, out)
			}
		}
		checks = tmp
	}

	//do initReaches
	checks = dangleInitsEdges
	for len(checks) > 0 {
		var tmp []*Edge
		for _, check := range checks {
			fn := check.Callee.cgn.fn
			fnName := fn.Name()
			if strings.HasPrefix(fnName, "init") {
				continue //skip init fns
			}
			if _, ok := dangleInitReaches[fn]; ok {
				continue //checked already
			}

			dangleInitReaches[fn] = fn
			for _, out := range check.Callee.Out {
				tmp = append(tmp, out)
			}
		}
		checks = tmp
	}

	//how many of implicits are from preGens?
	cg := r.CallGraph
	prenodes := make(map[int]int)
	preFuncs := make(map[*ssa.Function]*ssa.Function) //& how many of functions are there in prenodes?
	for _, preGen := range r.a.preGens {
		precgnodes := cg.Fn2CGNode[preGen]
		for _, precgn := range precgnodes {
			if precgn.callersite[0] != nil { //this is not shared contour, not from pregen, but from origin call chain
				continue
			}
			node := cg.GetNodeWCtx(precgn)
			prenodes[node.ID] = node.ID
			preFuncs[node.GetFunc()] = node.GetFunc()
			//for _, out := range node.Out {
			//	checks = append(checks, out)
			//}
		}
	}

	//how many of reachable are from app? from lib?
	for _, f := range reaches {
		if r.a.withinScope(f.String()) {
			reachApps[f] = f
		}
	}

	//print out all numbers
	fmt.Println("#Reach Origins: ", len(reachOrgs)+1) //main
	fmt.Println("#Unreach CG Nodes: ", len(cg.Nodes)-(len(reachCGs)))
	fmt.Println("#Reach CG Nodes: ", len(reachCGs))
	fmt.Println("#Unreach Functions: ", len(unreaches))
	fmt.Println("#Reach Functions: ", len(reaches), " (#Reach App Functions: ", len(reachApps), ")")
	//fmt.Println("\n#Unreach Nodes from Pre-Gen Nodes: ", len(prenodes))
	//fmt.Println("#Unreach Functions from Pre-Gen Nodes: ", len(preFuncs))
	//fmt.Println("#(Pre-Gen are created for reflections)")
	fmt.Println("#Init Functions: ", len(inits))
	fmt.Println("#Init Reachable-Only Functions: ", len(initReaches))
	fmt.Println("#Dangling Init Functions: ", len(dangleInits))
	fmt.Println("#Dangling Init Reachable-Only Functions: ", len(dangleInitReaches))

	if doDetail { //bz: print out all details
		//fmt.Println("\n\nPre Generated Functions: ")
		//for _, prefunc := range preFuncs {
		//	fmt.Println(prefunc)
		//}
		fmt.Println("\n\n*****************************************************\nDump Details: ")
		fmt.Println("\n\nInit Reachable-Only Functions: ")
		for _, f := range initReaches {
			fmt.Println(f)
		}

		fmt.Println("\n\nDangling Init Functions: ")
		for _, f := range dangleInits {
			fmt.Println(f)
		}

		fmt.Println("\n\nDangling Init Reachable-Only Functions: ")
		for _, f := range dangleInitReaches {
			fmt.Println(f)
		}

		fmt.Println("\n\nUnreach Functions: ")
		for _, f := range unreaches {
			fmt.Println(f)
		}
	}

	//for further use
	return reaches, unreaches, reachCGs, prenodes, preFuncs
}

//bz: user API: for debug to dump all result out
func (r *ResultWCtx) DumpAll() {
	fmt.Println("\nWe are going to dump all results. If not desired, turn off DEBUG.")

	//bz: also a reference of how to use new APIs here
	main := r.getMain()
	fmt.Println("Main CGNode: " + main.String())

	fmt.Println("\nWe are going to print out call graph. If not desired, turn off DEBUG.")
	callers := r.CallGraph.Nodes
	fmt.Println("#CGNode: " + strconv.Itoa(len(callers)))
	for _, caller := range callers {
		//if !strings.Contains(caller.GetFunc().String(), "command-line-arguments.") {
		//	continue //we only want the app call edges
		//}
		fmt.Println(caller.String()) //bz: with context
		outs := caller.Out           // caller --> callee
		for _, out := range outs {   //callees
			fmt.Println("  -> " + out.Callee.String()) //bz: with context
		}
	}

	//fmt.Println("\nWe are going to print out queries. If not desired, turn off DEBUG.")
	//queries := r.Queries
	//inQueries := r.IndirectQueries
	//exQueries := r.ExtendedQueries
	//globalQueries := r.GlobalQueries
	//fmt.Println("#Queries: " + strconv.Itoa(len(queries)) + "  #Indirect Queries: " + strconv.Itoa(len(inQueries)) +
	//	"  #Extended Queries: " + strconv.Itoa(len(exQueries)) +
	//	"  #Global Queries: " + strconv.Itoa(len(globalQueries)))
	//
	//fmt.Println("Queries Detail: ")
	//for v, ps := range queries {
	//	for _, p := range ps { //p -> types.Pointer: includes its context
	//		//SSA here is your *ssa.Value
	//		fmt.Println(p.String() + " (SSA:" + v.String() + "): {" + p.PointsTo().String() + "}")
	//	}
	//}
	//
	//fmt.Println("\nIndirect Queries Detail: ")
	//for v, ps := range inQueries {
	//	for _, p := range ps { //p -> types.Pointer: includes its context
	//		fmt.Println(p.String() + " (SSA:" + v.String() + "): {" + p.PointsTo().String() + "}")
	//	}
	//}
	//
	//fmt.Println("\nExtended Queries Detail: ")
	//for v, ps := range exQueries {
	//	for _, p := range ps { //p -> types.Pointer: includes its context
	//		fmt.Println(p.String() + " (SSA:" + v.String() + "): {" + p.PointsTo().String() + "}")
	//	}
	//}
	//
	//fmt.Println("\nGlobal Queries Detail: ")
	//for v, ps := range globalQueries {
	//	for _, p := range ps { //p -> types.Pointer: includes its context
	//		fmt.Println(p.String() + " (SSA:" + v.String() + "): {" + p.PointsTo().String() + "}")
	//	}
	//}
}

//bz: user API, return nil if cannot find corresponding pts for v
func (r *Result) Query(v ssa.Value) []PointerWCtx {
	if pts, ok := r.Queries[v]; ok {
		return pts
	} else if _pts, ok := r.IndirectQueries[v]; ok {
		return _pts
	} else {
		fmt.Println(" ****  Pointer Analysis: " + v.String() + " has no match in Queries/IndirectQueries **** ")
		return nil
	}
}

//bz: user API: for debug to dump all queries out
func (r *Result) DumpAll() {
	fmt.Println("\nWe are going to dump all results. If not desired, turn off DEBUG.")
	r.DumpCG()
	r.DumpQueries()
}

//bz: user API: for debug to dump cg
func (r *Result) DumpCG() {
	_result := r.a.result

	fmt.Println("\nWe are going to print out call graph. If not desired, turn off DEBUG.")
	main := _result.getMain()
	fmt.Println("Main CGNode: " + main.String())
	callers := _result.CallGraph.Nodes
	fmt.Println("#CGNode: " + strconv.Itoa(len(callers)))
	for _, caller := range callers {
		//if !strings.Contains(caller.GetFunc().String(), "command-line-arguments.") {
		//	continue //we only want the app call edges
		//}
		fmt.Println(caller.String()) //bz: with context
		outs := caller.Out           // caller --> callee
		for _, out := range outs {   //callees
			fmt.Println("  -> " + out.Callee.String()) //bz: with context
		}
	}
}

//bz: user API: for debug to dump queries
func (r *Result) DumpQueries() {
	fmt.Println("\nWe are going to print out queries. If not desired, turn off DEBUG.")
	queries := r.Queries
	inQueries := r.IndirectQueries
	fmt.Println("#Queries: " + strconv.Itoa(len(queries)) + "  #Indirect Queries: " + strconv.Itoa(len(inQueries)))

	fmt.Println("Queries Detail: ")
	for v, ps := range queries {
		for _, p := range ps { //p -> types.Pointer: includes its context
			//SSA here is your *ssa.Value
			fmt.Println(p.String() + " (SSA:" + v.String() + "): {" + p.PointsTo().String() + "}")
		}
	}

	fmt.Println("\nIndirect Queries Detail: ")
	for v, ps := range inQueries {
		for _, p := range ps { //p -> types.Pointer: includes its context
			fmt.Println(p.String() + " (SSA:" + v.String() + "): {" + p.PointsTo().String() + "}")
		}
	}
}

//bz: intervals used as distributes and ranges in Statistics and TotalStatistics
var (
	//             idx:  0    1    2    3    4     5     6     7     8    9     10    11
	intervals1 = [12]int{10, 100, 200, 500, 700, 1000, 1500, 2000, 2500, 3000, 3500, 4000}
	intervals2 = [12]int{16, 128, 256, 512, 768, 1024, 1536, 2048, 2560, 3072, 3584, 4096}
)

//bz: for my use
func (r *Result) Statistics() {
	fmt.Println("\nStatistics of PTS: ")
	_result := r.a.result
	sPts := 0
	nPts := 0
	intervals := intervals1        //bz: intervals used to classify pts
	distributes := make([]int, 13) //bz: want to know the distribution of pts:
	//                  idx:  0     1     2     3    4      5      6      7      8      9      10      11    12
	// the array represents: <10, <100, <200, <500, <700, <1000, <1500, <2000, <2500, <3000, <3500, <4000, all others ...
	ranges := make([]int, 13) //bz: same as above, but == max obj idx - min obj idx
	mins := make([]int, 13)   //bz: the min obj idx in a pts
	//                  idx:   0       1      2       3       4       5       6        7       8         9       10      11       12
	// the array represents: <1000, <5000, <10000, <30000, <50000, <70000, <90000, <100000, <200000, <300000, <500000, <700000, all others ...

	for id, n := range _result.a.nodes {
		if !n.solve.pts.IsEmpty() {
			sPts++
			s := n.solve.pts.Len()
			r := n.solve.pts.Max() - n.solve.pts.Min()
			m := n.solve.pts.Min()
			nPts = nPts + s
			if s < intervals[0] {
				distributes[0] = distributes[0] + 1
			} else if s < intervals[1] {
				distributes[1] = distributes[1] + 1
			} else if s < intervals[2] {
				distributes[2] = distributes[2] + 1
			} else if s < intervals[3] {
				distributes[3] = distributes[3] + 1
			} else if s < intervals[4] {
				distributes[4] = distributes[4] + 1
			} else if s < intervals[5] {
				distributes[5] = distributes[5] + 1
			} else if s < intervals[6] {
				distributes[6] = distributes[6] + 1
			} else if s < intervals[7] {
				distributes[7] = distributes[7] + 1
			} else if s < intervals[8] {
				distributes[8] = distributes[8] + 1
			} else if s < intervals[9] {
				distributes[9] = distributes[9] + 1
			} else if s < intervals[10] {
				distributes[10] = distributes[10] + 1
			} else if s < intervals[11] {
				distributes[11] = distributes[11] + 1
			} else {
				distributes[12] = distributes[12] + 1
			}
			if r < intervals[0] {
				ranges[0] = ranges[0] + 1
			} else if r < intervals[1] {
				ranges[1] = ranges[1] + 1
			} else if r < intervals[2] {
				ranges[2] = ranges[2] + 1
			} else if r < intervals[3] {
				ranges[3] = ranges[3] + 1
			} else if r < intervals[4] {
				ranges[4] = ranges[4] + 1
			} else if r < intervals[5] {
				ranges[5] = ranges[5] + 1
			} else if r < intervals[6] {
				ranges[6] = ranges[6] + 1
			} else if r < intervals[7] {
				ranges[7] = ranges[7] + 1
			} else if r < intervals[8] {
				ranges[8] = ranges[8] + 1
			} else if r < intervals[9] {
				ranges[9] = ranges[9] + 1
			} else if r < intervals[10] {
				ranges[10] = ranges[10] + 1
			} else if r < intervals[11] {
				ranges[11] = ranges[11] + 1
			} else {
				ranges[12] = ranges[12] + 1
			}
			if m < 1000 {
				mins[0] = mins[0] + 1
			} else if r < 5000 {
				mins[1] = mins[1] + 1
			} else if r < 10000 {
				mins[2] = mins[2] + 1
			} else if r < 30000 {
				mins[3] = mins[3] + 1
			} else if r < 50000 {
				mins[4] = mins[4] + 1
			} else if r < 70000 {
				mins[5] = mins[5] + 1
			} else if r < 90000 {
				mins[6] = mins[6] + 1
			} else if r < 100000 {
				mins[7] = mins[7] + 1
			} else if r < 200000 {
				mins[8] = mins[8] + 1
			} else if r < 300000 {
				mins[9] = mins[9] + 1
			} else if r < 500000 {
				mins[10] = mins[10] + 1
			} else if r < 700000 {
				mins[11] = mins[11] + 1
			} else {
				mins[12] = mins[12] + 1
			}

			//bz: check the details
			if s > 1000 {
				if n.obj == nil {
					fmt.Printf("-> len = %d: pts(n%d : %s)\n", s, id, n.typ)
				} else {
					fmt.Printf("-> len = %d: pts(n%d : %s), created at: %s \n", s, id, n.typ, n.obj.cgn)
				}
			}
		}
	}
	printStatistics(sPts, nPts, intervals, distributes, mins, ranges)
}

//bz: for my use; statistics for all mains
func TotalStatistics(results map[*ssa.Package]*Result) {
	fmt.Println("\n\n*************************************\nTotal Statistics of PTS: ")
	sPts := 0
	nPts := 0
	intervals := intervals1        //bz: intervals used to classify pts
	distributes := make([]int, 13) //bz: want to know the distribution of pts:
	//                  idx:  0     1     2     3    4      5      6      7      8      9      10      11    12
	// the array represents: <10, <100, <200, <500, <700, <1000, <1500, <2000, <2500, <3000, <3500, <4000, all others ...
	ranges := make([]int, 13) //bz: same as above, but == max obj idx - min obj idx
	mins := make([]int, 13)   //bz: the min obj idx in a pts
	//                  idx:   0       1      2       3       4       5       6        7       8         9       10      11       12
	// the array represents: <1000, <5000, <10000, <30000, <50000, <70000, <90000, <100000, <200000, <300000, <500000, <700000, all others ...
	for main, r := range results {
		_result := r.a.result
		fmt.Println("main: ", main)
		for id, n := range _result.a.nodes {
			if !n.solve.pts.IsEmpty() {
				sPts++
				s := n.solve.pts.Len()
				r := n.solve.pts.Max() - n.solve.pts.Min()
				m := n.solve.pts.Min()
				nPts = nPts + s
				if s < intervals[0] {
					distributes[0] = distributes[0] + 1
				} else if s < intervals[1] {
					distributes[1] = distributes[1] + 1
				} else if s < intervals[2] {
					distributes[2] = distributes[2] + 1
				} else if s < intervals[3] {
					distributes[3] = distributes[3] + 1
				} else if s < intervals[4] {
					distributes[4] = distributes[4] + 1
				} else if s < intervals[5] {
					distributes[5] = distributes[5] + 1
				} else if s < intervals[6] {
					distributes[6] = distributes[6] + 1
				} else if s < intervals[7] {
					distributes[7] = distributes[7] + 1
				} else if s < intervals[8] {
					distributes[8] = distributes[8] + 1
				} else if s < intervals[9] {
					distributes[9] = distributes[9] + 1
				} else if s < intervals[10] {
					distributes[10] = distributes[10] + 1
				} else if s < intervals[11] {
					distributes[11] = distributes[11] + 1
				} else {
					distributes[12] = distributes[12] + 1
				}
				if r < intervals[0] {
					ranges[0] = ranges[0] + 1
				} else if r < intervals[1] {
					ranges[1] = ranges[1] + 1
				} else if r < intervals[2] {
					ranges[2] = ranges[2] + 1
				} else if r < intervals[3] {
					ranges[3] = ranges[3] + 1
				} else if r < intervals[4] {
					ranges[4] = ranges[4] + 1
				} else if r < intervals[5] {
					ranges[5] = ranges[5] + 1
				} else if r < intervals[6] {
					ranges[6] = ranges[6] + 1
				} else if r < intervals[7] {
					ranges[7] = ranges[7] + 1
				} else if r < intervals[8] {
					ranges[8] = ranges[8] + 1
				} else if r < intervals[9] {
					ranges[9] = ranges[9] + 1
				} else if r < intervals[10] {
					ranges[10] = ranges[10] + 1
				} else if r < intervals[11] {
					ranges[11] = ranges[11] + 1
				} else {
					ranges[12] = ranges[12] + 1
				}
				if m < 1000 {
					mins[0] = mins[0] + 1
				} else if r < 5000 {
					mins[1] = mins[1] + 1
				} else if r < 10000 {
					mins[2] = mins[2] + 1
				} else if r < 30000 {
					mins[3] = mins[3] + 1
				} else if r < 50000 {
					mins[4] = mins[4] + 1
				} else if r < 70000 {
					mins[5] = mins[5] + 1
				} else if r < 90000 {
					mins[6] = mins[6] + 1
				} else if r < 100000 {
					mins[7] = mins[7] + 1
				} else if r < 200000 {
					mins[8] = mins[8] + 1
				} else if r < 300000 {
					mins[9] = mins[9] + 1
				} else if r < 500000 {
					mins[10] = mins[10] + 1
				} else if r < 700000 {
					mins[11] = mins[11] + 1
				} else {
					mins[12] = mins[12] + 1
				}

				//bz: check the details
				if s > 1000 { //s > 100 && s < 1000
					if n.obj == nil {
						fmt.Printf("-> len = %d: pts(n%d : %s): underlying %s \n", s, id, n.typ, n.typ.Underlying())
					} else {
						fmt.Printf("-> len = %d: pts(n%d : %s), created at: %s \n", s, id, n.typ, n.obj.cgn)
					}
				}
			}
		}
	}

	printStatistics(sPts, nPts, intervals, distributes, mins, ranges)
	fmt.Println("*************************************\n\n")
}

//bz: common part for Statistics and TotalStatistics
func printStatistics(sPts, nPts int, intervals [12]int, distributes, mins, ranges []int) {
	fmt.Println("#pts: ", sPts, "\n#total of pts:", nPts, "\n#avg of pts:", float64(nPts)/float64(sPts))
	fmt.Println("\nDistribution: ", "\n# < ", intervals[0], ":", float64(distributes[0])/float64(sPts)*100, "%",
		"\n# <", intervals[1], ": ", float64(distributes[1])/float64(sPts)*100, "%",
		"\n# <", intervals[2], ": ", float64(distributes[2])/float64(sPts)*100, "%",
		"\n# <", intervals[3], ": ", float64(distributes[3])/float64(sPts)*100, "%",
		"\n# <", intervals[4], ": ", float64(distributes[4])/float64(sPts)*100, "%",
		"\n# <", intervals[5], ":", float64(distributes[5])/float64(sPts)*100, "%",
		"\n# <", intervals[6], ":", float64(distributes[6])/float64(sPts)*100, "%",
		"\n# <", intervals[7], ":", float64(distributes[7])/float64(sPts)*100, "%",
		"\n# <", intervals[8], ":", float64(distributes[8])/float64(sPts)*100, "%",
		"\n# <", intervals[9], ":", float64(distributes[9])/float64(sPts)*100, "%",
		"\n# <", intervals[10], ":", float64(distributes[10])/float64(sPts)*100, "%",
		"\n# <", intervals[11], ":", float64(distributes[11])/float64(sPts)*100, "%",
		"\n# others:", float64(distributes[12])/float64(sPts)*100, "%\n")
	fmt.Println("Min Idx in PTS: ", "\n# < 1000:  ", float64(mins[0])/float64(sPts)*100, "%",
		"\n# < 5000:  ", float64(mins[1])/float64(sPts)*100, "%",
		"\n# < 10000: ", float64(mins[2])/float64(sPts)*100, "%",
		"\n# < 30000: ", float64(mins[3])/float64(sPts)*100, "%",
		"\n# < 50000: ", float64(mins[4])/float64(sPts)*100, "%",
		"\n# < 70000: ", float64(mins[5])/float64(sPts)*100, "%",
		"\n# < 90000: ", float64(mins[6])/float64(sPts)*100, "%",
		"\n# < 100000:", float64(mins[7])/float64(sPts)*100, "%",
		"\n# < 200000:", float64(mins[8])/float64(sPts)*100, "%",
		"\n# < 300000:", float64(mins[9])/float64(sPts)*100, "%",
		"\n# < 500000:", float64(mins[10])/float64(sPts)*100, "%",
		"\n# < 700000:", float64(mins[11])/float64(sPts)*100, "%",
		"\n# others:", float64(mins[12])/float64(sPts)*100, "%\n")
	fmt.Println("Range(Max obj - Min obj): ", "\n# <", intervals[0], ":", float64(ranges[0])/float64(sPts)*100, "%",
		"\n# <", intervals[1], ": ", float64(ranges[1])/float64(sPts)*100, "%",
		"\n# <", intervals[2], ": ", float64(ranges[2])/float64(sPts)*100, "%",
		"\n# <", intervals[3], ": ", float64(ranges[3])/float64(sPts)*100, "%",
		"\n# <", intervals[4], ": ", float64(ranges[4])/float64(sPts)*100, "%",
		"\n# <", intervals[5], ":", float64(ranges[5])/float64(sPts)*100, "%",
		"\n# <", intervals[6], ":", float64(ranges[6])/float64(sPts)*100, "%",
		"\n# <", intervals[7], ":", float64(ranges[7])/float64(sPts)*100, "%",
		"\n# <", intervals[8], ":", float64(ranges[8])/float64(sPts)*100, "%",
		"\n# <", intervals[9], ":", float64(ranges[9])/float64(sPts)*100, "%",
		"\n# <", intervals[10], ":", float64(ranges[10])/float64(sPts)*100, "%",
		"\n# <", intervals[11], ":", float64(ranges[11])/float64(sPts)*100, "%",
		"\n# others:", float64(ranges[12])/float64(sPts)*100, "%")
}

//bz: do comparison with default
func (r *Result) GetResult() *ResultWCtx {
	return r.a.result
}

//bz: for callback use only
func (r *Result) GetMySyntheticFn(fn *ssa.Function) *ssa.Function {
	return r.a.GetMySyntheticFn(fn)
}

//bz: user API: return a map of (fn <-> cgnode) that are Testxxx, Examplexxx, Benchmarkxxx
// from a test analyzed by this r *Result
func (r *Result) GetTests() map[*ssa.Function]*cgnode {
	if r.a.isMain {
		fmt.Println("This result is for the main entry:", r.a.config.Mains[0], ", not for a test. Return.")
		return nil
	}

	a := r.a
	result := make(map[*ssa.Function]*cgnode)
	testSig := a.config.Mains[0].Pkg.Path() // this is xx/xx/xxx.test, we need to remove the '.test'
	testSig = testSig[0 : len(testSig)-5]

	for v, id := range a.globalobj {
		if fn, ok := v.(*ssa.Function); ok {
			name := fn.Name()
			pkg := fn.String()
			if (strings.HasPrefix(pkg, testSig) || strings.HasPrefix(pkg, "("+testSig)) && //static/non-static functions
				(strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "Benchmark") || strings.HasPrefix(name, "Example")) && // test format
				!strings.Contains(name, "$") { //exclude closure
				obj := a.nodes[id].obj
				if obj == nil {
					panic(fmt.Sprintf("nil obj of %s: %s", id, a.nodes[id]))
				}
				cgn := obj.cgn
				if cgn == nil {
					panic(fmt.Sprintf("nil cgnode in obj: %s", obj))
				}
				result[fn] = cgn
			}
		}
	}

	return result
}

//bz: user API: when analyzing test only, tell which test is used as main in order to confirm the correct main context
func (r *Result) AnalyzeTest(test *ssa.Function) {
	if r.a.isMain {
		panic("This result is for analyzing a main entry, not a test entry: " + test.String())
	}
	cg := r.a.result.CallGraph
	cgns := cg.Fn2CGNode[test]
	if len(cgns) > 1 {
		fmt.Println("cgn(test) > 1: something is wrong ... ")
	}
	r.testMainCtx = cgns[0].callersite
}

//bz: user API: return PointerWCtx for a ssa.Value used under context of *ssa.GO,
//input: ssa.Value, *ssa.GO
//output: PointerWCtx; this can be empty if we cannot match any v with its goInstr
func (r *Result) PointsToByGo(v ssa.Value, goInstr *ssa.Go) PointerWCtx {
	ptss := r.a.result.pointsToFreeVar(v)
	if ptss != nil { // global var
		if len(ptss) == 0 { //no record for this v
			return PointerWCtx{a: nil}
		} else if len(ptss) == 1 {
			return ptss[0]
		} else { // > 1
			fmt.Println(">1 pts for", v, ": len =", len(ptss))
			return ptss[0]
		}
	}

	//others
	ptss = r.a.result.pointsToRegular(v)
	if ptss == nil { //no record for this v
		return PointerWCtx{a: nil}
	}

	for _, pts := range ptss {
		if pts.cgn.fn == v.Parent() { //many same v (ssa.Value) from different functions, separate them
			if v.Parent().IsFromApp {
				if pts.MatchMyContext(goInstr, r.testMainCtx) {
					return pts
				}
			} else {
				//discard the goInstr input, we use shared contour in real pta, hence only one pts is available
				return pts
			}
		}
	}
	if r.a.result.DEBUG {
		if goInstr == nil {
			fmt.Println(" **** *Result:", v.Parent(), "Pointer Analysis cannot match this ssa.Value: "+v.String()+" with this *ssa.GO: main **** ") //panic
		} else {
			fmt.Println(" **** *Result:", v.Parent(), " Pointer Analysis cannot match this ssa.Value: "+v.String()+" with this *ssa.GO: "+goInstr.String()+" **** ") //panic
		}
	}
	return PointerWCtx{a: nil}
}

//bz: user API: return PointerWCtx for a ssa.Value used under context of *ssa.GO,
//input: ssa.Value, *ssa.GO, loopID = 1/2
//output: PointerWCtx; this can be empty if we cannot match any v with its goInstr
//Update: many same v from different functions ... further separate them
func (r *Result) PointsToByGoWithLoopID(v ssa.Value, goInstr *ssa.Go, loopID int) PointerWCtx {
	ptss := r.a.result.pointsToFreeVar(v)
	if ptss != nil { // global var
		if len(ptss) == 0 { //no record for this v
			return PointerWCtx{a: nil}
		} else if len(ptss) == 1 {
			return ptss[0]
		} else { // > 1
			fmt.Println(">1 pts for", v, ": len =", len(ptss))
			return ptss[0]
		}
	}

	//others
	ptss = r.a.result.pointsToRegular(v)
	if ptss == nil { //no record for this v
		return PointerWCtx{a: nil}
	}

	for _, pts := range ptss {
		if pts.cgn.fn == v.Parent() { //many same v (ssa.Value) from different functions, separate them
			if v.Parent().IsFromApp {
				if pts.MatchMyContextWithLoopID(goInstr, loopID, r.testMainCtx) {
					return pts
				}
			} else {
				//discard the goInstr input, we use shared contour in real pta, hence only one pts is available
				return pts
			}
		}
	}
	if r.a.result.DEBUG {
		if goInstr == nil {
			fmt.Println(" **** *Result: Pointer Analysis cannot match this ssa.Value: " + v.String() + " with this *ssa.GO: main **** ") //panic
		} else {
			fmt.Println(" **** *Result: Pointer Analysis cannot match this ssa.Value: " + v.String() + " with this *ssa.GO: " + goInstr.String() + " **** ") //panic
		}
	}
	return PointerWCtx{a: nil}
}

//bz: user API: return PointerWCtx for a ssa.Value used under context of *ssa.GO, and an offset
//   return pts(v + offset), v is base, if base == nil assume its field with offset is also nil
func (r *Result) PointsToByGoWithLoopIDOffset(base ssa.Value, f int, goInstr *ssa.Go, loopID int) PointerWCtx {
	basePtr := r.PointsToByGoWithLoopID(base, goInstr, loopID) //bz: base ptr
	if basePtr.a == nil {
		return PointerWCtx{a: nil}
	}

	return r.getPointerWithOffset(base, basePtr, f)
}

//bz: compute offset nodeid, similar to a.genOffsetAddr()
func (r *Result) getPointerWithOffset(baseV ssa.Value, base PointerWCtx, f int) PointerWCtx {
	cgn := base.cgn
	baseID := base.n
	offset := r.a.offsetOf(mustDeref(baseV.Type()), f)
	var fID nodeid
	if offset == 0 {
		fID = baseID
	} else {
		fID = baseID + nodeid(offset) + 1
	}
	return PointerWCtx{a: r.a, n: fID, cgn: cgn}
}

//bz: user API: tmp solution for missing invoke callee target if func wrapped in parameters
func (r *Result) GetFreeVarFunc(caller *ssa.Function, call *ssa.Call, goInstr *ssa.Go) *ssa.Function {
	cgn := r.a.result.getFreeVarFunc(caller, call, goInstr)
	if cgn == nil {
		return nil
	}
	return cgn.fn
}

func (r *Result) GetInvokeFuncs(call *ssa.Call, ptr PointerWCtx, goInstr *ssa.Go) []*ssa.Function {
	return r.a.result.getInvokeFunc(call, ptr, goInstr)
}


	//bz: do comparison with default
func (r *Result) DumpToCompare(cgfile *os.File, queryfile *os.File) {
	_result := r.a.result

	fmt.Println("\nWe are going to dump call graph for comparison. If not desired, turn off doCompare.")
	callers := _result.CallGraph.Nodes
	for _, caller := range callers {
		fmt.Fprintln(cgfile, caller.String()) //bz: with context
		outs := caller.Out                    // caller --> callee
		for _, out := range outs {            //callees
			fmt.Fprintln(cgfile, "  -> "+out.Callee.String()) //bz: with context
		}
	}

	fmt.Println("\nWe are going to dump queries for comparison. If not desired, turn off doCompare.")
	queries := r.Queries
	inQueries := r.IndirectQueries
	fmt.Fprintln(queryfile, "Queries Detail: ")
	for v, ps := range queries {
		for _, p := range ps { //p -> types.Pointer: includes its context
			//SSA here is your *ssa.Value
			fmt.Fprintln(queryfile, "(SSA:"+v.String()+"): {"+p.PointsTo().String()+"}")
		}
	}

	fmt.Fprintln(queryfile, "Indirect Queries Detail: ")
	for v, ps := range inQueries {
		for _, p := range ps { //p -> types.Pointer: includes its context
			fmt.Fprintln(queryfile, "(SSA:"+v.String()+"): {"+p.PointsTo().String()+"}")
		}
	}
}

//bz: for my use; a wrapper of the same function in *ResultWCtx
func (r *Result) CountMyReachUnreachFunctions(doDetail bool) (map[*ssa.Function]*ssa.Function, map[*ssa.Function]*ssa.Function,
	map[int]int, map[int]int, map[*ssa.Function]*ssa.Function) {
	return r.a.result.CountMyReachUnreachFunctions(doDetail)
}

//bz: user api, return an array of *AllocSite {*ssa.Function, []*callsite}
func (r *Result) GetAllocations(p PointerWCtx) []*AllocSite {
	var allocs []*AllocSite
	pts := p.PointsTo().pts
	if pts.IsEmpty() {
		return allocs
	}

	a := r.a
	var space [100]int
	for _, nid := range pts.AppendTo(space[:0]) {
		if nid == 0 {
			continue
		}
		obj := a.nodes[nodeid(nid)].obj
		if obj == nil {
			continue
		}
		site := &AllocSite{
			Fn:  obj.cgn.fn,
			Ctx: obj.cgn.callersite,
		}
		allocs = append(allocs, site)
	}
	return allocs
}

//bz: user api used by GetAllocations()
type AllocSite struct {
	Fn  *ssa.Function
	Ctx []*callsite
}

// A Pointer is an equivalence class of pointer-like values.
//
// A Pointer doesn't have a unique type because pointers of distinct
// types may alias the same object.
//
type Pointer struct {
	a *analysis
	n nodeid
}

// A PointsToSet is a set of labels (locations or allocations).
type PointsToSet struct {
	a   *analysis // may be nil if pts is nil
	pts *nodeset
}

//bz: we created an empty PointerWCTx to return
func (s PointsToSet) IsNil() bool {
	return s.pts == nil
}

func (s PointsToSet) String() string {
	var buf bytes.Buffer
	buf.WriteByte('[')
	if s.pts != nil {
		var space [50]int
		for i, l := range s.pts.AppendTo(space[:0]) {
			if i > 0 {
				buf.WriteString(", ")
			}
			buf.WriteString(s.a.labelFor(nodeid(l)).String())
		}
	}
	buf.WriteByte(']')
	return buf.String()
}

// PointsTo returns the set of labels that this points-to set
// contains.
func (s PointsToSet) Labels() []*Label {
	var labels []*Label
	if s.pts != nil {
		var space [50]int
		for _, l := range s.pts.AppendTo(space[:0]) {
			labels = append(labels, s.a.labelFor(nodeid(l)))
		}
	}
	return labels
}

// If this PointsToSet came from a Pointer of interface kind
// or a reflect.Value, DynamicTypes returns the set of dynamic
// types that it may contain.  (For an interface, they will
// always be concrete types.)
//
// The result is a mapping whose keys are the dynamic types to which
// it may point.  For each pointer-like key type, the corresponding
// map value is the PointsToSet for pointers of that type.
//
// The result is empty unless CanHaveDynamicTypes(T).
//
func (s PointsToSet) DynamicTypes() *typeutil.Map {
	var tmap typeutil.Map
	tmap.SetHasher(s.a.hasher)
	if s.pts != nil {
		var space [50]int
		for _, x := range s.pts.AppendTo(space[:0]) {
			ifaceObjID := nodeid(x)
			if !s.a.isTaggedObject(ifaceObjID) {
				continue // !CanHaveDynamicTypes(tDyn)
			}
			tDyn, v, indirect := s.a.taggedValue(ifaceObjID)
			if indirect {
				panic("indirect tagged object") // implement later
			}
			pts, ok := tmap.At(tDyn).(PointsToSet)
			if !ok {
				pts = PointsToSet{s.a, new(nodeset)}
				tmap.Set(tDyn, pts)
			}
			pts.pts.addAll(&s.a.nodes[v].solve.pts)
		}
	}
	return &tmap
}

// Intersects reports whether this points-to set and the
// argument points-to set contain common members.
func (s PointsToSet) Intersects(y PointsToSet) bool {
	if s.pts == nil || y.pts == nil {
		return false
	}
	// This takes Θ(|x|+|y|) time.
	var z intsets.Sparse
	z.Intersection(&s.pts.Sparse, &y.pts.Sparse)
	return !z.IsEmpty()
}

func (p Pointer) String() string {
	return fmt.Sprintf("n%d", p.n)
}

// PointsTo returns the points-to set of this pointer.
func (p Pointer) PointsTo() PointsToSet {
	if p.n == 0 {
		return PointsToSet{}
	}
	return PointsToSet{p.a, &p.a.nodes[p.n].solve.pts}
}

// MayAlias reports whether the receiver pointer may alias
// the argument pointer.
func (p Pointer) MayAlias(q Pointer) bool {
	return p.PointsTo().Intersects(q.PointsTo())
}

// DynamicTypes returns p.PointsTo().DynamicTypes().
func (p Pointer) DynamicTypes() *typeutil.Map {
	return p.PointsTo().DynamicTypes()
}

// bz: a Pointer with context
type PointerWCtx struct {
	a   *analysis
	n   nodeid
	cgn *cgnode
}

//bz: whether goID is match with the contexts in this pointer
//TODO: this does not match parent context if callsite.length > 1 (k > 1)
func (p PointerWCtx) MatchMyContext(go_instr *ssa.Go, testMainCtx []*callsite) bool {
	if p.cgn == nil || p.cgn.callersite == nil || p.cgn.callersite[0] == nil {
		if go_instr == nil { //shared contour ~> when using test as entry
			return true
		}
		return false
	}
	if go_instr == nil { //when wants main context
		if p.a.isMain { //when using main as entry
			if p.cgn.callersite[0].targets == p.a.result.main.callersite[0].targets {
				return true
			}
		} else { //when using test as entry
			if p.cgn.callersite[0].targets == testMainCtx[0].targets {
				return true
			}
		}
		return false
	}

	my_go_instr := p.cgn.callersite[0].goInstr
	if my_go_instr == go_instr {
		return true
	}
	if p.cgn.actualCallerSite == nil {
		return false
	}
	//double check actualCallerSite
	for _, actualCS := range p.cgn.actualCallerSite {
		if actualCS == nil || actualCS[0] == nil {
			continue
		}
		actual_go_instr := actualCS[0].goInstr
		if actual_go_instr == go_instr {
			return true
		}
	}
	return false
}

//bz: whether goID is match with the contexts in this pointer + loopID
//Update: go_instr can be nil for main go routine
//TODO: this does not match parent context if callsite.length > 1 (k > 1)
func (p PointerWCtx) MatchMyContextWithLoopID(go_instr *ssa.Go, loopID int, testMainCtx []*callsite) bool {
	if p.cgn == nil || p.cgn.callersite == nil || p.cgn.callersite[0] == nil {
		if go_instr == nil && loopID == 0 { //shared contour ~> when using test as entry
			return true
		}
		return false
	}
	if go_instr == nil { //when wants main context
		if p.a.isMain { //when using main as entry
			if p.cgn.callersite[0].targets == p.a.result.main.callersite[0].targets {
				return true
			}
		} else { //when using test as entry
			if p.cgn.callersite[0].targets == testMainCtx[0].targets {
				return true
			}
		}
		return false
	}

	my_cs := p.cgn.callersite[0]
	my_go_instr := my_cs.goInstr
	if my_go_instr == go_instr {
		if loopID == 0 {
			return true //no loop
		} else if my_cs.loopID == loopID {
			return true //loop id matched
		} else {
			return false //loop id not matched
		}
	}
	if p.cgn.actualCallerSite == nil {
		return false
	}
	//double check actualCallerSite
	for _, actualCS := range p.cgn.actualCallerSite {
		if actualCS == nil || actualCS[0] == nil {
			continue
		}
		actual_go_instr := actualCS[0].goInstr
		if actual_go_instr == go_instr {
			if loopID == 0 {
				return true //no loop
			} else if actualCS[0].loopID == loopID {
				return true //loop id matched
			}
		}
	}
	return false //loop id not matched
}

//bz: return the context of cgn which calls setValueNode() to record this pointer;
func (p PointerWCtx) GetMyContext() []*callsite {
	return p.cgn.callersite
}

//bz: user API, expose to user
type GoLoopID struct {
	GoInstr *ssa.Go
	LoopID  int
}

//bz: return the goInstruction and loopID of the context of cgn (1st *callsite)
//    return value can be nil if context is shared contour or pts == empty
func (p PointerWCtx) GetMyGoAndLoopID() *GoLoopID {
	if p.cgn == nil || p.cgn.callersite == nil || p.cgn.callersite[0] == nil {
		return &GoLoopID{
			GoInstr: nil,
			LoopID:  -1,
		}
	}
	cs := p.cgn.callersite[0]
	return &GoLoopID{
		GoInstr: cs.goInstr,
		LoopID:  cs.loopID,
	}
}

//bz: add ctx
func (p PointerWCtx) String() string {
	if p.cgn == nil {
		return fmt.Sprintf("n%d&(Global/Local)", p.n)
	}
	if p.cgn.actualCallerSite == nil {
		return fmt.Sprintf("n%d&%s", p.n, p.cgn.contourkFull())
	} else {
		return fmt.Sprintf("n%d&%s%s", p.n, p.cgn.contourkFull(), p.cgn.contourkActualFull())
	}
}

// PointsTo returns the points-to set of this pointer.
func (p PointerWCtx) PointsTo() PointsToSet {
	if p.n == 0 {
		return PointsToSet{}
	}
	return PointsToSet{p.a, &p.a.nodes[p.n].solve.pts}
}

// MayAlias reports whether the receiver pointer may alias
// the argument pointer.  --> same as Intersects()
func (p PointerWCtx) MayAlias(q PointerWCtx) bool {
	return p.PointsTo().Intersects(q.PointsTo())
}

// DynamicTypes returns p.PointsTo().DynamicTypes().
func (p PointerWCtx) DynamicTypes() *typeutil.Map {
	return p.PointsTo().DynamicTypes()
}
