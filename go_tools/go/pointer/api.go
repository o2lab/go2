// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pointer

import (
	"bytes"
	"fmt"
	"github.tamu.edu/April1989/go_tools/container/intsets"
	"github.tamu.edu/April1989/go_tools/go/callgraph"
	"github.tamu.edu/April1989/go_tools/go/ssa"
	"github.tamu.edu/April1989/go_tools/go/types/typeutil"
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
	K              int      //how many level? the most recent callsite/origin?
	LimitScope     bool     //only apply kcfa to app methods
	DEBUG          bool     //print out debug info
	Scope          []string //analyzed scope -> from user input: -path
	Exclusion      []string //excluded packages from this analysis -> from race_checker if any
	DiscardQueries bool     //bz: do not use queries, but keep every pts info in *cgnode
	UseQueriesAPI  bool     //bz: change the api the same as default pta
	TrackMore      bool     //bz: track pointers with types declared in Analyze Scope

	imports       []string //bz: internal use: store all import pkgs in a main
	Level         int      //bz: level == 0: traverse all app and lib, but with different ctx; level == 1: traverse 1 level lib call; level == 2: traverse 2 leve lib calls; no other option now
	DoPerformance bool     //bz: if we output performance related info
}

//bz: user API: race checker, added when ptaconfig.Level == 2
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
//
// See Config for how to request the various Result components.
//
type Result struct {
	a         *analysis        // bz: for debug
	CallGraph *callgraph.Graph // discovered call graph
	////bz: default
	//Queries         map[ssa.Value]Pointer // pts(v) for each v in Config.Queries.
	//IndirectQueries map[ssa.Value]Pointer // pts(*v) for each v in Config.IndirectQueries.
	//bz: we replaced default to include context
	Queries         map[ssa.Value][]PointerWCtx // pts(v) for each v in setValueNode().
	IndirectQueries map[ssa.Value][]PointerWCtx // pts(*v) for each v in setValueNode().
	Warnings        []Warning                   // warnings of unsoundness
}

//bz: same as default , but we want contexts
type ResultWCtx struct {
	a         *analysis  // bz: we need a lot from here...
	main      *cgnode    // bz: the cgnode for main method
	CallGraph *GraphWCtx // discovered call graph

	//bz: if DiscardQueries the following will be empty
	Queries         map[ssa.Value][]PointerWCtx // pts(v) for each v in setValueNode().
	IndirectQueries map[ssa.Value][]PointerWCtx // pts(*v) for each v in setValueNode().
	GlobalQueries   map[ssa.Value][]PointerWCtx // pts(v) for each freevar in setValueNode().
	ExtendedQueries map[ssa.Value][]PointerWCtx
	Warnings        []Warning // warnings of unsoundness

	DEBUG          bool // bz: print out debug info ...
	DiscardQueries bool // bz: do not use queries, but keep every pts info in *cgnode
	UseQueriesAPI  bool //bz: change the api the same as default pta
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
func (r *ResultWCtx) getInvokeFunc(call *ssa.Call, pointer PointerWCtx, goInstr *ssa.Go) *ssa.Function {
	fn := call.Parent()
	cgn := r.getCGNodebyFuncGoInstr(fn, goInstr)
	if cgn == nil {
		return nil
	}
	caller := r.CallGraph.GetNodeWCtx(cgn)
	for _, out := range caller.Out {
		if call == out.Site {
			return out.Callee.GetFunc()
		}
	}
	return nil
}

//bz: user API: tmp solution for missing invoke callee target if func wrapped in parameters
//alloc should be a freevar
func (r *ResultWCtx) getFreeVarFunc(alloc *ssa.Alloc, call *ssa.Call, goInstr *ssa.Go) *ssa.Function {
	val, _ := call.Common().Value.(*ssa.UnOp)
	freeV := val.X //this should be the free var of func
	pointers := r.pointsToFreeVar(freeV)
	p := pointers[0].PointsTo() //here should be only one element
	a := p.a
	pts := p.pts

	for { //recursively find the func body, since it can be assigned multiple times...
		if pts.Len() > 1 {
			if r.DEBUG {
				fmt.Println(" ****  Pointer Analysis: " + freeV.String() + " has multiple targets **** ") //panic
			}
		}
		nid := pts.Min()
		n := a.nodes[nid]
		pts = &n.solve.pts
		if pts.IsEmpty() { //TODO: bz: this maybe right maybe wrong ....
			return n.obj.cgn.fn
		} //else: continue to find...
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
func (r *ResultWCtx) pointsTo(v ssa.Value) []PointerWCtx {
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
		if pts.MatchMyContext(goInstr) {
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

//bz: return []PointerWCtx for query and indirect query and extended query
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
		if pointers != nil {
			return pointers
		}
	} else if freev, ok := v.(*ssa.FreeVar); ok {
		pointers := r.GlobalQueries[freev]
		if pointers != nil {
			return pointers
		}
	} else if op, ok := v.(*ssa.UnOp); ok {
		pointers := r.GlobalQueries[op.X] //bz: X is the freeVar
		if pointers != nil {
			return pointers
		}
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
	//fn
	reaches := make(map[*ssa.Function]*ssa.Function)
	implicits := make(map[*ssa.Function]*ssa.Function)

	var checks []*Edge
	//start from root
	root := r.CallGraph.Root
	reaches[root.GetFunc()] = root.GetFunc()
	reachCGs[root.ID] = root.ID
	for _, out := range root.Out {
		checks = append(checks, out)
	}

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
		}
		checks = tmp
	}

	//collect implicits
	for _, node := range r.CallGraph.Nodes {
		if _, ok := reachCGs[node.ID]; ok { //reached
			if _, ok2 := reaches[node.GetFunc()]; ok2 {
				continue //already stored
			}
			reaches[node.GetFunc()] = node.GetFunc()
			continue
		} else {
			//these functions in implicits, but are actually reached.
			//HOWEVER, it is not direct function call,
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
			//===>>>> no call edge created for this ...
			if _, ok2 := implicits[node.GetFunc()]; ok2 {
				continue //already stored
			}
			implicits[node.GetFunc()] = node.GetFunc()
		}
	}

	//how many of implicits are from preGens?
	cg := r.CallGraph
	prenodes := make(map[int]int)
	preFuncs := make(map[*ssa.Function]*ssa.Function) //& how many of functions are there in prenodes?
	for _, preGen := range preGens {
		precgnodes := cg.Fn2CGNode[preGen]
		for _, precgn := range precgnodes {
			if precgn.callersite[0] != nil { //this is not shared contour, not from pregen, but from origin call chain
				continue
			}
			node := cg.GetNodeWCtx(precgn)
			prenodes[node.ID] = node.ID
			preFuncs[node.GetFunc()] = node.GetFunc()
			for _, out := range node.Out {
				checks = append(checks, out)
			}
		}
	}

	if doDetail { //bz: print out all details
		//fmt.Println("\n\nImplicit Functions: ")
		//for _, unreach := range implicits {
		//	fmt.Println(unreach)
		//}

		fmt.Println("\n\nPre Generated Functions: ")
		for _, prefunc := range preFuncs {
			fmt.Println(prefunc)
		}
	}

	return reaches, implicits, reachCGs, prenodes, preFuncs
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

	if r.DiscardQueries {
		return //nothing in queries
	}

	fmt.Println("\nWe are going to print out queries. If not desired, turn off DEBUG.")
	queries := r.Queries
	inQueries := r.IndirectQueries
	exQueries := r.ExtendedQueries
	globalQueries := r.GlobalQueries
	fmt.Println("#Queries: " + strconv.Itoa(len(queries)) + "  #Indirect Queries: " + strconv.Itoa(len(inQueries)) +
		"  #Extended Queries: " + strconv.Itoa(len(exQueries)) +
		"  #Global Queries: " + strconv.Itoa(len(globalQueries)))

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

	fmt.Println("\nExtended Queries Detail: ")
	for v, ps := range exQueries {
		for _, p := range ps { //p -> types.Pointer: includes its context
			fmt.Println(p.String() + " (SSA:" + v.String() + "): {" + p.PointsTo().String() + "}")
		}
	}

	fmt.Println("\nGlobal Queries Detail: ")
	for v, ps := range globalQueries {
		for _, p := range ps { //p -> types.Pointer: includes its context
			fmt.Println(p.String() + " (SSA:" + v.String() + "): {" + p.PointsTo().String() + "}")
		}
	}
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

	//bz: also a reference of how to use new APIs here
	_result := r.a.result
	main := _result.getMain()
	fmt.Println("Main CGNode: " + main.String())

	fmt.Println("\nWe are going to print out call graph. If not desired, turn off DEBUG.")
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

//bz: do comparison with default
func (r *Result) GetResult() *ResultWCtx {
	return r.a.result
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
func (p PointerWCtx) MatchMyContext(go_instr *ssa.Go) bool {
	if p.cgn == nil || p.cgn.callersite == nil || p.cgn.callersite[0] == nil {
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
		actual_go_instr := actualCS[0].goInstr
		if actual_go_instr == go_instr {
			return true
		}
	}
	return false
}

//bz: return the context of cgn which calls setValueNode() to record this pointer;
func (p PointerWCtx) GetMyContext() []*callsite {
	return p.cgn.callersite
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
