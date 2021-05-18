// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pointer

// This file defines the constraint generation phase.

// TODO(adonovan): move the constraint definitions and the store() etc
// functions which add them (and are also used by the solver) into a
// new file, constraints.go.

import (
	"fmt"
	"github.com/april1989/origin-go-tools/container/intsets"
	"github.com/april1989/origin-go-tools/go/myutil/flags"
	"github.com/april1989/origin-go-tools/go/ssa"
	"go/token"
	"go/types"
	"math"
	"strconv"
	"strings"
)

var (
	tEface     = types.NewInterfaceType(nil, nil).Complete()
	tInvalid   = types.Typ[types.Invalid]
	tUnsafePtr = types.Typ[types.UnsafePointer]
)

//bz: NOT USED NOW.
// used when DoCollapse = true: collapse the context of these functions, because they are used too much in a program
// the following order is the descendent order of usage frequency of the function
var collapseFns = [...]string{
	//from tidb
	"sort.Slice",
	"sort.Search",
	"sort.SliceStable",
	"(*sync.Once).Do",
	"github.com/pingcap/parser/terror.Call",
	"(*golang.org/x/sync/singleflight.Group).Do",
	"strings.TrimLeftFunc",
}

//var origins []string //bz: debug: record the origin cgnode

// ---------- Node creation ----------

// nextNode returns the index of the next unused node.
func (a *analysis) nextNode() nodeid {
	return nodeid(len(a.nodes))
}

// addNodes creates nodes for all scalar elements in type typ, and
// returns the id of the first one, or zero if the type was
// analytically uninteresting.
//
// comment explains the origin of the nodes, as a debugging aid.
//
func (a *analysis) addNodes(typ types.Type, comment string) nodeid {
	id := a.nextNode()
	for _, fi := range a.flatten(typ) { //bz: fi is types.Pointer or type.Pointer.*; only create for them
		a.addOneNode(fi.typ, comment, fi)
	}
	if id == a.nextNode() {
		return 0 // type contained no pointers
	}
	return id
}

// addOneNode creates a single node with type typ, and returns its id.
//
// typ should generally be scalar (except for tagged.T nodes
// and struct/array identity nodes).  Use addNodes for non-scalar types.
//
// comment explains the origin of the nodes, as a debugging aid.
// subelement indicates the subelement, e.g. ".a.b[*].c".
//
func (a *analysis) addOneNode(typ types.Type, comment string, subelement *fieldInfo) nodeid {
	id := a.nextNode()
	a.nodes = append(a.nodes, &node{typ: typ, subelement: subelement, solve: new(solverState)})
	if a.log != nil {
		fmt.Fprintf(a.log, "\tcreate n%d %s for %s%s\n",
			id, typ, comment, subelement.path())
	}
	return id
}

// setValueNode associates node id with the value v.
// cgn identifies the context iff v is a local variable.
func (a *analysis) setValueNode(v ssa.Value, id nodeid, cgn *cgnode) {
	if cgn != nil { // bz: both a.localval and a.globalval; will be nullify at the end of genFunc()
		a.localval[v] = id
	} else {
		a.globalval[v] = id
	}
	if a.log != nil {
		fmt.Fprintf(a.log, "\tval[%s] = n%d  (%T)\n", v.Name(), id, v)
	}

	return //bz: skip recording queries

	//// Default: Due to context-sensitivity, we may encounter the same Value
	//// in many contexts. We merge them to a canonical node, since
	//// that's what all clients want.
	//// Record the (v, id) relation if the client has queried pts(v).
	////!!!! bz : this part is evil ... they may considered the performance issue,
	//// BUT we want to directly query after running pointer analysis, not run after each query...
	//// from the code@https://github.tamu.edu/jeffhuang/go2/blob/master/race_checker/pointerAnalysis.go
	//// seems like we only query pointers, so CURRENTLY only record for pointers in app methods
	//// -> go to commit@acb4db0349f131f8d10ddbec6d4fb686258becca (or comment out below for now)
	//// to check original code
	////HOWEVER, it is too heavy to create each query another copy constraint, discarded.
	//t := v.Type()
	//if cgn == nil {
	//	if !withinScope {
	//		return // not interested
	//	}
	//	//bz: this might be the root cgn, interface, from global, etc.
	//	//NOW, put the a.globalobj[] also into query, since a lot of thing is stored there, e.g.,
	//	//*ssa.FreeVar (but I PERSONALLY do not want *ssa.Function, *ssa.Global, *ssa.Function, *ssa.Const,
	//	//exclude now)
	//	if a.log != nil {
	//		fmt.Fprintf(a.log, "nil cgn in setValueNode(): v:"+v.Type().String()+" "+v.String()+"\n")
	//	}
	//	switch v.(type) {
	//	case *ssa.FreeVar:
	//		//bz: Global object. But are they unique mapping/replaced when put into a.globalval[]?
	//		// a.globalobj[v] = n0  --> nothing stored, do not use this
	//		a.recordGlobalQueries(t, cgn, v, id)
	//	case *ssa.Global:
	//		//Updated: bz: capture global var, e.g., race_checker/tests/runc_simple.go:31
	//		a.recordGlobalQueries(t, cgn, v, id)
	//	}
	//	return //else: nothing to record
	//}
	//
	//if a.withinScope(cgn.fn.String()) { //record queries
	//	//if a.config.DEBUG {
	//	//	fmt.Println("query (in): " + t.String())
	//	//}
	//	if CanPoint(t) {
	//		a.recordQueries(t, cgn, v, id)
	//	} else
	//	//bz: this condition is copied from go2: indirect queries
	//	if underType, ok := v.Type().Underlying().(*types.Pointer); ok && CanPoint(underType.Elem()) {
	//		a.recordIndirectQueries(t, cgn, v, id)
	//	} else { //bz: extended queries for debug --> might be global (cgn == nil) or local (cgn != nil)
	//		a.recordExtendedQueries(t, cgn, v, id)
	//	}
	//}
}

func (a *analysis) recordExtendedQueries(t types.Type, v ssa.Value, id nodeid) {
	ptr := PointerWCtx{a, a.addNodes(t, "query.extended"), nil}
	ptrs, ok := a.result.ExtendedQueries[v]
	if !ok {
		// First time?  Create the canonical query node.
		ptrs = make([]PointerWCtx, 1)
		ptrs[0] = ptr
	} else {
		ptrs = append(ptrs, ptr)
	}
	a.result.ExtendedQueries[v] = ptrs
	a.copy(ptr.n, id, a.sizeof(t))
}

//bz: utility func to record global queries
func (a *analysis) recordGlobalQueries(t types.Type, v ssa.Value, id nodeid) {
	ptr := PointerWCtx{a, a.addNodes(t, "query.global"), nil}
	ptrs, ok := a.result.GlobalQueries[v]
	if !ok {
		// First time?  Create the canonical query node.
		ptrs = make([]PointerWCtx, 1)
		ptrs[0] = ptr
	} else {
		ptrs = append(ptrs, ptr)
	}
	a.result.GlobalQueries[v] = ptrs
	a.copy(ptr.n, id, a.sizeof(t))
}

//bz: utility func to record indirect queries
func (a *analysis) recordIndirectQueries(t types.Type, cgn *cgnode, v ssa.Value, id nodeid) {
	ptr := PointerWCtx{a, a.addNodes(v.Type(), "query.indirect"), cgn}
	ptrs, ok := a.result.IndirectQueries[v]
	if !ok {
		// First time? Create the canonical indirect query node.
		ptrs = make([]PointerWCtx, 1)
		ptrs[0] = ptr
	} else {
		ptrs = append(ptrs, ptr)
	}
	a.result.IndirectQueries[v] = ptrs
	//a.genLoad(cgn, ptr.n, v, 0, a.sizeof(t)) //bz: original code -> cause empty pts; updated to copy below.
	a.copy(ptr.n, id, a.sizeof(t))
}

//bz: utility func to record queries
func (a *analysis) recordQueries(t types.Type, cgn *cgnode, v ssa.Value, id nodeid) {
	ptr := PointerWCtx{a, a.addNodes(t, "query.direct"), cgn}
	ptrs, ok := a.result.Queries[v]
	if !ok {
		// First time?  Create the canonical query node.
		ptrs = make([]PointerWCtx, 1)
		ptrs[0] = ptr
	} else {
		ptrs = append(ptrs, ptr)
	}
	a.result.Queries[v] = ptrs
	a.copy(ptr.n, id, a.sizeof(t))
}

// endObject marks the end of a sequence of calls to addNodes denoting
// a single object allocation.
//
// obj is the start node of the object, from a prior call to nextNode.
// Its size, flags and optional data will be updated.
//
func (a *analysis) endObject(obj nodeid, cgn *cgnode, data interface{}) *object {
	// Ensure object is non-empty by padding;
	// the pad will be the object node.
	size := uint32(a.nextNode() - obj)
	if size == 0 {
		a.addOneNode(tInvalid, "padding", nil)
	}
	objNode := a.nodes[obj]
	o := &object{
		size: size, // excludes padding
		cgn:  cgn,  //here has the context if needed
		data: data,
	}
	objNode.obj = o //bz: points-to heap; this is the only place that assigned to node.obj

	return o
}

// makeFunctionObject creates and returns a new function object
// (contour) for fn, and returns the id of its first node.  It also
// enqueues fn for subsequent constraint generation.
//
// For a context-sensitive contour, callersite identifies the sole
// callsite; for shared contours, caller is nil.
//
//bz: except the caller from genInvokeReflectType(), all others will store this obj in
//    a.globalobj with *ssa.Function as the key, we can use this to retrieve existing pts info
func (a *analysis) makeFunctionObject(fn *ssa.Function, callersite *callsite) nodeid {
	if a.log != nil {
		fmt.Fprintf(a.log, "\t---- makeFunctionObject %s\n", fn)
	}

	// obj is the function object (identity, params, results).
	obj := a.nextNode()
	cgn := a.makeCGNode(fn, obj, callersite)

	if !a.isMain && a.isGoTestForm(fn.Name()) {
		//bz: we create a new context for this test -> in order to separate call edges
		//TODO: use itself as target? this pattern only works for grpc, not sure others
		special := &callsite{targets: obj}
		cgn.callersite = a.createSingleCallSite(special)
	}

	sig := fn.Signature
	a.addOneNode(sig, "func.cgnode", nil) // (scalar with Signature type)
	if recv := sig.Recv(); recv != nil {
		a.addNodes(recv.Type(), "func.recv")
	}
	a.addNodes(sig.Params(), "func.params")
	a.addNodes(sig.Results(), "func.results")
	a.endObject(obj, cgn, fn).flags |= otFunction

	if a.log != nil {
		fmt.Fprintf(a.log, "\t----\n")
	}

	// Queue it up for constraint processing.
	a.genq = append(a.genq, cgn)

	return obj
}

//bz: we make function body nodes and param/result nodes together, instead of calling makeCGNode
//because for kcfa, we might have multiple cgnodes for fn
//fn -> target; obj -> for fn cgnode; callersite -> callsite of fn, and is nil for makeclosure
//bz: adjust for origin-sensitive
//TODO: Precision Problem: since go ssa instruction only record call instruction but no program counter,
//      two calls (no param and return value) to the same target within one method cannot be distinguished ...
//      e.g., go2/race_checker/GoBench/Kubernetes/88331/main.go: func NewPriorityQueue() *PriorityQueue {...}
//      bz: DID I FIX THIS??
func (a *analysis) makeFunctionObjectWithContext(caller *cgnode, fn *ssa.Function, callersite *callsite, closure *ssa.MakeClosure, loopID int) (nodeid, bool) {
	if a.log != nil {
		fmt.Fprintf(a.log, "\t---- makeFunctionObjectWithContext (kcfa) %s\n", fn)
	}
	//if a.config.DEBUG {
	//	fmt.Printf("\t---- makeFunctionObjectWithContext (kcfa) for %s\n", fn)
	//}

	if a.config.Origin && callersite == nil && closure != nil {
		//origin: case 2: fn is wrapped by make closure -> we checked before calling this, now needs to create it
		// and will update the a.closures[] outside
		// !! for origin, this is the only place triggering context switch !!
		obj, _ := a.makeCGNodeAndRelated(fn, caller, nil, closure, loopID) //create new one
		return obj, true
	}

	//if we can find an existing cgnode/obj -> update: remove the duplicate cgnode added to a.genq[]
	var existCGNIdx []int
	var multiFn bool
	var existNodeID nodeid
	var isNew bool
	if a.config.Origin { //bz: origin -> we only create new callsite[] for specific instr, not everyone
		assert(callersite != nil, "Unexpected nil callersite.instr @ makeFunctionObjectWithContext().")

		existCGNIdx, multiFn, existNodeID, isNew = a.existContext(fn, callersite, caller, loopID)
		if !isNew {
			return existNodeID, isNew
		}
	} else if a.config.CallSiteSensitive { //bz: for kcfa
		existCGNIdx, multiFn, existNodeID, isNew = a.existContextForComb(fn, callersite, caller, -1)
		if !isNew {
			return existNodeID, isNew
		}
	} else {
		panic("NO SELECTED MY CONTEXT IN a.config. GO SELECT ONE.")
	}

	//create a callee for THIS caller context if available, not every caller context
	var newFnIdx = make([]int, 1)
	obj, fnIdx := a.makeCGNodeAndRelated(fn, caller, callersite, nil, loopID)

	//update
	newFnIdx[0] = fnIdx

	//update fn2cgnodeIdx
	a.updateFn2NodeID(fn, multiFn, newFnIdx, existCGNIdx)

	return obj, true
}

//bz: a summary of existContextXXX(); if loopID = -1 -> loopID is not needed; return isNew
func (a *analysis) existContext(fn *ssa.Function, callersite *callsite, caller *cgnode, loopID int) ([]int, bool, nodeid, bool) {
	if _, ok := callersite.instr.(*ssa.Go); callersite != nil && ok {
		return a.existContextForComb(fn, callersite, caller, loopID)
	} else {
		return a.existContextFor(fn, caller)
	}
}

//bz: if exist this caller for fn? return isNew
func (a *analysis) existContextFor(fn *ssa.Function, caller *cgnode) ([]int, bool, nodeid, bool) {
	existCGNIdx, multiFn := a.fn2cgnodeIdx[fn]
	if multiFn { //check if we already have the caller + callsite ?? recursive/duplicate call
		for i, existIdx := range existCGNIdx { // idx -> index of fn cgnode in a.cgnodes[]
			existCGNode := a.cgnodes[existIdx]
			if equalCallSite(existCGNode.callersite, caller.callersite) { //check all callsites
				//duplicate combination, return this
				if a.log != nil { //debug
					fmt.Fprintf(a.log, "    EXIST**: "+strconv.Itoa(i+1)+"th: K-CALLSITE -- "+existCGNode.contourkFull()+"\n")
				}
				if a.config.DEBUG {
					fmt.Printf("    EXIST**: " + strconv.Itoa(i+1) + "th: K-CALLSITE -- " + existCGNode.contourkFull() + "\n")
				}
				//only one match here, create a wrapper for existIdx
				idxes := make([]int, 1)
				idxes[0] = existIdx
				return idxes, multiFn, existCGNode.obj, false
			}
		}
	}
	return existCGNIdx, multiFn, 0, true
}

//bz: if exist this callersite (with loopID) + caller for fn? return isNew
func (a *analysis) existContextForComb(fn *ssa.Function, callersite *callsite, caller *cgnode, loopID int) ([]int, bool, nodeid, bool) {
	existCGNIdx, multiFn := a.fn2cgnodeIdx[fn]
	if multiFn { //check if we already have the caller + callsite ?? recursive/duplicate call
		for i, existIdx := range existCGNIdx { // idx -> index of fn cgnode in a.cgnodes[]
			_fnCGNode := a.cgnodes[existIdx]
			if a.equalContextForComb(_fnCGNode.callersite, callersite, caller.callersite, loopID) { //check all callsites
				//duplicate combination, return this
				if a.log != nil { //debug
					fmt.Fprintf(a.log, "    EXIST**: "+strconv.Itoa(i+1)+"th: K-CALLSITE -- "+_fnCGNode.contourkFull()+"\n")
				}
				if a.config.DEBUG {
					fmt.Printf("    EXIST**: " + strconv.Itoa(i+1) + "th: K-CALLSITE -- " + _fnCGNode.contourkFull() + "\n")
				}
				//only one match here, create a wrapper for existIdx
				idxes := make([]int, 1)
				idxes[0] = existIdx
				return idxes, multiFn, _fnCGNode.obj, false
			}
		}
	}
	return existCGNIdx, multiFn, 0, true
}

//bz: if two contexts are the same: existCSs == cur (with loopID) + curCallerCSs
func (a *analysis) equalContextForComb(existCSs []*callsite, cur *callsite, curCallerCSs []*callsite, loopID int) bool {
	for i, existCS := range existCSs {
		switch i {
		case 0: //[0] is the most recent; work for k = 1 only
			if loopID == 0 { //no loop
				return existCS.equal(cur)
			} else { //from a loop
				return cur.loopEqual(existCS) && existCS.loopID == loopID
			}
		default:
			if !existCS.equal(curCallerCSs[i-1]) {
				return false
			}
		}
	}
	return true
}

//bz: continue with makeFunctionObjectWithContext (kcfa), create cgnode for one caller context as well as its param/result
func (a *analysis) makeCGNodeAndRelated(fn *ssa.Function, caller *cgnode, callersite *callsite, closure *ssa.MakeClosure, loopID int) (nodeid, int) {
	// obj is the function object (identity, params, results).
	obj := a.nextNode()

	//doing task of makeCGNode()
	var cgn *cgnode
	//TODO: bz: this ifelse is a bit huge ....
	if a.config.Mains[0].Func("main") == fn { //bz: give the main method a context, instead of using shared contour
		single := a.createSingleCallSite(callersite)
		cgn = &cgnode{fn: fn, obj: obj, callersite: single}
	} else {                 // other functions
		if a.config.Origin { //bz: for origin-sensitive
			if callersite == nil { //we only create new context for make closure and go instruction
				var fnkcs []*callsite
				goInstr := a.isGoNext(closure)
				if goInstr != nil { //case 2: create one with only target, make closure is not ssa.CallInstruction
					special := &callsite{targets: obj, loopID: loopID, goInstr: goInstr}
					fnkcs = a.createKCallSite(caller.callersite, special)
				} else { // use parent context, since no go invoke afterwards (no go can be reachable currently at this point);
					//update: we will update the parent ctx (including loopID) after solving
					if !a.consumeMakeClosureNext(closure) { //only record if next instr is go -> context changes afterwards
						a.closureWOGo[obj] = obj //record
					}
					fnkcs = caller.callersite
				}
				cgn = &cgnode{fn: fn, obj: obj, callersite: fnkcs}
			} else if goInstr, ok := callersite.instr.(*ssa.Go); ok { //case 1 and 3: this is a *ssa.GO without closure
				special := callersite
				special.goInstr = goInstr //update
				if loopID > 0 {           //handle loop TODO: will this affect exist checking?
					special = &callsite{targets: callersite.targets, instr: callersite.instr, loopID: loopID, goInstr: goInstr}
				}
				fnkcs := a.createKCallSite(caller.callersite, special)
				cgn = &cgnode{fn: fn, obj: obj, callersite: fnkcs}
				a.numOrigins++ //only here really trigger the go

				//origins = append(origins, cgn.String())
				//fmt.Println(cgn.String()) //bz: debug
			} else { //use caller context
				cgn = &cgnode{fn: fn, obj: obj, callersite: caller.callersite}
			}
		} else if a.config.CallSiteSensitive { //bz: for kcfa
			if callersite == nil { //fn is make closure
				special := &callsite{targets: obj} //create one with only target, make closure is not ssa.CallInstruction
				fnkcs := a.createKCallSite(caller.callersite, special)
				cgn = &cgnode{fn: fn, obj: obj, callersite: fnkcs}
			} else if caller.callersite[0] == nil { //no caller context
				single := a.createSingleCallSite(callersite)
				cgn = &cgnode{fn: fn, obj: obj, callersite: single}
			} else {
				fnkcs := a.createKCallSite(caller.callersite, callersite)
				cgn = &cgnode{fn: fn, obj: obj, callersite: fnkcs}
			}
		} else {
			panic("NO SELECTED MY CONTEXT IN a.config. GO SELECT ONE.")
		}
	}

	fn.IsFromApp = true //if it reaches here, must be kcfa/origin, mark it
	if a.log != nil {   //debug
		fmt.Fprintf(a.log, "     K-CALLSITE -- "+cgn.contourkFull()+"\n")
	}
	if a.config.DEBUG {
		fmt.Printf("     K-CALLSITE -- " + cgn.contourkFull() + "\n")
	}

	a.cgnodes = append(a.cgnodes, cgn)
	fnIdx := len(a.cgnodes) - 1 // last element of a.cgnodes
	cgn.idx = fnIdx             //initialize -> only here

	//make param and result nodes
	a.makeParamResultNodes(fn, obj, cgn)

	// Queue it up for constraint processing.
	a.genq = append(a.genq, cgn)
	return obj, fnIdx
}

//bz: continue with makeFunctionObjectWithContext (kcfa), update a.fn2cgnodeIdx for fn
func (a *analysis) updateFn2NodeID(fn *ssa.Function, multiFn bool, newFnIdx []int, existFnIdx []int) {
	if multiFn { //update and add to fn2cgnodeIdx
		for _, fnIdx := range newFnIdx {
			if fnIdx != 0 {
				existFnIdx = append(existFnIdx, fnIdx)
			}
		}
		a.fn2cgnodeIdx[fn] = existFnIdx
	} else { //init and add to fn2cgnodeIdx
		a.fn2cgnodeIdx[fn] = newFnIdx
	}
}

//bz: continue with makeFunctionObjectWithContext (kcfa), we create the parameter/result nodes for fn
//doing things similar to makeFunctionObject() but after makeCGNode()
func (a *analysis) makeParamResultNodes(fn *ssa.Function, obj nodeid, cgn *cgnode) {
	sig := fn.Signature
	a.addOneNode(sig, "func.cgnode", nil) // (scalar with Signature type)
	if recv := sig.Recv(); recv != nil {
		a.addNodes(recv.Type(), "func.recv")
	}
	a.addNodes(sig.Params(), "func.params")
	a.addNodes(sig.Results(), "func.results")
	a.endObject(obj, cgn, fn).flags |= otFunction //bz: here marks the sig node as function

	if a.log != nil {
		fmt.Fprintf(a.log, "\t----\n")
	}
}

//bz: create a kcallsite array with a single element --> no new callsite created
func (a *analysis) createSingleCallSite(callersite *callsite) []*callsite {
	var slice = make([]*callsite, 1)
	slice[0] = callersite
	return slice
}

//bz: create a kcallsite array with a mix of caller and callee call sites  --> no new callsite created
func (a *analysis) createKCallSite(caller2sites []*callsite, callersite *callsite) []*callsite {
	k := a.config.K
	if k == 1 { //len(caller2sites) == 0 ||
		return a.createSingleCallSite(callersite)
	}
	//possible k call sites
	size := math.Min(float64(k), float64(1+len(caller2sites))) // take the smaller number
	var slice = make([]*callsite, int(size))
	for i, _ := range slice {
		if i == 0 {
			slice[i] = callersite
		} else {
			slice[i] = caller2sites[i-1] //bz: not sure about the idx
		}
	}
	return slice
}

// makeTagged creates a tagged object of type typ.
func (a *analysis) makeTagged(typ types.Type, cgn *cgnode, data interface{}) nodeid {
	obj := a.addOneNode(typ, "tagged.T", nil) // NB: type may be non-scalar!
	a.addNodes(typ, "tagged.v")
	a.endObject(obj, cgn, data).flags |= otTagged
	return obj
}

// makeRtype returns the canonical tagged object of type *rtype whose
// payload points to the sole rtype object for T.
//
// TODO(adonovan): move to reflect.go; it's part of the solver really.
//
func (a *analysis) makeRtype(T types.Type) nodeid {
	if v := a.rtypes.At(T); v != nil {
		return v.(nodeid)
	}

	// Create the object for the reflect.rtype itself, which is
	// ordinarily a large struct but here a single node will do.
	obj := a.nextNode()
	a.addOneNode(T, "reflect.rtype", nil)
	a.endObject(obj, nil, T)

	id := a.makeTagged(a.reflectRtypePtr, nil, T)
	a.nodes[id+1].typ = T // trick (each *rtype tagged object is a singleton)
	a.addressOf(a.reflectRtypePtr, id+1, obj)

	a.rtypes.Set(T, id)
	return id
}

// rtypeValue returns the type of the *reflect.rtype-tagged object obj.
func (a *analysis) rtypeTaggedValue(obj nodeid) types.Type {
	tDyn, t, _ := a.taggedValue(obj)
	if tDyn != a.reflectRtypePtr {
		panic(fmt.Sprintf("not a *reflect.rtype-tagged object: obj=n%d tag=%v payload=n%d", obj, tDyn, t))
	}
	return a.nodes[t].typ
}

// valueNode returns the id of the value node for v, creating it (and
// the association) as needed.  It may return zero for uninteresting
// values containing no pointers.
//
func (a *analysis) valueNode(v ssa.Value) nodeid {
	// Value nodes for locals are created en masse (== in a group) by genFunc.
	if id, ok := a.localval[v]; ok {
		return id
	}

	// Value nodes for globals are created on demand.
	id, ok := a.globalval[v]
	if !ok {
		var comment string
		if a.log != nil {
			comment = v.String()
		}
		id = a.addNodes(v.Type(), comment)
		if obj := a.objectNode(nil, v); obj != 0 {
			a.addressOf(v.Type(), id, obj)
		}
		a.setValueNode(v, id, nil)
	}
	return id
}

// valueOffsetNode ascertains the node for tuple/struct value v,
// then returns the node for its subfield #index.
//
func (a *analysis) valueOffsetNode(v ssa.Value, index int) nodeid {
	id := a.valueNode(v)
	if id == 0 {
		panic(fmt.Sprintf("cannot offset within n0: %s = %s", v.Name(), v))
	}
	return id + nodeid(a.offsetOf(v.Type(), index))
}

// isTaggedObject reports whether object obj is a tagged object.
func (a *analysis) isTaggedObject(obj nodeid) bool {
	return a.nodes[obj].obj.flags&otTagged != 0
}

// taggedValue returns the dynamic type tag, the (first node of the)
// payload, and the indirect flag of the tagged object starting at id.
// Panic ensues if !isTaggedObject(id).
//
func (a *analysis) taggedValue(obj nodeid) (tDyn types.Type, v nodeid, indirect bool) {
	n := a.nodes[obj]
	flags := n.obj.flags
	if flags&otTagged == 0 {
		panic(fmt.Sprintf("not a tagged object: n%d = %s", obj, n))
	}
	return n.typ, obj + 1, flags&otIndirect != 0
}

//bz: added for rVCallConstraint, no panic; if not a function, reutrn nil
func (a *analysis) taggedFunc(obj nodeid) (tDyn types.Type) {
	n := a.nodes[obj]
	if n.obj == nil || n.obj.flags&otFunction == 0 {
		if a.log != nil { //debug
			fmt.Fprintf(a.log, "not a tagged function: n", obj, "=", n)
		}
		//panic(fmt.Sprintf("not a tagged function: n%d = %s", obj, n))
		return nil
	}
	return n.typ
}

// funcParams returns the first node of the params (P) block of the
// function whose object node (obj.flags&otFunction) is id.
//
func (a *analysis) funcParams(id nodeid) nodeid {
	n := a.nodes[id]
	if n.obj == nil || n.obj.flags&otFunction == 0 {
		panic(fmt.Sprintf("funcParams(n%d): not a function object block", id))
	}
	return id + 1
}

// funcResults returns the first node of the results (R) block of the
// function whose object node (obj.flags&otFunction) is id.
//
func (a *analysis) funcResults(id nodeid) nodeid {
	n := a.nodes[id]
	if n.obj == nil || n.obj.flags&otFunction == 0 {
		panic(fmt.Sprintf("funcResults(n%d): not a function object block", id))
	}
	sig := n.typ.(*types.Signature)
	id += 1 + nodeid(a.sizeof(sig.Params()))
	if sig.Recv() != nil {
		id += nodeid(a.sizeof(sig.Recv().Type()))
	}
	return id
}

// ---------- Constraint creation ----------

// copy creates a constraint of the form dst = src.
// sizeof is the width (in logical fields) of the copied type.
//
func (a *analysis) copy(dst, src nodeid, sizeof uint32) {
	if src == dst || sizeof == 0 {
		return // trivial
	}
	if src == 0 || dst == 0 {
		panic(fmt.Sprintf("ill-typed copy dst=n%d src=n%d", dst, src))
	}
	for i := uint32(0); i < sizeof; i++ {
		a.addConstraint(&copyConstraint{dst, src})
		src++
		dst++
	}
}

// addressOf creates a constraint of the form id = &obj.
// T is the type of the address.
func (a *analysis) addressOf(T types.Type, id, obj nodeid) {
	if id == 0 {
		panic("addressOf: zero id")
	}
	if obj == 0 {
		panic("addressOf: zero obj")
	}

	if a.config.TrackMore {
		if a.isWithinScope || a.shouldTrack(T) {
			a.addConstraint(&addrConstraint{id, obj})
		}
	} else { //default
		if a.shouldTrack(T) {
			a.addConstraint(&addrConstraint{id, obj})
		}
	}
}

// load creates a load constraint of the form dst = src[offset].
// offset is the pointer offset in logical fields.
// sizeof is the width (in logical fields) of the loaded type.
//
func (a *analysis) load(dst, src nodeid, offset, sizeof uint32) {
	if dst == 0 {
		return // load of non-pointerlike value
	}
	if src == 0 && dst == 0 {
		return // non-pointerlike operation
	}
	if src == 0 || dst == 0 {
		panic(fmt.Sprintf("ill-typed load dst=n%d src=n%d", dst, src))
	}
	for i := uint32(0); i < sizeof; i++ {
		a.addConstraint(&loadConstraint{offset, dst, src})
		offset++
		dst++
	}
}

// store creates a store constraint of the form dst[offset] = src.
// offset is the pointer offset in logical fields.
// sizeof is the width (in logical fields) of the stored type.
//
func (a *analysis) store(dst, src nodeid, offset uint32, sizeof uint32) {
	if src == 0 {
		return // store of non-pointerlike value
	}
	if src == 0 && dst == 0 {
		return // non-pointerlike operation
	}
	if src == 0 || dst == 0 {
		panic(fmt.Sprintf("ill-typed store dst=n%d src=n%d", dst, src))
	}
	for i := uint32(0); i < sizeof; i++ {
		a.addConstraint(&storeConstraint{offset, dst, src})
		offset++
		src++
	}
}

// offsetAddr creates an offsetAddr constraint of the form dst = &src.#offset.
// offset is the field offset in logical fields.
// T is the type of the address.
//
func (a *analysis) offsetAddr(T types.Type, dst, src nodeid, offset uint32) {
	if a.config.TrackMore {
		if !a.isWithinScope && !a.shouldTrack(T) {
			return
		}
	} else { //default
		if !a.shouldTrack(T) {
			return
		}
	}

	if offset == 0 {
		// Simplify  dst = &src->f0
		//       to  dst = src
		// (NB: this optimisation is defeated by the identity
		// field prepended to struct and array objects.)
		a.copy(dst, src, 1)
	} else {
		a.addConstraint(&offsetAddrConstraint{offset, dst, src})
	}
}

// typeAssert creates a typeFilter or untag constraint of the form dst = src.(T):
// typeFilter for an interface, untag for a concrete type.
// The exact flag is specified as for untagConstraint.
//
func (a *analysis) typeAssert(T types.Type, dst, src nodeid, exact bool) {
	if isInterface(T) {
		a.addConstraint(&typeFilterConstraint{T, dst, src})
	} else {
		a.addConstraint(&untagConstraint{T, dst, src, exact})
	}
}

// addConstraint adds c to the constraint set.
func (a *analysis) addConstraint(c constraint) {
	a.constraints = append(a.constraints, c)
	if a.log != nil {
		fmt.Fprintf(a.log, "\t%s\n", c)
	}
}

// copyElems generates load/store constraints for *dst = *src,
// where src and dst are slices or *arrays.
//
func (a *analysis) copyElems(cgn *cgnode, typ types.Type, dst, src ssa.Value) {
	tmp := a.addNodes(typ, "copy")
	sz := a.sizeof(typ)
	a.genLoad(cgn, tmp, src, 1, sz)
	a.genStore(cgn, dst, tmp, 1, sz)
}

// ---------- Constraint generation ----------

// genConv generates constraints for the conversion operation conv.
func (a *analysis) genConv(conv *ssa.Convert, cgn *cgnode) {
	res := a.valueNode(conv)
	if res == 0 {
		return // result is non-pointerlike
	}

	tSrc := conv.X.Type()
	tDst := conv.Type()

	switch utSrc := tSrc.Underlying().(type) {
	case *types.Slice:
		// []byte/[]rune -> string?
		return

	case *types.Pointer:
		// *T -> unsafe.Pointer?
		if tDst.Underlying() == tUnsafePtr {
			return // we don't model unsafe aliasing (unsound)
		}

	case *types.Basic:
		switch tDst.Underlying().(type) {
		case *types.Pointer:
			// Treat unsafe.Pointer->*T conversions like
			// new(T) and create an unaliased object.
			if utSrc == tUnsafePtr {
				obj := a.addNodes(mustDeref(tDst), "unsafe.Pointer conversion")
				a.endObject(obj, cgn, conv)
				a.addressOf(tDst, res, obj)
				return
			}

		case *types.Slice:
			// string -> []byte/[]rune (or named aliases)?
			if utSrc.Info()&types.IsString != 0 {
				obj := a.addNodes(sliceToArray(tDst), "convert")
				a.endObject(obj, cgn, conv)
				a.addressOf(tDst, res, obj)
				return
			}

		case *types.Basic:
			// All basic-to-basic type conversions are no-ops.
			// This includes uintptr<->unsafe.Pointer conversions,
			// which we (unsoundly) ignore.
			return
		}
	}

	panic(fmt.Sprintf("illegal *ssa.Convert %s -> %s: %s", tSrc, tDst, conv.Parent()))
}

// genAppend generates constraints for a call to append.
func (a *analysis) genAppend(instr *ssa.Call, cgn *cgnode) {
	// Consider z = append(x, y).   y is optional.
	// This may allocate a new [1]T array; call its object w.
	// We get the following constraints:
	// 	z = x
	// 	z = &w
	//     *z = *y

	x := instr.Call.Args[0]

	z := instr
	a.copy(a.valueNode(z), a.valueNode(x), 1) // z = x

	if len(instr.Call.Args) == 1 {
		return // no allocation for z = append(x) or _ = append(x).
	}

	// TODO(adonovan): test append([]byte, ...string) []byte.

	y := instr.Call.Args[1]
	tArray := sliceToArray(instr.Call.Args[0].Type())

	w := a.nextNode()
	a.addNodes(tArray, "append")
	a.endObject(w, cgn, instr)

	a.copyElems(cgn, tArray.Elem(), z, y)        // *z = *y
	a.addressOf(instr.Type(), a.valueNode(z), w) //  z = &w
}

// genBuiltinCall generates constraints for a call to a built-in.
func (a *analysis) genBuiltinCall(instr ssa.CallInstruction, cgn *cgnode) {
	call := instr.Common()
	switch call.Value.(*ssa.Builtin).Name() {
	case "append":
		// Safe cast: append cannot appear in a go or defer statement.
		a.genAppend(instr.(*ssa.Call), cgn)

	case "copy":
		tElem := call.Args[0].Type().Underlying().(*types.Slice).Elem()
		a.copyElems(cgn, tElem, call.Args[0], call.Args[1])

	case "panic":
		a.copy(a.panicNode, a.valueNode(call.Args[0]), 1)

	case "recover":
		if v := instr.Value(); v != nil {
			a.copy(a.valueNode(v), a.panicNode, 1)
		}

	case "print":
		// In the tests, the probe might be the sole reference
		// to its arg, so make sure we create nodes for it.
		if len(call.Args) > 0 {
			a.valueNode(call.Args[0])
		}

	case "ssa:wrapnilchk":
		a.copy(a.valueNode(instr.Value()), a.valueNode(call.Args[0]), 1)

	default:
		// No-ops: close len cap real imag complex print println delete.
	}
}

// shouldUseContext defines the context-sensitivity policy.  It
// returns true if we should analyse all static calls to fn anew.
//
// Obviously this interface rather limits how much freedom we have to
// choose a policy.  The current policy, rather arbitrarily, is true
// for intrinsics and accessor methods (actually: short, single-block,
// call-free functions).  This is just a starting point.
//
func (a *analysis) shouldUseContext(fn *ssa.Function) bool {
	if a.findIntrinsic(fn) != nil {
		return true // treat intrinsics context-sensitively
	}
	if len(fn.Blocks) != 1 {
		return false // too expensive
	}
	blk := fn.Blocks[0]
	if len(blk.Instrs) > 10 {
		return false // too expensive
	}
	if fn.Synthetic != "" && (fn.Pkg == nil || fn != fn.Pkg.Func("init")) {
		return true // treat synthetic wrappers context-sensitively
	}
	for _, instr := range blk.Instrs {
		switch instr := instr.(type) {
		case ssa.CallInstruction:
			// Disallow function calls (except to built-ins)
			// because of the danger of unbounded recursion.
			if _, ok := instr.Common().Value.(*ssa.Builtin); !ok {
				return false
			}
		}
	}
	return true
}

//bz: do pta use our context-sensitive algo? which algo?
//if true, must be in scope. otherwise, use shared contour
func (a *analysis) considerMyContext(fn string) bool {
	return a.considerKCFA(fn) || a.considerOrigin(fn)
}

//bz: whether we do origin-sensitive on this fn
func (a *analysis) considerOrigin(fn string) bool {
	return a.config.Origin == true && a.withinScope(fn) //&& (caller.fn.IsFromApp || a.isMainMethod(caller.fn))
}

//bz: whether we do kcfa on this fn
func (a *analysis) considerKCFA(fn string) bool {
	return a.config.CallSiteSensitive == true && a.withinScope(fn) //&& (caller.fn.IsFromApp || a.isMainMethod(caller.fn))
}

//bz:  currently compare string, already considered LimitScope
func (a *analysis) withinScope(method string) bool {
	if a.config.LimitScope {
		// preprocess
		if string(method[0]) == "*" { // from collectFnsWScope(): pointer/named/interface types
			method = method[1:]
		} else if method[0:2] == "(*" { //this is a pointer type -> remove pointer
			method = method[2:]
		} else if string(method[0]) == "(" { //this is a wrapper bracket -> remove bracket
			method = method[1:]
		} else if len(method) > 8 && method[0:8] == "package " { //package github.com/ethereum/go-ethereum/common/fdlimit
			method = method[8:]
		}
		//compare: must be xxx.xxx.xx/xx
		if strings.HasPrefix(method, "command-line-arguments") || strings.HasPrefix(method, "main.") { //default scope or when running my tests in _test_main, _tests_callback
			return true
		} else {
			if len(a.config.Exclusion) > 0 { //user assigned exclusion -> bz: want to exclude all reflection ...
				for _, pkg := range a.config.Exclusion {
					if strings.HasPrefix(method, pkg) {
						return false
					}
				}
			}
			if len(a.config.Scope) > 0 { //project scope
				for _, pkg := range a.config.Scope {
					if strings.HasPrefix(method, pkg) {
						return true
					}
				}
			}
			//if len(a.config.imports) > 0 { //consider imports in scope
			//	for _, pkg := range a.config.imports {
			//		if strings.Contains(method, pkg) && !strings.Contains(method, "google.golang.org/grpc/grpclog") {
			//			return true
			//		}
			//	}
			//}
		}
		return false
	}
	return true
}

//bz: whether a func is from a.config.imports
func (a *analysis) fromImports(method string) bool {
	if a.config.LimitScope {
		if len(a.config.imports) > 0 { //user assigned scope
			for _, pkg := range a.config.imports {
				if strings.Contains(method, pkg) {
					return true
				}
			}
		}
		return false
	}
	return true
}

//bz: whether a func is from a.config.Exclusion
func (a *analysis) fromExclusion(fn *ssa.Function) bool {
	if a.config.LimitScope {
		if len(a.config.Exclusion) > 0 { //user assigned scope
			if fn.Pkg == nil || fn.Pkg.Pkg == nil {
				return false
			}
			for _, pkg := range a.config.Exclusion {
				if strings.Contains(fn.Pkg.Pkg.Path(), pkg) {
					return true
				}
			}
		}
		return false
	}
	return true
}

//bz: to check whether a go routine is inside a loop in fn; also record the result in *analysis
//TODO: what if fn has no loop, but its caller has loop enclosing fn?
//      we need to recursively check loop existence til reach main?
func (a *analysis) isInLoop(fn *ssa.Function, inst ssa.Instruction) bool {
	instbb := inst.Block()  //basic block of inst
	instIdx := instbb.Index // index of instbb in all bbs
	nextbbs := instbb.Succs //successers
	next := make(map[int]*ssa.BasicBlock)
	var total intsets.Sparse //all been traversed
	for _, nextbb := range nextbbs {
		next[nextbb.Index] = nextbb
		total.Insert(nextbb.Index)
	}
	tmp := make(map[int]*ssa.BasicBlock) //store for the successers of nextbbs
	for len(next) > 0 {
		for nextIdx, nextbb := range next {
			if nextIdx == instIdx { //see instbb again
				return true
			} else { //continue
				for _, nnbb := range nextbb.Succs {
					if !total.Has(nnbb.Index) {
						tmp[nnbb.Index] = nnbb   //next nextbb
						total.Insert(nnbb.Index) //record
					}
				}
			}
		}
		next = tmp
		tmp = make(map[int]*ssa.BasicBlock) //set map to empty
	}
	return false
}

//bz: which level of lib/app calls we consider: true -> create func/cgnode; false -> do not create
//scope: 1 < 2 < 3 < 0
func (a *analysis) createForLevelX(caller *ssa.Function, callee *ssa.Function) bool {
	switch a.config.Level {
	case 0: //bz: analyze all
		return true

	case 1:
		//bz: if callee is from app => 1 level
		if a.withinScope(callee.String()) {
			return true
		}

	case 2:
		//bz: parent of caller in app, caller in lib, callee also in lib
		// || parent in lib, caller in app, callee in lib || parent in lib, caller in lib, callee in app
		if caller == nil { //shared contour
			if a.withinScope(callee.String()) || a.fromImports(callee.String()) {
				return true //bz: as long as callee is app/path func, we do it
			}
			return false
		}

		parentCaller := caller.Parent()
		if parentCaller == nil { //bz: pkg initializer, no parent or other cases
			if a.withinScope(callee.String()) || a.fromImports(callee.String()) ||
				a.withinScope(caller.String()) || a.fromImports(caller.String()) {
				return true
			}
			return false
		}

		//parentcaller -> app; caller -> lib; callee -> lib  => 2 level
		if a.withinScope(parentCaller.String()) || a.withinScope(caller.String()) || a.withinScope(callee.String()) {
			return true
		}

	case 3:
		//bz: this analyze lib's import
		if caller == nil {
			if a.withinScope(callee.String()) || a.fromImports(callee.String()) {
				return true
			}
			return false
		}
		if a.withinScope(callee.String()) || a.fromImports(callee.String()) ||
			a.withinScope(caller.String()) || a.fromImports(caller.String()) {
			return true
		}
	default:
		//bz: default 0: this is really considering all, including lib's lib, lib's lib's lib, etc.
		return false
	}
	return false
}

//bz: experiment, do not want to write each fn signature in .yml
//   true -> if param wraps a function type and func is from app
//   also return the callback func (or pointer) and its corresponding arg (v here)
//return value order -> v, targetfn, bool
//TODO: NOW ASSUME only one function in params
func (a *analysis) hasFuncParam(call *ssa.CallCommon) (ssa.Value, ssa.Value, bool) {
	for _, arg := range call.Args { //which params is callback fn?
		switch v := arg.(type) {
		case *ssa.MakeClosure: //most cases: create a make closure and pass as param
			fn := v.Fn.(*ssa.Function)
			if fn != nil && fn.IsFromApp { //we must seen this closure before reaching this point
				return v, fn, true // if arg is callback fn and it is a function inside closure
			}

		//we might not seen the fn for the following cases, check if fn is in scope -> no then return
		case *ssa.Function: //e.g., _tests/main/cg_namefn.go, may be directly pass a func as param
			if a.withinScope(v.String()) {
				return v, v, true
			}

		case *ssa.TypeAssert: //maybe create func first, then long call chain (need to cast to interface),
			// finally pass to the caller and cast back
			if sig, ok := v.Type().(*types.Signature); ok {
				//from go1.15 doc: A Signature (*types.Signature) represents a (non-builtin) function or method type.
				//-> then this should be a function pointer; however, I need to create a ssa.instruction of type change in IR before creating this sequence
				//   of creating constraints
				//TODO: what is exactly the target func here?
				if a.withinScope(sig.String()) {
					return v, v, true //bz: THIS IS THE ONLY ONE THAT IS NOT OF TYPE *ssa.Function
				}
			}

		case *ssa.ChangeType: //e.g., _tests/main/cg_typefn.go
			fn, ok := v.X.(*ssa.Function)
			if ok && a.withinScope(fn.String()) {
				return v, fn, true
			}

		case *ssa.Call: //e.g., _tests/main/cg_long.go
			if fn, ok := v.Call.Value.(*ssa.Function); ok && fn.Signature.Results().Len() == 0 && a.withinScope(fn.String()) {
				//bz: special cases in google.golang.org/grpc/benchmark/server, the code is:
				//    tr, ok := trace.FromContext(stream.Context())
				//stream.Context() as a function is used as a parameter of FromContext,
				//but actually FromContext requires the return value of stream.Context() as parameter
				//but the generated ir is not clear here ...
				return v, fn, true
			}
		}
	}

	return nil, nil, false
}

//bz: link the callback here, return value can be nil
//caller -> app func; instr -> invoke; fn -> target lib func of invoke; targetFn -> callback func invoked by fn
//    fake a fn with one call site linked to the callback function
//    for callback from makecloser, it has been created already, but caller + context becomes different
//    -> tmp solution: we use the context of caller as the context of fakeCgn; assume they share the same ctx
//    the same fn might be invoked multiple times with different makeclosure callback function
//    -> we store the fakeFn created first time, then retrieve it next time;
//       this corresponding cgnode will be solved at the end when all callback functions are collected from the program
//    Further: the same fn might be invoked from different context ...
//Update: ASSUME the parameter together of the caller (of callback) with callback func also is the parameter to callback func
//     if callback needs any input
//Update: avoid redundant genInstr() that have been done in previous preSolve() loops
//     -> put all instructions in a preSolve loop in one basic block
//TODO: 1. why this change mess up the renumber phase?
func (a *analysis) genCallBack(caller *cgnode, instr ssa.CallInstruction, fn *ssa.Function, site *callsite, call *ssa.CallCommon) []nodeid {
	//relation: caller -(invoke)-> fn -(invoke)-> callback/targetFn
	v, targetFn, isCallback := a.hasFuncParam(call) //v -> arg that wrapping callback; targetFn -> callback fn
	if !isCallback {
		return nil //not callback or not in scope
	}

	if targetFn == nil {
		panic("No callback fn in *ssa.MakeClosure @" + call.String() + ". DEBUG or Adjust your callback.yml.")
	}

	if flags.DoCollapse {
		id := a.genCallBackCollapse(caller, instr, fn, site, call, targetFn, v)
		result := make([]nodeid, 1)
		result[0] = id
		return result
	}

	//the key of a.globalcb -> different callsite has different ir in fakeFn
	//for virtual function, we need to change the key to include receiver type
	key := fn.String() + "@" + caller.contourkFull()

	//bz: skip recursive relations between lib call <-> callback fn
	if a.recursiveCallback(fn, caller.callersite[0], targetFn) {
		return nil //skip it, already computed enough calls
	}

	//check if fake function has spawned its own go routine
	spawn := HasGoSpawn(fn)

	//check if fake function already exists
	fakeFn, okFn := a.globalcb[key] //check if fakeFn exist for this caller (fn) and context (caller's ctx)
	var fakeCgns []*cgnode          //to handle loop

	var ids []nodeid
	var okCS bool
	var c2ids map[*callsite][]nodeid
	if okFn { //retrieve the record of ids and c2ids
		ids, okCS, c2ids = a.existCallback(fakeFn, caller.callersite[0]) //TODO: bz: only works for k=1
	}

	//may have duplicate targetFn added to ir -> move it in the front to check if is duplicate under the same context; if so, return
	if okFn && okCS && a.existTargetFn(fakeFn, targetFn) { //everything is the same, return
		if a.online {
			objs := make([]nodeid, len(ids))
			for i, id := range ids {
				obj := id + 1
				objs[i] = obj
			}
			return objs
		} else {
			return ids
		}
	}

	//we need to create a whole set (fakefn, fakecgn), since callsite/ctx/origin is different, basicblock/instruction is different
	if !okFn || !okCS {
		if a.log != nil {
			if caller.callersite[0] == nil {
				fmt.Fprintf(a.log, "\t---- \n\tCreate fake function and cgnode for: %s@nil\n", fn.String())
			} else {
				fmt.Fprintf(a.log, "\t---- \n\tCreate fake function and cgnode for: %s@%s\n", fn.String(), caller.callersite)
			}
		}

		//create a fake function
		//we use fn's signature/params below to match the params and return val
		fnName := fn.Name()
		fakeFn = a.prog.NewFunction(fnName, fn.Signature, "synthetic of "+fn.Name())
		fakeFn.Pkg = fn.Pkg
		fakeFn.Params = fn.Params
		fakeFn.IsMySynthetic = true
		fakeFn.SyntInfo = &ssa.SyntheticInfo{ //initialize
			MyIter:      a.curIter,
			CurBlockIdx: 0,
		}

		id := a.addNodes(fakeFn.Type(), fakeFn.String()) //fn id

		//create a cgnode
		obj := a.nextNode()
		fakeCgn := a.makeCGNode(fakeFn, obj, nil)
		fakeCgn.callersite = caller.callersite //same ctx as caller
		sig := fakeFn.Signature
		a.addOneNode(sig, "func.cgnode", nil) // (scalar with Signature type)
		if recv := sig.Recv(); recv != nil {
			a.addNodes(recv.Type(), "func.recv")
		}
		a.addNodes(sig.Params(), "func.params")
		a.addNodes(sig.Results(), "func.results")
		a.endObject(obj, fakeCgn, fakeFn).flags |= otFunction

		if a.log != nil {
			fmt.Fprintf(a.log, "\t---- \n")
		}

		a.gencb = append(a.gencb, fakeCgn) //manually queue it

		//udpate maps
		fakeCgns = make([]*cgnode, 1)
		fakeCgns[0] = fakeCgn

		//update for new callback
		ids = make([]nodeid, 1) //-> we store ids for fakeFn, we need to update its bb later
		ids[0] = id
		c2ids = make(map[*callsite][]nodeid)
		callsite := caller.callersite[0]
		c2ids[callsite] = ids
		a.callbacks[fakeFn] = &Ctx2nodeid{c2ids}

		//update
		a.globalcb[key] = fakeFn
	} else { //reuse
		requeue := false
		if fakeFn.SyntInfo.MyIter != a.curIter { //going to the next iteration, need to update here
			fakeFn.SyntInfo.Update(a.curIter)
			requeue = true
		}

		fakeCgns = make([]*cgnode, len(ids))
		for i, id := range ids {
			obj := id + 1
			fakeCgn := a.nodes[obj].obj.cgn //retrieve it
			fakeCgns[i] = fakeCgn
			if requeue {
				a.gencb = append(a.gencb, fakeCgn) //re-queue it due to newly added instructions
			}
		}
	}

	//create fake callsite and constraint for this fakeFn
	a.genFakeConstraints(fakeFn, v, call, fakeCgns)

	//create/add to a basic block to hold the invoke callback fn instruction
	fakeFn.Pkg.CreateSyntheticCallForCallBack(fakeFn, targetFn, spawn)

	//TODO: why virtual calls (also be added in AnalyzeWCtx()) has duplicate edges?
	objs := make([]nodeid, len(ids))
	for i, id := range ids {
		obj := id + 1
		objs[i] = obj

		//other receive/param constraints
		var result nodeid
		if v := instr.Value(); v != nil {
			result = a.valueNode(v)
		}
		a.genStaticCallCommon(caller, obj, site, call, result)
	}

	//fmt.Println("---> caught: ", key, "\t ", targetFn) //bz: key is lib func call + ctx; targetFn is app make closure

	if a.online {
		return objs
	} else {
		return ids
	}
}

//bz: when DoCollapse = true, ignore context
func (a *analysis) genCallBackCollapse(caller *cgnode, instr ssa.CallInstruction, fn *ssa.Function, site *callsite, call *ssa.CallCommon,
	targetFn ssa.Value, v ssa.Value) nodeid {

	key := fn.String()      //the key of a.globalcb
	spawn := HasGoSpawn(fn) //check if fake function has spawned its own go routine

	//check if fake function already exists
	fakeFn, okFn := a.globalcb[key] //check if fakeFn exist for this caller (fn)
	var fakeCgn *cgnode
	var id nodeid
	//may have duplicate targetFn added to ir -> move it in the front to check if is duplicate; if so, return
	if okFn && a.existTargetFn(fakeFn, targetFn) { //everything is the same, return
		id = a.globalval[fakeFn]
		if a.online {
			return id + 1
		} else {
			return id
		}
	}

	//we need to create a whole set (fakefn, fakecgn), since callsite/ctx/origin is different, basicblock/instruction is different
	if !okFn {
		if a.log != nil {
			fmt.Fprintf(a.log, "\t---- \n\tCreate fake function and cgnode (collapsed) for: %s@nil\n", fn.String())
		}

		//create a fake function
		//we use fn's signature/params below to match the params and return val
		fnName := fn.Name()
		fakeFn = a.prog.NewFunction(fnName, fn.Signature, "synthetic of "+fn.Name())
		fakeFn.Pkg = fn.Pkg
		fakeFn.Params = fn.Params
		fakeFn.IsMySynthetic = true
		fakeFn.SyntInfo = &ssa.SyntheticInfo{ //initialize
			MyIter:      a.curIter,
			CurBlockIdx: 0,
		}

		id = a.addNodes(fakeFn.Type(), fakeFn.String()) //fn id
		a.setValueNode(fakeFn, id, nil)                 //record?? TODO: bz: not sure if new id will overwrite old ones for the same fn

		//create a cgnode
		obj := a.nextNode()
		fakeCgn = a.makeCGNode(fakeFn, obj, nil) //bz: should not consider the context here
		sig := fakeFn.Signature
		a.addOneNode(sig, "func.cgnode", nil) // (scalar with Signature type)
		if recv := sig.Recv(); recv != nil {
			a.addNodes(recv.Type(), "func.recv")
		}
		a.addNodes(sig.Params(), "func.params")
		a.addNodes(sig.Results(), "func.results")
		a.endObject(obj, fakeCgn, fakeFn).flags |= otFunction

		if a.log != nil {
			fmt.Fprintf(a.log, "\t---- \n")
		}

		//udpate with fake callsite for fakefn
		a.gencb = append(a.gencb, fakeCgn) //manually queue it

		//update
		a.globalcb[key] = fakeFn
	} else { //reuse
		requeue := false
		if fakeFn.SyntInfo.MyIter != a.curIter { //going to the next iteration, need to update here
			fakeFn.SyntInfo.Update(a.curIter)
			requeue = true
		}

		id = a.globalval[fakeFn]
		obj := id + 1
		fakeCgn = a.nodes[obj].obj.cgn //retrieve it
		if requeue {
			a.gencb = append(a.gencb, fakeCgn) //re-queue it due to newly added instructions
		}
	}

	//create fake callsite and constraint for this fakeFn
	fakeCgns := make([]*cgnode, 1)
	fakeCgns[0] = fakeCgn
	a.genFakeConstraints(fakeFn, v, call, fakeCgns)

	//create/add to a basic block to hold the invoke callback fn instruction
	fakeFn.Pkg.CreateSyntheticCallForCallBack(fakeFn, targetFn, spawn)

	//TODO: why virtual calls (also be added in AnalyzeWCtx()) has duplicate edges?
	obj := id + 1

	//other receive/param constraints
	var result nodeid
	if v := instr.Value(); v != nil {
		result = a.valueNode(v)
	}
	a.genStaticCallCommon(caller, obj, site, call, result)

	//fmt.Println("---> caught (collapsed): ", key, "\t ", targetFn) //bz: key is lib func call; targetFn is app make closure

	if a.online {
		return obj
	} else {
		return id
	}
}

//bz: check existence of calls to targetFn in the ir of fakeFn
func (a *analysis) existTargetFn(fakeFn *ssa.Function, targetFn ssa.Value) bool {
	for _, bb := range fakeFn.Blocks {
		for _, inst := range bb.Instrs {
			if call, ok := inst.(*ssa.Call); ok {
				if call.Call.Value == targetFn {
					return true
				}
			} else if goo, ok := inst.(*ssa.Go); ok {
				if goo.Call.Value == targetFn {
					return true
				}
			}
		}
	}
	return false
}

//bz: used by a.recursiveCallback(): record the relations among: callback fn, caller lib fn and its context to avoid recursive calls
type callbackRecord struct {
	caller2ctx map[*ssa.Function][]*callsite //TODO: bz: now k = 1 for origin, this is ok; if k>1, this will have problem
}

//bz: skip recursive relations between lib call (caller) <-> callback fn (callee)
//solution: check if # of existing ctx of callers of callback fn >= 3; if so, already traversed, skip
func (a *analysis) recursiveCallback(caller *ssa.Function, callerCtx *callsite, callee ssa.Value) bool {
	targetFn, ok := callee.(*ssa.Function)
	if !ok {
		return false //this is from type assert, do not know how to handle now ...
	}

	//bz: cb2Callers: a map of targetFn <-> {fn <-> callersite}
	record := a.cb2Callers[targetFn]
	if record == nil { //new callback targetFn
		caller2ctx := make(map[*ssa.Function][]*callsite)
		ctx := make([]*callsite, 0)
		ctx = append(ctx, callerCtx)
		caller2ctx[caller] = ctx
		a.cb2Callers[targetFn] = &callbackRecord{
			caller2ctx: caller2ctx,
		}
		return false
	} else { //exist callback targetFn
		caller2ctx := record.caller2ctx
		ctx := caller2ctx[caller]
		if ctx == nil { //new for this caller
			ctx := make([]*callsite, 0)
			ctx = append(ctx, callerCtx)
			caller2ctx[caller] = ctx

			return false
		} else {                                              //exist this caller: check if callerCtx.goInstr already considered
			if callerCtx == nil || callerCtx.goInstr == nil { //no goroutine spawned in this callback
				ctx = append(ctx, callerCtx)
				caller2ctx[caller] = ctx
				return false
			}

			size := 0
			for _, c := range ctx {
				if c == nil || c.goInstr == nil { //no goroutine spawned in this callback
					continue
				}
				if c.goInstr == callerCtx.goInstr {
					if c.loopID == callerCtx.loopID {
						size++
					}
				}
			}
			if size > numGoCallback {
				return true //skip
			}

			ctx = append(ctx, callerCtx)
			caller2ctx[caller] = ctx
			return false
		}
	}

	return false
}

//bz: generate fake target/constraints for fake function --> manually add it
func (a *analysis) genFakeConstraints(targetFn ssa.Value, v ssa.Value, call *ssa.CallCommon, fakeCgns []*cgnode) {
	targets := a.addOneNode(targetFn.Type(), "synthetic.targets", nil)
	site := &callsite{targets: targets}
	for _, fakeCgn := range fakeCgns {
		fakeCgn.sites = append(fakeCgn.sites, site)
	}
	a.copy(targets, a.valueNode(v), 1) //bz: same as above, no context -> cb must exist a obj already before this point
}

//bz: if exist nodeid for *cgnode of fn with callersite as callsite and targetFn as call instr, return nodeids of *cgnode
func (a *analysis) existCallback(fn *ssa.Function, callersite *callsite) ([]nodeid, bool, map[*callsite][]nodeid) {
	_map, ok := a.callbacks[fn]
	if ok { //check if exist
		c2id := _map.ctx2nodeid
		for site, ids := range c2id { //currently only match 1 callsite
			if callersite == site || callersite.loopEqual(site) {
				return ids, true, c2id //exist
			}
		}
		return nil, false, c2id
	}
	return nil, false, nil
}

//bz: whether this is a fn from reflect pkg
func (a *analysis) isFromReflect(fn *ssa.Function) bool {
	if fn.Pkg == nil || fn.Pkg.Pkg == nil {
		return false
	}
	if fn.Pkg.Pkg.Name() == "reflect" {
		return true
	}
	return false
}

//bz : the following several functions generate constraints for different method calls --------------
// genStaticCall generates constraints for a statically dispatched function call.
func (a *analysis) genStaticCall(caller *cgnode, instr ssa.CallInstruction, site *callsite, call *ssa.CallCommon, result nodeid) {
	fn := call.StaticCallee()
	if !a.createForLevelX(caller.fn, fn) {
		if a.log != nil {
			fmt.Fprintf(a.log, "Level excluded: %s\n", fn.String())
		}
		if a.config.DoCallback {
			a.genCallBack(caller, instr, fn, site, call)
		}
		return
	}

	// Special cases for inlined intrinsics.
	switch fn {
	case a.runtimeSetFinalizer:
		// Inline SetFinalizer so the call appears direct.
		site.targets = a.addOneNode(tInvalid, "SetFinalizer.targets", nil)
		a.addConstraint(&runtimeSetFinalizerConstraint{
			targets: site.targets,
			x:       a.valueNode(call.Args[0]),
			f:       a.valueNode(call.Args[1]),
		})
		return

	case a.reflectValueCall:
		if !a.config.Reflection {
			return //bz: skip reflection constraints if we do not consider reflection
		}
		// Inline (reflect.Value).Call so the call appears direct.
		dotdotdot := false
		ret := reflectCallImpl(a, caller, site, a.valueNode(call.Args[0]), a.valueNode(call.Args[1]), dotdotdot)
		if result != 0 {
			a.addressOf(fn.Signature.Results().At(0).Type(), result, ret)
		}
		return
	}

	// Ascertain the context (contour/cgnode) for a particular call.
	var obj nodeid //bz: only used in some cases below
	var id nodeid  //bz: only used if fn is in skips and optHVN == true

	//bz: check if in skip; only if we have not create it before -> optHVN only
	//TODO: bz: how about fn that will have multiple contexts? we did not store them in a.globalobj ...
	if _, ok := a.globalobj[fn]; optHVN && !ok {
		//TODO: bz: this name matching is not perfect ... and this is not a good solution
		name := fn.String()
		name = name[0:strings.LastIndex(name, ".")] //remove func name
		if strings.Contains(name, "(") {
			name = name[1 : len(name)-1] //remove brackets
		}
		if _type, ok := a.skipTypes[name]; ok {
			//let's make a id here
			id = a.addNodes(fn.Type(), _type)
			if a.log != nil {
				fmt.Fprintf(a.log, "Capture skipped type & function: %s\n", fn.String())
			}
		}
	}

	if a.config.K > 0 {
		//bz: for origin-sensitive, we have two cases:
		//case 1: no closure, directly invoke static function: e.g., go Producer(t0, t1, t3), we create a new context for it
		//case 2: has closure: make closure has been created earlier, here find the çreated obj and use its context;
		//        to be specific, a go routine requires a make closure: e.g.,
		//              t37 = make closure (*B).RunParallel$1 [t35, t29, t6, t0, t1]
		//              go t37()
		//        Update: make closure and go can happens in different functions ... i do not like this ...
		//        e.g., t37 passed as a param to func a, then go t37() is in func a
		//case 3: no closure, but invoke virtual function: e.g., go (*ccBalancerWrapper).watcher(t0), we create a new context for it
		if a.considerMyContext(fn.String()) {
			//bz: simple brute force solution; start to be kcfa from main.main.go
			if a.config.DEBUG {
				fmt.Println("CAUGHT APP METHOD -- " + fn.String()) //debug
			}

			//handle possible cases that consumes makeclosure
			_, okGo := instr.(*ssa.Go)
			_, okDefer := instr.(*ssa.Defer)
			_, okCall := instr.Common().Value.(*ssa.MakeClosure) //otherwise if inst like the e.g. in isCallNext(): a direct call to makeclosure, not used as params
			if okGo || okDefer || okCall {                       //bz: invoke a go routine --> detail check for different context-sensitivities; defer/call is similar
				if a.config.DEBUG { //debug
					fmt.Println("\tBUT ssa.GO/ssa.Defer/ssa.Call -- " + site.instr.String() + "   LET'S SEE.")
				}
				a.genStaticCallAfterMakeClosure(caller, instr, site, call, result) //bz: direct the handling outside and return
				return
			}

			//handle other cases
			if caller.fn.IsMySynthetic {
				//bz: synthetic fn and its callback fn need to match here using a.closure not a.fn2cgnodeIdx
				objs, ok, _ := a.existClosure(fn, caller.callersite[0])
				if ok { //exist closure
					for _, obj := range objs { //bz: maybe objs is from loop
						a.genStaticCallCommon(caller, obj, site, call, result)
						return
					}
				} else {
					obj = a.globalval[fn] + 1 //may be directly assign a function instead of make closure
					if obj == 1 {
						//bz: maybe a origin-sensitive function, not a shared contour
						_, _, obj, _ = a.existContextFor(fn, caller)
						if obj == 0 {
							panic("Nil callback makeclosure func in a.existClosure: " + fn.String())
						}
					}
				}
			} else {
				//for kcfa: we need a new contour
				//for origin: whatever left, we use caller context
				obj, _ = a.makeFunctionObjectWithContext(caller, fn, site, nil, 0)
			}
		} else {
			if a.isFromReflect(fn) {
				//bz: for using tests as entry -> use reflection
				// static reflection functions only have shared contours, however, this makes the pta exploded ...
				// because their usage are like:
				//   xt := reflect.TypeOf(x)
				//	 xv := reflect.ValueOf(x)
				//
				//	 for i := 0; i < xt.NumMethod(); i++ {
				//		methodName := xt.Method(i).Name ...
				//
				// all x passed to reflect.TypeOf() will be also passed to xt.Method(), including a lot of infeasible path.
				//SOLUTION -> add context only if origin-sensitive and caller is within scope: use caller context, do not consider loop now;
				// other reflection methods, ignore them
				if a.config.Reflection && a.considerMyContext(caller.fn.String()) {
					obj, _ = a.makeFunctionObjectWithContext(caller, fn, site, nil, 0)
				} //else: from reflections that are not in the tests from the analyzed app
			} else { //bz: skip reflection constraints if we do not consider reflection
				obj = a.objectNode(nil, fn) // shared contour
			}
		}
	} else {                        //default: context-insensitive
		if a.shouldUseContext(fn) { // default
			obj = a.makeFunctionObject(fn, site) // new contour
		} else {
			obj = a.objectNode(nil, fn) // shared contour
		}
	}

	if obj != 0 {
		if optHVN && id != 0 { //bz: we need to make up this missing constraints
			a.addressOf(fn.Type(), id, obj)
			//but do we set value in a.globalobj? since fn might have multiple contexts
		}

		a.genStaticCallCommon(caller, obj, site, call, result)
	}
}

//bz: we take these special cases for all cases that consumes a makeclosure (*ssa.Go, *ssa.Defer, *ssa.Call) outside normal workflow;
//both kcfa and origin
func (a *analysis) genStaticCallAfterMakeClosure(caller *cgnode, instr ssa.CallInstruction, site *callsite, call *ssa.CallCommon, result nodeid) {
	fn := call.StaticCallee()
	objs, ok, c2id := a.existClosure(fn, caller.callersite[0])
	if ok { //exist closure add its new context, but no constraints
	} else if c2id != nil { //has fn as key, but does not have the caller context as value
		fmt.Println("TODO .... @genStaticCallAfterMakeClosure")
	} else { //bz: case 1 and 3: we need a new cgn and a new context for origin -> this record is stored at a.fn2cgnodeIdx
		if a.config.Origin && a.isInLoop(fn, instr) {
			objs = make([]nodeid, 2)
			//bz: loop problem -> two origin contexts here
			_, _, obj1, isNew1 := a.existContext(fn, site, caller, 1)
			if isNew1 {
				obj1, _ = a.makeFunctionObjectWithContext(caller, fn, site, nil, 1)
			}
			objs[0] = obj1

			_, _, obj2, isNew2 := a.existContext(fn, site, caller, 2)
			if isNew2 {
				obj2, _ = a.makeFunctionObjectWithContext(caller, fn, site, nil, 2)
			}
			objs[1] = obj2
		} else { //kcfa (no loop handling); and origin with no loop
			objs = make([]nodeid, 1)
			_, _, obj, isNew := a.existContext(fn, site, caller, 0)
			if isNew {
				obj, _ = a.makeFunctionObjectWithContext(caller, fn, site, nil, 0)
			}
			objs[0] = obj
		}
	}

	for _, obj := range objs {
		a.genStaticCallCommon(caller, obj, site, call, result)
	}
}

//bz: the common part of genStaticCall(); for each input obj (with type nodeid)
func (a *analysis) genStaticCallCommon(caller *cgnode, obj nodeid, site *callsite, call *ssa.CallCommon, result nodeid) {
	a.callEdge(caller, site, obj)
	sig := call.Signature()

	// Copy receiver, if any.
	params := a.funcParams(obj)
	args := call.Args
	if sig.Recv() != nil {
		sz := a.sizeof(sig.Recv().Type())
		a.copy(params, a.valueNode(args[0]), sz)
		params += nodeid(sz)
		args = args[1:]

		////bz: update for preSolve()
		//a.recvConstraints = append(a.recvConstraints, &recvConstraint{
		//	iface:  sig.Recv().Type(),
		//	method: call.Value.(*ssa.Function), //bz: here call.Value is func value (call mode)
		//	caller: caller,
		//	site:   site,
		//})
	}

	// Copy actual parameters into formal params block.
	// Must loop, since the actuals aren't contiguous.
	for i, arg := range args { // bz: arg -> *ssa.Instruction
		sz := a.sizeof(sig.Params().At(i).Type())
		a.copy(params, a.valueNode(arg), sz)
		params += nodeid(sz)
	}

	// Copy formal results block to actual result.
	if result != 0 {
		a.copy(result, a.funcResults(obj), a.sizeof(sig.Results()))
	}
}

// genDynamicCall generates constraints for a dynamic function call.
func (a *analysis) genDynamicCall(caller *cgnode, site *callsite, call *ssa.CallCommon, result nodeid) {
	// pts(targets) will be the set of possible call targets.
	site.targets = a.valueNode(call.Value)

	// We add dynamic closure rules that store the arguments into
	// the P-block and load the results from the R-block of each
	// function discovered in pts(targets).

	sig := call.Signature()

	var offset uint32 = 1 // P/R block starts at offset 1
	for i, arg := range call.Args {
		sz := a.sizeof(sig.Params().At(i).Type())
		a.genStore(caller, call.Value, a.valueNode(arg), offset, sz)
		offset += sz
	}

	if result != 0 {
		a.genLoad(caller, result, call.Value, offset, a.sizeof(sig.Results()))
	}
}

//bz: Online iteratively doing genFunc <-> genInstr iteratively
func (a *analysis) genInvokeOnline(caller *cgnode, site *callsite, fn *ssa.Function) nodeid {
	//set status
	a.online = true
	if caller != nil {
		a.localval = caller.localval
		a.localobj = caller.localobj
	}

	//start point
	fnObj := a.valueNodeInvoke(caller, site, fn)

	//TODO: we skip optimization now, just in case it will mess up. maybe add it later??
	return fnObj
}

//bz: Online iteratively doing genFunc <-> genInstr iteratively: after solving param/ret constraints
func (a *analysis) genConstraintsOnline() {
	//generate constraints for each one
	for len(a.genq) > 0 {
		cgn := a.genq[0]
		a.genq = a.genq[1:]
		a.genFunc(cgn)
	}

	a.online = false //set back
}

//bz: special handling for invoke (online solving), doing something like genMethodsOf() and valueNode() for invoke calls;
//called Online must be global
//UPDATE: the id and obj are confusing: offline we need the id (which is created func node, or mostly represents a pointer),
//        while online we need the obj (which is the cgnode and its params/return values)
//        THIS previously causes panics, should not have panics any more now
func (a *analysis) valueNodeInvoke(caller *cgnode, site *callsite, fn *ssa.Function) nodeid {
	if caller == nil && site == nil { //requires shared contour
		id := a.valueNode(fn) //bz: we use this to generate fn, cgn and their constraints, no other uses
		//return cgn vs func ? different algo/settings require different values, only cgn is marked with otFunction
		//-> apply to all returns in this function
		if a.config.DoCallback {
			//optRenumber and optHVN will mess up the routine (cgn = fn + 1), and it needs cgn here
			//so retrieve this from a.globalobj (shared contour stores here) and id must exist since we already preSolve() all cgnodes
			return a.globalobj[fn]
		}
		//on-the-fly
		if a.online {
			return id + 1 //cgn
		} else {
			return id //fn
		}
	}

	//loopID here should be consistent with caller's loopID -> the context of an invoked target should be consistent with its caller; no go/closure here
	loopID := 0
	if caller.callersite[0] != nil {
		loopID = caller.callersite[0].loopID
	}
	//similar with valueNode(), created on demand. Instead of a.globalval[], we use a.fn2cgnodeid[] due to contexts
	existCGNIdx, _, obj, isNew := a.existContext(fn, site, caller, loopID)
	if isNew {
		var comment string
		if a.log != nil {
			comment = fn.String()
		}
		var id = a.addNodes(fn.Type(), comment) //bz: id + 1 = obj
		if obj = a.objectNodeSpecial(caller, nil, site, fn, loopID); obj != 0 {
			a.addressOf(fn.Type(), id, obj)
		}
		//a.setValueNode(fn, id, nil) //bz: this will mess up/overwrite record for shared contour fns

		itf := isInterface(fn.Type()) //bz: not sure if this is equivalent to the one in genMethodOf()
		if !itf {
			a.atFuncs[fn] = true // Methods of concrete types are address-taken functions.
		}

		if a.online { //bz: callback should not reach here
			return obj
		} else {
			return id
		}
	}

	if a.config.DoCallback {
		if len(existCGNIdx) > 1 {
			fmt.Println("PANIC. why do i have multiple match: ", fn, "@", caller, "&", site)
		}
		idx := existCGNIdx[0]
		cgn := a.cgnodes[idx]
		return cgn.obj
	}
	if a.online {
		return obj
	} else {
		return obj - 1 //== id : fn
	}
}

// genInvoke generates constraints for a dynamic method invocation.
// bz: NOTE: not every x.m() is treated by genInvoke() here, some is treated by genStaticCall(),
// move the genFunc() for invoke calls Online
// Update: we cannot know the exact target function, maybe call.Method() to look up the concrete method???
//  so we create the constraints here; if we confirm this is not reachable or excluded, we just leave the constraints here
func (a *analysis) genInvoke(caller *cgnode, site *callsite, call *ssa.CallCommon, result nodeid) {
	if call.Value.Type() == a.reflectType {
		if a.config.Reflection { //bz: skip reflection constraints if we do not consider reflection
			a.genInvokeReflectType(caller, site, call, result)
		}
		return
	}
	sig := call.Signature()

	// Allocate a contiguous targets/params/results block for this call.
	block := a.nextNode() //bz: <----- this node is empty, just to mark this is the start of P/R block
	// pts(targets) will be the set of possible call targets
	site.targets = a.addOneNode(sig, "invoke.targets", nil) //bz: site.targets -> receiver of this invoke
	p := a.addNodes(sig.Params(), "invoke.params")
	r := a.addNodes(sig.Results(), "invoke.results")

	// Copy the actual parameters into the call's params block.
	for i, n := 0, sig.Params().Len(); i < n; i++ {
		sz := a.sizeof(sig.Params().At(i).Type())
		a.copy(p, a.valueNode(call.Args[i]), sz) //bz: call.Args -> *ssa.Parameter, is local
		p += nodeid(sz)
	}
	// Copy the call's results block to the actual results.
	if result != 0 {
		a.copy(result, r, a.sizeof(sig.Results()))
	}

	// We add a dynamic invoke constraint that will connect the
	// caller's and the callee's P/R blocks for each discovered
	// call target.
	if a.considerMyContext(sig.Recv().Type().String()) { //requires receiver type
		//bz: simple solution; start to be kcfa from main.main; INSTEAD OF genMethodsOf(), we create it Online
		if a.config.DEBUG {
			fmt.Println("CAUGHT APP INVOKE METHOD -- " + sig.Recv().Type().String() + "   NO FUNC, WILL CREATE IT ONLINE LATER.")
		}
		a.addConstraint(&invokeConstraint{call.Method, a.valueNode(call.Value), block, site, caller}) //bz: we need sites later Online
	} else {
		a.addConstraint(&invokeConstraint{call.Method, a.valueNode(call.Value), block, nil, nil}) //bz: call.Value is local and base, e.g., t1
	}
}

// genInvokeReflectType is a specialization of genInvoke where the
// receiver type is a reflect.Type, under the assumption that there
// can be at most one implementation of this interface, *reflect.rtype.
//
// (Though this may appear to be an instance of a pattern---method
// calls on interfaces known to have exactly one implementation---in
// practice it occurs rarely, so we special case for reflect.Type.)
//
// In effect we treat this:
//    var rt reflect.Type = ...
//    rt.F()
// as this:
//    rt.(*reflect.rtype).F()
//bz: for now use 1-callsite (original default), TODO: if necessary, update to kcfa
func (a *analysis) genInvokeReflectType(caller *cgnode, site *callsite, call *ssa.CallCommon, result nodeid) {
	// Look up the concrete method.
	fn := a.prog.LookupMethod(a.reflectRtypePtr, call.Method.Pkg(), call.Method.Name())
	if !a.createForLevelX(caller.fn, fn) {
		if a.config.DEBUG {
			fmt.Println("Level excluded: " + fn.String())
		}
		return
	}

	// Unpack receiver into rtype
	rtype := a.addOneNode(a.reflectRtypePtr, "rtype.recv", nil)
	recv := a.valueNode(call.Value)
	a.typeAssert(a.reflectRtypePtr, rtype, recv, true)

	obj := a.makeFunctionObject(fn, site) // new contour for this call
	a.callEdge(caller, site, obj)

	// From now on, it's essentially a static call, but little is
	// gained by factoring together the code for both cases.

	sig := fn.Signature // concrete method
	targets := a.addOneNode(sig, "call.targets", nil)
	a.addressOf(sig, targets, obj) // (a singleton)

	// Copy receiver.
	params := a.funcParams(obj)
	a.copy(params, rtype, 1)
	params++

	// Copy actual parameters into formal P-block.
	// Must loop, since the actuals aren't contiguous.
	for i, arg := range call.Args {
		sz := a.sizeof(sig.Params().At(i).Type())
		a.copy(params, a.valueNode(arg), sz)
		params += nodeid(sz)
	}

	// Copy formal R-block to actual R-block.
	if result != 0 {
		a.copy(result, a.funcResults(obj), a.sizeof(sig.Results()))
	}
}

// genCall generates constraints for call instruction instr.
func (a *analysis) genCall(caller *cgnode, instr ssa.CallInstruction) {
	call := instr.Common()

	// Intrinsic implementations of built-in functions.
	if _, ok := call.Value.(*ssa.Builtin); ok {
		a.genBuiltinCall(instr, caller)
		return
	}

	var result nodeid
	if v := instr.Value(); v != nil {
		result = a.valueNode(v)
	}

	site := &callsite{instr: instr}
	if call.StaticCallee() != nil {
		a.genStaticCall(caller, instr, site, call, result)
	} else if call.IsInvoke() {
		a.genInvoke(caller, site, call, result)
	} else {
		a.genDynamicCall(caller, site, call, result)
	}

	caller.sites = append(caller.sites, site)

	if a.log != nil {
		// TODO(adonovan): debug: improve log message.
		fmt.Fprintf(a.log, "\t%s to targets %s from %s\n", site, site.targets, caller)
	}
}

//bz: special handling for closure, doing somthing similar to valueNode(); must be global
// only for origin
func (a *analysis) valueNodeClosure(cgn *cgnode, closure *ssa.MakeClosure, v ssa.Value) []nodeid {
	// Value nodes for globals are created on demand.
	fn, _ := v.(*ssa.Function)
	callersite := cgn.callersite[0]
	ids, ok, c2id := a.existClosure(fn, callersite) //checked
	if !ok {                                        //not exist
		var comment string
		if a.log != nil {
			comment = v.String()
		}
		var objs []nodeid

		//if callersite == nil {
		//	fmt.Println("--> makeclosure: ", fn, "\tnil")
		//} else {
		//	fmt.Println("--> makeclosure: ", fn, "\t", callersite)
		//}

		if c2id != nil { //closure might from loop but not solved at the same time/same cgnode
			ids = make([]nodeid, 1)
			objs = make([]nodeid, 1)
			id, obj := a.valueNodeClosureInternal(cgn, closure, v, -1, comment)
			//udpate
			ids[0] = id
			objs[0] = obj

			c2id[callersite] = objs
			a.closures[fn] = &Ctx2nodeid{c2id} //we do not know the matching go instr now -> update later

			return ids
		}

		//if strings.Contains(fn.String(), "(*github.com/pingcap/tidb/util/stmtsummary.stmtSummaryByDigestMap).GetMoreThanOnceBindableStmt$1") {
		//	//&& strings.Contains(caller.contourkFull(), "[0:synthetic function call@n229683; ]")
		//	fmt.Print(" ! ", cgn.contourkFull())
		//}

		if a.config.Origin && a.isInLoop(cgn.fn, closure) && !a.consumeMakeClosureNext(closure) { // handle loop only in origin-sensitive
			ids = make([]nodeid, 2)
			objs = make([]nodeid, 2)
			loopID := 1
			for loopID < 3 { //loopID = 1 and 2
				id, obj := a.valueNodeClosureInternal(cgn, closure, v, loopID, comment)
				//udpate
				ids[loopID-1] = id
				objs[loopID-1] = obj
				loopID++
			}
		} else { // create a single cgnode
			ids = make([]nodeid, 1)
			objs = make([]nodeid, 1)
			id, obj := a.valueNodeClosureInternal(cgn, closure, v, 0, comment)
			//udpate
			ids[0] = id
			objs[0] = obj
		}

		//update for new closures
		c2id = make(map[*callsite][]nodeid)
		c2id[callersite] = objs
		a.closures[fn] = &Ctx2nodeid{c2id} //we do not know the matching go instr now -> update later
	}

	return ids
}

//bz: see valueNodeClosure()
func (a *analysis) valueNodeClosureInternal(cgn *cgnode, closure *ssa.MakeClosure, v ssa.Value, loopID int, comment string) (nodeid, nodeid) {
	var obj nodeid
	id := a.addNodes(v.Type(), comment)
	if obj = a.objectNodeSpecial(cgn, closure, nil, v, loopID); obj != 0 {
		a.addressOf(v.Type(), id, obj)
	}
	a.setValueNode(v, id, nil) //bz: this is local; multi closures in a method will have different $num, no replacements
	return id, obj
}

//bz: if exist nodeid for *cgnode of fn with callersite as callsite, return nodeids of *cgnode
//Update: avoid recursive goroutine (and its make closure) spawn, these are redundant,
//    e.g.,  (*github.com/pingcap/tidb/store/tikv.tikvStore).splitBatchRegionsReq$1
//solution: check the existence, if all its callersite (caller.callersite[0] for k = 1) has the same go instruction (), skip -> return true
func (a *analysis) existClosure(fn *ssa.Function, callersite *callsite) ([]nodeid, bool, map[*callsite][]nodeid) {
	_map, ok := a.closures[fn]
	if ok { //check if exist
		c2id := _map.ctx2nodeid
		size := 0                     //the number of make closure
		var goIDs []nodeid            //for duplicate go and make closure
		for site, ids := range c2id { //currently only match 1 callsite
			if callersite == site || callersite.loopEqual(site) {
				return ids, true, c2id //exist
			} else if callersite.goEqual(site) { //check if go is the same
				size++
				goIDs = ids
			}
		}
		if size >= 2 { //duplicate go calls on the same make closure TODO: this number can be changed ... i feel 2 is reasonable -> 2-level of recursive calls
			return goIDs, true, c2id
		}
		return nil, false, c2id
	}
	return nil, false, nil
}

//bz: special handling for objectNode()
func (a *analysis) objectNodeSpecial(cgn *cgnode, closure *ssa.MakeClosure, site *callsite, v ssa.Value, loopID int) nodeid {
	fn, _ := v.(*ssa.Function)            // must be Global object.
	if a.considerMyContext(fn.String()) { //create cgnode/constraints here;
		if closure != nil {
			//make closure: this has NO ssa.CallInstruction as callsite
			//this existance check has done already and is separated using a.closure[]; if reach here, must be new one and create one
			obj, _ := a.makeFunctionObjectWithContext(cgn, fn, nil, closure, loopID)
			return obj
		} else if site != nil {
			//invoke: ONLINE; we have checked the existance in valueNodeInvoke(),
			//TODO: skip the checking here; create if not exist
			obj, _ := a.makeFunctionObjectWithContext(cgn, fn, site, nil, loopID)
			return obj
		}
	}
	// normal case or not interesting case
	return a.objectNodeOthers(fn)
}

// bz: normal case or not interesting cases
func (a *analysis) objectNodeOthers(fn *ssa.Function) nodeid {
	obj, ok := a.globalobj[fn]
	if !ok {
		obj = a.makeFunctionObject(fn, nil)
		if a.log != nil {
			fmt.Fprintf(a.log, "\tglobalobj[%s] = n%d\n", fn, obj)
		}
		a.globalobj[fn] = obj //bz: obj is nodeid used in a.nodes[] if v is invoke
	}
	return obj
}

//bz: tell if fn is from makeclosure
func (a *analysis) isFromMakeClosure(fn *ssa.Function) bool {
	referrers := fn.Referrers()
	if referrers == nil {
		return false
	}

	_, ok := (*referrers)[0].(*ssa.MakeClosure)
	if ok { //should have another better way to do type comparison
		return true
	}
	return false
}

// objectNode returns the object to which v points, if known.
// In other words, if the points-to set of v is a singleton, it
// returns the sole label, zero otherwise.
//
// We exploit this information to make the generated constraints less
// dynamic.  For example, a complex load constraint can be replaced by
// a simple copy constraint when the sole destination is known a priori.
//
// Some SSA instructions always have singletons points-to sets:
// 	Alloc, Function, Global, MakeChan, MakeClosure,  MakeInterface,  MakeMap,  MakeSlice.
// Others may be singletons depending on their operands:
// 	FreeVar, Const, Convert, FieldAddr, IndexAddr, Slice.
//
// Idempotent.  Objects are created as needed, possibly via recursion
// down the SSA value graph, e.g IndexAddr(FieldAddr(Alloc))).
//
func (a *analysis) objectNode(cgn *cgnode, v ssa.Value) nodeid {
	switch v.(type) {
	case *ssa.Global, *ssa.Function, *ssa.Const, *ssa.FreeVar:
		// Global object.
		obj, ok := a.globalobj[v]
		if !ok {
			switch v := v.(type) {
			case *ssa.Global:
				obj = a.nextNode()
				a.addNodes(mustDeref(v.Type()), "global")
				a.endObject(obj, nil, v)

			case *ssa.Function:
				//bz: this has NO ssa.CallInstruction as callsite;
				//v should not be make closure, we handle it in a different function for both kcfa and origin, panic!
				isClosure := a.isFromMakeClosure(v)
				if isClosure && a.considerMyContext(v.String()) {
					panic("WRONG PATH @objectNode() FOR MAKE CLOSURE: " + v.String())
				}
				obj = a.makeFunctionObject(v, nil) //TODO: bz: missing scope func here

			case *ssa.Const:
				// not addressable

			case *ssa.FreeVar:
				// not addressable
			}

			if a.log != nil {
				fmt.Fprintf(a.log, "\tglobalobj[%s] = n%d\n", v, obj)
			}
			a.globalobj[v] = obj //bz: obj is nodeid used in a.nodes[] if v is invoke
		}
		return obj
	}

	// Local object.
	obj, ok := a.localobj[v]
	if !ok {
		switch v := v.(type) {
		case *ssa.Alloc:
			obj = a.nextNode()
			a.addNodes(mustDeref(v.Type()), "alloc")
			a.endObject(obj, cgn, v)
			a.numObjs++

		case *ssa.MakeSlice:
			obj = a.nextNode()
			a.addNodes(sliceToArray(v.Type()), "makeslice")
			a.endObject(obj, cgn, v)
			a.numObjs++

		case *ssa.MakeChan:
			obj = a.nextNode()
			a.addNodes(v.Type().Underlying().(*types.Chan).Elem(), "makechan")
			a.endObject(obj, cgn, v)
			a.numObjs++

		case *ssa.MakeMap:
			obj = a.nextNode()
			tmap := v.Type().Underlying().(*types.Map)
			a.addNodes(tmap.Key(), "makemap.key")
			elem := a.addNodes(tmap.Elem(), "makemap.value")

			// To update the value field, MapUpdate
			// generates store-with-offset constraints which
			// the presolver can't model, so we must mark
			// those nodes indirect.
			for id, end := elem, elem+nodeid(a.sizeof(tmap.Elem())); id < end; id++ {
				a.mapValues = append(a.mapValues, id)
			}
			a.endObject(obj, cgn, v)
			a.numObjs++

		case *ssa.MakeInterface:
			tConc := v.X.Type()
			obj = a.makeTagged(tConc, cgn, v)

			// Copy the value into it, if nontrivial.
			if x := a.valueNode(v.X); x != 0 {
				a.copy(obj+1, x, a.sizeof(tConc))
			}

		case *ssa.FieldAddr:
			if xobj := a.objectNode(cgn, v.X); xobj != 0 {
				obj = xobj + nodeid(a.offsetOf(mustDeref(v.X.Type()), v.Field))
			}

		case *ssa.IndexAddr:
			if xobj := a.objectNode(cgn, v.X); xobj != 0 {
				obj = xobj + 1
			}

		case *ssa.Slice:
			obj = a.objectNode(cgn, v.X)

		case *ssa.Convert:
			// TODO(adonovan): opt: handle these cases too:
			// - unsafe.Pointer->*T conversion acts like Alloc
			// - string->[]byte/[]rune conversion acts like MakeSlice
		}

		if a.log != nil {
			fmt.Fprintf(a.log, "\tlocalobj[%s] = n%d\n", v.Name(), obj)
		}
		a.localobj[v] = obj
	}
	return obj
}

// genLoad generates constraints for result = *(ptr + val).
func (a *analysis) genLoad(cgn *cgnode, result nodeid, ptr ssa.Value, offset, sizeof uint32) {
	if obj := a.objectNode(cgn, ptr); obj != 0 {
		// Pre-apply loadConstraint.solve().
		a.copy(result, obj+nodeid(offset), sizeof)
	} else {
		a.load(result, a.valueNode(ptr), offset, sizeof)
	}
}

// genOffsetAddr generates constraints for a 'v=ptr.field' (FieldAddr)
// or 'v=ptr[*]' (IndexAddr) instruction v.
func (a *analysis) genOffsetAddr(cgn *cgnode, v ssa.Value, ptr nodeid, offset uint32) {
	dst := a.valueNode(v)
	if obj := a.objectNode(cgn, v); obj != 0 {
		// Pre-apply offsetAddrConstraint.solve().
		a.addressOf(v.Type(), dst, obj)
	} else {
		a.offsetAddr(v.Type(), dst, ptr, offset)
	}
}

// genStore generates constraints for *(ptr + offset) = val.
func (a *analysis) genStore(cgn *cgnode, ptr ssa.Value, val nodeid, offset, sizeof uint32) {
	if obj := a.objectNode(cgn, ptr); obj != 0 {
		// Pre-apply storeConstraint.solve().
		a.copy(obj+nodeid(offset), val, sizeof)
	} else {
		a.store(a.valueNode(ptr), val, offset, sizeof)
	}
}

// genInstr generates constraints for instruction instr in context cgn.
func (a *analysis) genInstr(cgn *cgnode, instr ssa.Instruction) {
	if a.log != nil {
		var prefix string
		if val, ok := instr.(ssa.Value); ok {
			prefix = val.Name() + " = "
		}
		fmt.Fprintf(a.log, "; %s%s\n", prefix, instr)
	}

	switch instr := instr.(type) {
	case *ssa.DebugRef:
		// no-op.

	case *ssa.UnOp:
		switch instr.Op {
		case token.ARROW: // <-x
			// We can ignore instr.CommaOk because the node we're
			// altering is always at zero offset relative to instr
			tElem := instr.X.Type().Underlying().(*types.Chan).Elem()
			a.genLoad(cgn, a.valueNode(instr), instr.X, 0, a.sizeof(tElem))

		case token.MUL: // *x
			a.genLoad(cgn, a.valueNode(instr), instr.X, 0, a.sizeof(instr.Type()))

		default:
			// NOT, SUB, XOR: no-op.
		}

	case *ssa.BinOp:
		// All no-ops.

	case ssa.CallInstruction: // *ssa.Call, *ssa.Go, *ssa.Defer
		a.genCall(cgn, instr) // for origin, creat new contexts for *ssa.GO

	case *ssa.ChangeType:
		a.copy(a.valueNode(instr), a.valueNode(instr.X), 1)

	case *ssa.Convert:
		a.genConv(instr, cgn)

	case *ssa.Extract: // bz: access the ith results of a multiple return values; should already be separated, not interested
		a.copy(a.valueNode(instr),
			a.valueOffsetNode(instr.Tuple, instr.Index),
			a.sizeof(instr.Type()))

	case *ssa.FieldAddr:
		a.genOffsetAddr(cgn, instr, a.valueNode(instr.X),
			a.offsetOf(mustDeref(instr.X.Type()), instr.Field))

	case *ssa.IndexAddr:
		a.genOffsetAddr(cgn, instr, a.valueNode(instr.X), 1)

	case *ssa.Field:
		a.copy(a.valueNode(instr),
			a.valueOffsetNode(instr.X, instr.Field),
			a.sizeof(instr.Type()))

	case *ssa.Index:
		a.copy(a.valueNode(instr), 1+a.valueNode(instr.X), a.sizeof(instr.Type()))

	case *ssa.Select:
		recv := a.valueOffsetNode(instr, 2) // instr : (index, recvOk, recv0, ... recv_n-1)
		for _, st := range instr.States {
			elemSize := a.sizeof(st.Chan.Type().Underlying().(*types.Chan).Elem())
			switch st.Dir {
			case types.RecvOnly:
				a.genLoad(cgn, recv, st.Chan, 0, elemSize)
				recv += nodeid(elemSize)

			case types.SendOnly:
				a.genStore(cgn, st.Chan, a.valueNode(st.Send), 0, elemSize)
			}
		}

	case *ssa.Return:
		results := a.funcResults(cgn.obj)
		for _, r := range instr.Results {
			sz := a.sizeof(r.Type())
			a.copy(results, a.valueNode(r), sz)
			results += nodeid(sz)
		}

	case *ssa.Send:
		a.genStore(cgn, instr.Chan, a.valueNode(instr.X), 0, a.sizeof(instr.X.Type()))

	case *ssa.Store:
		a.genStore(cgn, instr.Addr, a.valueNode(instr.Val), 0, a.sizeof(instr.Val.Type()))

	case *ssa.Alloc, *ssa.MakeSlice, *ssa.MakeChan, *ssa.MakeMap, *ssa.MakeInterface: //bz: all are creating objs
		v := instr.(ssa.Value)
		a.addressOf(v.Type(), a.valueNode(v), a.objectNode(cgn, v))

	case *ssa.ChangeInterface:
		a.copy(a.valueNode(instr), a.valueNode(instr.X), 1)

	case *ssa.TypeAssert:
		a.typeAssert(instr.AssertedType, a.valueNode(instr), a.valueNode(instr.X), true)

	case *ssa.Slice:
		a.copy(a.valueNode(instr), a.valueNode(instr.X), 1)

	case *ssa.If, *ssa.Jump:
		// no-op.

	case *ssa.Phi:
		sz := a.sizeof(instr.Type())
		for _, e := range instr.Edges {
			a.copy(a.valueNode(instr), a.valueNode(e), sz)
		}

	case *ssa.MakeClosure:
		fn := instr.Fn.(*ssa.Function) //bz: fn should not be nil, because we are closuring on a static go routine
		if a.considerMyContext(fn.String()) {
			if a.config.DEBUG {
				fmt.Println("CAUGHT APP (MakeClosure) METHOD -- " + fn.String()) //debug
			}
			//bz: should only handle this in the origin-sensitive way when the next stmt is go invoke
			// do this check during the process
			cs := a.valueNodeClosure(cgn, instr, fn)
			for _, c := range cs { // bz: updated for []nodeid
				a.copy(a.valueNode(instr), c, 1)
			}
		} else { //default: context-insensitive
			//Update: for origin-sensitive, we ignore the go routines that associate with this make closure
			a.copy(a.valueNode(instr), a.valueNode(fn), 1)
		}

		// Free variables are treated like global variables.
		for i, b := range instr.Bindings {
			a.copy(a.valueNode(fn.FreeVars[i]), a.valueNode(b), a.sizeof(b.Type()))
		}

	case *ssa.RunDefers:
		// The analysis is flow insensitive, so we just "call"
		// defers as we encounter them.

	case *ssa.Range:
		// Do nothing.  Next{Iter: *ssa.Range} handles this case.

	case *ssa.Next:
		if !instr.IsString { // map
			// Assumes that Next is always directly applied to a Range result.
			theMap := instr.Iter.(*ssa.Range).X
			tMap := theMap.Type().Underlying().(*types.Map)

			ksize := a.sizeof(tMap.Key())
			vsize := a.sizeof(tMap.Elem())

			// The k/v components of the Next tuple may each be invalid.
			tTuple := instr.Type().(*types.Tuple)

			// Load from the map's (k,v) into the tuple's (ok, k, v).
			osrc := uint32(0) // offset within map object
			odst := uint32(1) // offset within tuple (initially just after 'ok bool')
			sz := uint32(0)   // amount to copy

			//TODO: report to go @ https://github.com/golang/go/issues/45735  (local log: my_log_0-panic-report)
			// !!!! bz: panic@grpc commit e38032e9 at ir stmt (; t40 = next t34) in function (*google.golang.org/grpc/xds/internal/balancer/edsbalancer.balancerGroup).close
			// REASON -> load base is wrong due to wrong computed odst: it maps to other localval
			//   This has nothing to do with my code, so i guess it's some wrong logic in default algorithm?
			// from what I see there are two types of tTuple:
			// (1) (ok bool, k invalid type, v *subBalancerWithConfig) -> key is invalid type (but actually the type is valid, maybe because the range only iterates over map values, not keys)
			// (2) (ok bool, k string, v invalid type) -> no invalid type of key
			// and the receiver/base of the load is created like this:
			//  	create n678886 bool for t40#0 (--> ok)
			//  	create n678887 invalid type for t40#1 (--> key)
			//  	create n678888 *google.golang.org/grpc/xds/internal/balancer/edsbalancer.subBalancerWithConfig for t40#2 (--> value)
			//  	val[t40] = n678886  (*ssa.Next)
			// or:
			// 		create n117162 bool for t67#0
			// 		create n117163 string for t67#1 (--> valid key type)
			// 		create n117164 []byte for t67#2
			// 		val[t67] = n117162  (*ssa.Next)
			// we need the key (n678887) and value (n678888) to be the receiver/base of the load, the algorithm will not do flatten copy for key or value pointer...
			// so, why needs the flattened size of key, i.e., ksize, when key is invalid type ??

			// Is key valid?
			if tTuple.At(1).Type() != tInvalid {
				sz += ksize
			} else {
				odst += 1 //ksize //bz: original code
				osrc += ksize
			}

			// Is value valid?
			if tTuple.At(2).Type() != tInvalid {
				sz += vsize
			}

			a.genLoad(cgn, a.valueNode(instr)+nodeid(odst), theMap, osrc, sz)
		}

	case *ssa.Lookup:
		if tMap, ok := instr.X.Type().Underlying().(*types.Map); ok {
			// CommaOk can be ignored: field 0 is a no-op.
			ksize := a.sizeof(tMap.Key())
			vsize := a.sizeof(tMap.Elem())
			a.genLoad(cgn, a.valueNode(instr), instr.X, ksize, vsize)
		}

	case *ssa.MapUpdate:
		tmap := instr.Map.Type().Underlying().(*types.Map)
		ksize := a.sizeof(tmap.Key())
		vsize := a.sizeof(tmap.Elem())
		a.genStore(cgn, instr.Map, a.valueNode(instr.Key), 0, ksize)
		a.genStore(cgn, instr.Map, a.valueNode(instr.Value), ksize, vsize)

	case *ssa.Panic:
		a.copy(a.panicNode, a.valueNode(instr.X), 1)

	default:
		panic(fmt.Sprintf("unimplemented: %T", instr))
	}
}

//// bz: check whether the stmt before a static call is make closure: they must be in the same basic block
//func (a *analysis) isClosureBefore(instr ssa.CallInstruction) bool {
//	stmts := instr.Block().Instrs
//	for i, stmt := range stmts {
//		if stmt == instr {
//			_, ok := stmts[i-1].(*ssa.MakeClosure) //before is the closure if available
//			if ok {
//				return true
//			}else {
//				return false
//			}
//		}
//	}
//	return false
//}

// bz: check whether there exist go stmt after make closure and using this make closure: they must be in the same basic block
// Update: this is too strict, things happens in Kubernetes88331: (*command-line-arguments.PriorityQueue).Run
//         now update to if there exists a go that refers instr, but they still must be in the same basic block
func (a *analysis) isGoNext(instr *ssa.MakeClosure) *ssa.Go {
	stmts := instr.Block().Instrs
	for i, stmt := range stmts {
		if stmt == instr { //start check from here
			for j := i + 1; j < len(stmts); j++ {
				goInstr, ok := stmts[j].(*ssa.Go) //if the go if available
				if ok {
					val := goInstr.Call.Value
					if closure, ok := val.(*ssa.MakeClosure); ok {
						if instr == closure { //these two values should be the same
							return goInstr
						}
					}
				}
			}
			if a.config.DEBUG {
				fmt.Println(">>> NO GO FOR MAKECLOSURE: " + instr.String())
			}
			return nil //no go instr until end of basic block
		}
	}
	return nil
}

//bz: there are three ways that a makeclosure will be consumed/invoked:
//(1) go (2) defer (3) call. Here, only (1) will create a new origin/context, the other two are only regular calls
//return true if make closure is consumed by (2) and (3)
func (a *analysis) consumeMakeClosureNext(instr *ssa.MakeClosure) bool {
	return a.isDeferNext(instr) != nil || a.isCallNext(instr) != nil
}

// bz: check whether the stmt after make closure is defer: they must be in the same basic block
func (a *analysis) isDeferNext(instr *ssa.MakeClosure) *ssa.Defer {
	stmts := instr.Block().Instrs
	for i, stmt := range stmts {
		if stmt == instr { //start check from here
			for j := i + 1; j < len(stmts); j++ {
				deferInstr, ok := stmts[j].(*ssa.Defer) //if the go if available
				if ok {
					val := deferInstr.Call.Value
					if closure, ok := val.(*ssa.MakeClosure); ok {
						if instr == closure { //these two values should be the same
							return deferInstr
						}
					} else if _, ok := val.(*ssa.Function); ok {
						args := deferInstr.Call.Args
						for _, arg := range args { // one should be closure
							if instr == arg { //these two values should be the same
								return deferInstr
							}
						}
					}
				}
			}
			if a.config.DEBUG {
				fmt.Println(">>> NO DEFER FOR MAKECLOSURE: " + instr.String())
			}
			return nil //no go instr until end of basic block
		}
	}
	return nil
}

// bz: check whether the stmt after make closure is a call: they must be in the same basic block
//e.g., in /tidb/cmd/benchdb, (*github.com/pingcap/tidb/util/stmtsummary.stmtSummaryByDigestMap).GetMoreThanOnceBindableStmt:
//t18 = make closure (*stmtSummaryByDigestMap).GetMoreThanOnceBindableStmt$1 [t16, t7] func()
//t19 = t18()
func (a *analysis) isCallNext(instr *ssa.MakeClosure) *ssa.Call {
	stmts := instr.Block().Instrs
	for i, stmt := range stmts {
		if stmt == instr { //start check from here
			for j := i + 1; j < len(stmts); j++ {
				callInstr, ok := stmts[j].(*ssa.Call) //if the go if available
				if ok {
					val := callInstr.Call.Value
					if closure, ok := val.(*ssa.MakeClosure); ok {
						if instr == closure { //these two values should be the same
							return callInstr
						}
					} else if _, ok := val.(*ssa.Function); ok {
						args := callInstr.Call.Args
						for _, arg := range args { // one should be closure
							if instr == arg { //these two values should be the same
								return callInstr
							}
						}
					}
				}
			}
			if a.config.DEBUG {
				fmt.Println(">>> NO DEFER FOR MAKECLOSURE: " + instr.String())
			}
			return nil //no go instr until end of basic block
		}
	}
	return nil
}

//bz: default: adjust for data structure changes
func (a *analysis) makeCGNode(fn *ssa.Function, obj nodeid, callersite *callsite) *cgnode {
	//bz: context-insensitive default code;
	//-> the same with 1callsite, where callersite can be null
	singlecs := a.createSingleCallSite(callersite)
	cgn := &cgnode{fn: fn, obj: obj, callersite: singlecs}
	a.cgnodes = append(a.cgnodes, cgn)
	fnIdx := len(a.cgnodes) - 1 // last element of a.cgnodes
	cgn.idx = fnIdx             //initialize --> only here
	return cgn
}

// genRootCalls generates the synthetic root of the callgraph and the
// initial calls from it to the analysis scope, such as main, a test
// or a library.
//
func (a *analysis) genRootCalls() *cgnode {
	r := a.prog.NewFunction("<root>", new(types.Signature), "root of callgraph")
	root := a.makeCGNode(r, 0, nil) //bz: root is a fake node, like to all main.init and main.main

	// TODO(adonovan): make an ssa utility to construct an actual
	// root function so we don't need to special-case site-less
	// call edges.

	// For each main package, call main.init(), main.main().
	for _, mainPkg := range a.config.Mains {
		main := mainPkg.Func("main")
		if main == nil {
			panic(fmt.Sprintf("%s has no main function", mainPkg))
		}

		targets := a.addOneNode(main.Signature, "root.targets", nil)
		site := &callsite{targets: targets}
		root.sites = append(root.sites, site)
		for _, fn := range [2]*ssa.Function{mainPkg.Func("init"), main} {
			if a.log != nil {
				fmt.Fprintf(a.log, "\troot call to %s:\n", fn)
			}
			if a.considerMyContext(fn.String()) { //bz: give the init/main method a context, instead of using shared contour
				a.copy(targets, a.valueNodeInvoke(root, site, fn), 1)
			} else {
				a.copy(targets, a.valueNode(fn), 1)
			}
		}
	}

	return root
}

//bz: whether the function name follows the go test form: https://golang.org/pkg/testing/
func (a *analysis) isGoTestForm(name string) bool {
	if strings.Contains(name, "$") {
		return false //closure
	}
	if strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "Benchmark") || strings.HasPrefix(name, "Example") {
		return true
	}
	return false
}

// genFunc generates constraints for function fn.
//bz: update to avoid duplicate handling of my synthetic fn/cgn
func (a *analysis) genFunc(cgn *cgnode) {
	fn := cgn.fn

	impl := a.findIntrinsic(fn)

	if a.log != nil {
		fmt.Fprintf(a.log, "\n\n==== Generating constraints for %s, %s\n", cgn, cgn.contour(a.config.CallSiteSensitive))

		// Hack: don't display body if intrinsic.
		if impl != nil {
			fn2 := *cgn.fn // copy
			fn2.Locals = nil
			fn2.Blocks = nil
			fn2.WriteTo(a.log)
		} else {
			cgn.fn.WriteTo(a.log)
		}
	}

	if impl != nil {
		impl(a, cgn)
		return
	}

	if fn.Blocks == nil {
		// External function with no intrinsic treatment.
		// We'll warn about calls to such functions at the end.
		return
	}

	if a.log != nil {
		fmt.Fprintln(a.log, "; Creating nodes for local values")
	}

	//bz: we do replace a.localval and a.localobj by cgn's
	reuse := cgn.initLocalMaps()
	a.localval = cgn.localval
	a.localobj = cgn.localobj
	////bz: default code below
	//a.localval = make(map[ssa.Value]nodeid)
	//a.localobj = make(map[ssa.Value]nodeid)

	if reuse { //bz: from my synthetic fn, and solved in last loop of preSolve() already -> now handle the diff
		// Create value nodes for all value instructions
		// since SSA may contain forward references.
		var space [10]*ssa.Value
		idx := fn.SyntInfo.CurBlockIdx
		for _, instr := range fn.Blocks[idx].Instrs {
			switch instr := instr.(type) {
			case *ssa.Range:
				// do nothing: it has a funky type,
				// and *ssa.Next does all the work.

			case ssa.Value:
				var comment string
				if a.log != nil {
					comment = instr.Name()
				}
				id := a.addNodes(instr.Type(), comment)
				a.setValueNode(instr, id, cgn) //bz: my synthetic should only update a.localval for instr
			}

			// Record all address-taken functions (for presolver).
			rands := instr.Operands(space[:0])
			if call, ok := instr.(ssa.CallInstruction); ok && !call.Common().IsInvoke() {
				// Skip CallCommon.Value in "call" mode.
				// TODO(adonovan): fix: relies on unspecified ordering.  Specify it.
				rands = rands[1:]
			}
			for _, rand := range rands { //bz: my synthetic should not update this
				if atf, ok := (*rand).(*ssa.Function); ok {
					a.atFuncs[atf] = true // bz: what is this ??
				}
			}
		}

		//bz: we only want to track global values used in the app methods
		a.isWithinScope = a.considerMyContext(fn.String()) //global bool
		// Generate constraints for instructions.
		for _, instr := range fn.Blocks[idx].Instrs {
			a.genInstr(cgn, instr)
		}

		a.localval = nil
		a.localobj = nil
		return
	}

	// The value nodes for the params are in the func object block.
	params := a.funcParams(cgn.obj)
	for _, p := range fn.Params {
		a.setValueNode(p, params, cgn)
		params += nodeid(a.sizeof(p.Type()))
	}

	// Free variables have global cardinality:
	// the outer function sets them with MakeClosure;
	// the inner function accesses them with FreeVar.
	//
	// TODO(adonovan): treat free vars context-sensitively.

	// Create value nodes for all value instructions
	// since SSA may contain forward references.
	var space [10]*ssa.Value
	for _, b := range fn.Blocks {
		for _, instr := range b.Instrs {
			switch instr := instr.(type) {
			case *ssa.Range:
				// do nothing: it has a funky type,
				// and *ssa.Next does all the work.

			case ssa.Value:
				var comment string
				if a.log != nil {
					comment = instr.Name()
				}
				id := a.addNodes(instr.Type(), comment)
				a.setValueNode(instr, id, cgn) //bz: my synthetic should only update a.localval for instr
			}

			// Record all address-taken functions (for presolver).
			rands := instr.Operands(space[:0])
			if call, ok := instr.(ssa.CallInstruction); ok && !call.Common().IsInvoke() {
				// Skip CallCommon.Value in "call" mode.
				// TODO(adonovan): fix: relies on unspecified ordering.  Specify it.
				rands = rands[1:]
			}
			for _, rand := range rands { //bz: my synthetic should not update this
				if atf, ok := (*rand).(*ssa.Function); ok {
					a.atFuncs[atf] = true // bz: what is this ??
				}
			}
		}
	}

	//bz: we only want to track global values used in the app methods
	a.isWithinScope = a.considerMyContext(fn.String()) //global bool
	// Generate constraints for instructions.
	for _, b := range fn.Blocks {
		for _, instr := range b.Instrs {
			a.genInstr(cgn, instr)
		}
	}

	a.localval = nil
	a.localobj = nil
}

// genMethodsOf generates nodes and constraints for all methods of type T.
func (a *analysis) genMethodsOf(T types.Type) {
	itf := isInterface(T)

	// TODO(adonovan): can we skip this entirely if itf is true?
	// I think so, but the answer may depend on reflection.
	mset := a.prog.MethodSets.MethodSet(T)
	for i, n := 0, mset.Len(); i < n; i++ {
		m := a.prog.MethodValue(mset.At(i))
		a.valueNode(m)

		if a.recordPreGen { //bz: performance info
			a.preGens = append(a.preGens, m)
		}

		if !itf {
			// Methods of concrete types are address-taken functions.
			a.atFuncs[m] = true
		}
	}
}

// generate generates offline constraints for the entire program.
func (a *analysis) generate() {
	start("Constraint generation")
	if a.log != nil {
		fmt.Fprintln(a.log, "==== Generating constraints")
	}

	// Create a dummy node since we use the nodeid 0 for
	// non-pointerlike variables.
	a.addNodes(tInvalid, "(zero)")

	// Create the global node for panic values.
	a.panicNode = a.addNodes(tEface, "panic")

	// Create nodes and constraints
	//for all methods of reflect.rtype.
	// (Shared contours are used by dynamic calls to reflect.Type methods---typically just String().)
	if flags.DoPerformance {
		a.recordPreGen = true
	}
	if rtype := a.reflectRtypePtr; rtype != nil {
		a.genMethodsOf(rtype) //bz: generate cgns for all fns of type *reflect.rtype
	}
	a.recordPreGen = false

	root := a.genRootCalls()

	if a.config.BuildCallGraph { //bz: i added
		a.result.CallGraph = NewWCtx(root)
	}

	// Create nodes and constraints for all methods of all types
	// that are dynamically accessible via reflection or interfaces.
	// default code: use a.genMethodsOf()
	// bz: skip the following code to generate fn/cgn and their constraints for shared contour,
	//   we want it on-the-fly, or at least semi-on-the-fly (i.e., callback)
	if a.log != nil || optHVN {
		skip := 0 //bz: assist a.skipTypes
		for _, T := range a.prog.RuntimeTypes() {
			typ := T.String()
			if a.log != nil {
				if a.considerMyContext(typ) {
					fmt.Fprintf(a.log, "SKIP genMethodsOf() offline for type: "+T.String()+"\n")
				} else {
					fmt.Fprintf(a.log, "EXCLUDE genMethodsOf() offline for type: "+T.String()+"\n")
				}
			}
			if optHVN { //record
				a.skipTypes[typ] = typ
			}
			skip++
		}

		if a.log != nil {
			fmt.Fprintf(a.log, "\nDone genMethodsOf() offline. \n\n")

			if optHVN {
				fmt.Fprintf(a.log, "\n#Excluded types in genMethodsOf() offline (not function): %d\n", skip)
				fmt.Fprintf(a.log, "Dump out skipped types:  \n")
				for _, typ := range a.skipTypes {
					fmt.Fprintf(a.log, typ+"\n")
				}
			}
		}
	}

	// Generate constraints for functions as they become reachable
	// from the roots.  (No constraints are generated for functions
	// that are dead in this analysis scope.)
	//---> bz: want to generate cgn called by interfaces here, so it can have kcfa not shared contour
	//Update: bz: only generate if it is in app scope, since we have a lot of untagged obj panics happens
	//if we create constraints for some lib function here
	for len(a.genq) > 0 {
		cgn := a.genq[0]
		a.genq = a.genq[1:]
		a.genFunc(cgn)
	}

	// The runtime magically allocates os.Args; so should we.
	//bz: we are trying to skip this iff "runtime" and "os" are both in exclusions
	if os := a.prog.ImportedPackage("os"); os != nil {
		// In effect:  os.Args = new([1]string)[:]
		T := types.NewSlice(types.Typ[types.String])
		obj := a.addNodes(sliceToArray(T), "<command-line args>")
		a.endObject(obj, nil, "<command-line args>")
		a.addressOf(T, a.objectNode(nil, os.Var("Args")), obj)
	}

	// Discard generation state, to avoid confusion after node renumbering.
	if a.config.K == 0 { //bz: when we are using kcfa or origin, we still need this when genInvoke on-the-fly
		a.panicNode = 0
		a.globalval = nil
	}
	a.localval = nil
	a.localobj = nil

	if a.config.DoCallback {
		a.preSolve()
	}

	stop("Constraint generation")
}

//bz: the scalability problem now is: there is repeated pts update for the same pointer everytime when there is new obj discovered during genConstraintsOnline()
// this leads to so bad performance
// try -> we create a list of type-tagged obj, match the type of obj with the receiver type of each invokeConstraint, if matched, we do genInstr() with the given obj and callersite
//        because probably this obj will be propagated to the receiver later during solve(); we do the renumbering and HVN afterwards, which probably will not mess up the two opts like before
// This generates an over-approximate result than on-the-fly
func (a *analysis) preSolve() {
	start("My PreSolving")
	if a.log != nil {
		fmt.Fprintln(a.log, "\n\n\n==== PreSolving Constraints")
	}

	cIdx := -1                                               //for this iteration, what are the start point of constraints (cIdx) to presolve
	cNextIdx := 0                                            //for next iteration
	nIdx := 0                                                //for next iteration, a.nodes traversal
	iface2invoke := make(map[types.Type][]*invokeConstraint) //implicitly supplied to the concrete method implementation <-> [] of its invoked constraints
	newCons := true                                          //whether this iteration has new invoke constraints
	newNodes := true                                         //whether this iteration has new nodes created
	newCB := true                                            //whether this iteration has new callback functions created
	newFn := true                                            //whether this iteration has new missing functions created

	for cIdx < cNextIdx || newCons || newNodes || newCB || newFn {
		//update idx: traverse from cidx to len(a.xxx) for this iteration
		cIdx = cNextIdx
		cNextIdx = len(a.constraints)
		//reset all bools
		newCons = false
		newNodes = false
		newCB = false
		newFn = false

		if a.log != nil {
			fmt.Fprintln(a.log, "Iteration ", a.curIter, ": From", cIdx, " to ", cNextIdx)
		}

		//organize invoke constraints
		newCons = a.organizeInvokeConstraints(cIdx, cNextIdx, iface2invoke)

		//check if obj can be receiver: traverse all is necessary
		start := 0
		if !newCons { //bz: if there is no new invoke constraints, we just traverse diff nodes on all existing invokes
			start = nIdx
		}
		numNodes := len(a.nodes)
		for i := start; i < numNodes; i++ {
			node := a.nodes[i]
			if node.obj == nil {
				continue //not an obj
			}
			flags := node.obj.flags
			if flags&otTagged == 0 || flags&otIndirect != 0 {
				continue //not-tagged obj or indirect tagged object -> cannot be used during a.solve(), will panic
			}

			tDyn := node.typ                                               //the type of this obj: real struct type
			if _, ok := tDyn.(*types.Signature); ok || isInterface(tDyn) { //func body or interface
				continue
			}

			//from genInvoke: do not remove solved iface from iface2invoke -> lead to missing unsolved constraints
			for typ, invokes := range iface2invoke {
				ityp, ok := typ.Underlying().(*types.Interface)
				if ok && types.Implements(tDyn, ityp) {
					if a.log != nil {
						fmt.Fprintln(a.log, "Handle invokes for tDyn: ", tDyn, " \n\tiface: ", ityp)
					}

					for _, c := range invokes { //similar to func (c *invokeConstraint) solve()
						fn := a.prog.LookupMethod(tDyn, c.method.Pkg(), c.method.Name())
						if fn == nil {
							if a.log != nil {
								fmt.Fprintf(a.log, "\t -> n%d: no ssa.Function for %s\n", c.iface, c.method)
							}
							continue
						}

						var fnObj nodeid
						if c.site != nil && c.caller != nil {
							//loopID here should be consistent with caller's loopID -> the context of an invoked target should be consistent with its caller; no go/closure here
							loopID := 0
							if c.caller.callersite[0] != nil {
								loopID = c.caller.callersite[0].loopID
							}
							_, _, fnObj, _ = a.existContext(fn, c.site, c.caller, loopID) //check existence
						} else {
							fnObj = a.globalobj[fn] // dynamic calls use shared contour  ---> bz: fnObj is nodeid
						}
						if fnObj == 0 { //a.objectNode(fn) was not called during gen phase -> call genInstr() here to create its constraints and everything
							fnObj = a.genMissingFn(fn, c.caller, c.site, "presolve")
							if fnObj != 0 { //return 0 for out of scope functions
								newFn = true
							}
						}
					}
				}
			}
		}

		if a.log != nil {
			fmt.Fprintf(a.log, "\nAnalyze cgns from genCallBack(). \n")
		}

		//bz: from genCallBack, we solve these at the end, since preSolve() may also add new calls (multiple time in the above loop) to the following cgns
		if len(a.gencb) > 0 {
			newCB = true
			for len(a.gencb) > 0 {
				cgn := a.gencb[0]
				a.gencb = a.gencb[1:]
				a.genFunc(cgn)
			}
		}

		if numNodes < len(a.nodes) { //bz: might have new obj created
			newNodes = true
			nIdx = numNodes
		}

		a.curIter++ //update here -> all before preSolve and 0 iteration of perSolve have the same basic block
		fmt.Println("end of iteration ", a.curIter-1, " ------------- #origins: ", a.numOrigins, " ------------- #constraints: ", len(a.constraints),
			" ------------- #face2invokes: ", len(iface2invoke), " ------------- #nodes: ", len(a.nodes))
	}
	stop("My PreSolving")
}

//bz: organize invoke constraints
func (a *analysis) organizeInvokeConstraints(cIdx, cNextIdx int, iface2invoke map[types.Type][]*invokeConstraint) bool {
	if cIdx == cNextIdx {
		return false //nothing new
	}

	updated := false
	for i := cIdx; i < cNextIdx; i++ {
		c := a.constraints[i]
		if invoke, ok := c.(*invokeConstraint); ok {
			typ := a.nodes[invoke.iface].typ //this is an interface
			invokes := iface2invoke[typ]
			if invokes != nil { //exist mapping -> we are always checking new constraints, there should be no duplicate invokes appended here
				invokes = append(invokes, invoke)
			} else { //new iface
				invokes = make([]*invokeConstraint, 1)
				invokes[0] = invoke
			}
			iface2invoke[typ] = invokes
			updated = true
		}
	}
	return updated
}

//bz: generate constraints and everything for fn, since they are not created during generate()@go/pointer/gen.go:2630
// return 0 for out of scope functions
func (a *analysis) genMissingFn(fn *ssa.Function, caller *cgnode, site *callsite, where string) nodeid {
	if a.log != nil { //debug
		fmt.Fprintln(a.log, "\n------------- GENERATING INVOKE FUNC HERE: (", where, ") "+fn.String()+" ------------------------------ ")
	}

	var fnObj nodeid
	if a.considerMyContext(fn.String()) {
		if a.config.DEBUG {
			if caller == nil && site == nil {
				fmt.Println("!! GENERATING INVOKE FUNC ONLINE (share contour): " + fn.String())
			} else { //bz: special handling of invoke targets, create here
				fmt.Println("!! GENERATING INVOKE FUNC ONLINE (ctx-sensitive): " + fn.String())
			}
		}
		fnObj = a.genInvokeOnline(caller, site, fn) //caller and site can be nil
	} else { //newly created app func invokes lib func: use share contour
		if !a.createForLevelX(nil, fn) {
			if a.config.DEBUG {
				fmt.Println("Level excluded: " + fn.String())
			}
			if a.log != nil { //debug
				fmt.Fprintf(a.log, "Level excluded: "+fn.String()+"\n")
			}
			if a.config.DoCallback && fn.IsMySynthetic { //bz: this is my callback fn, we store new cgnodes in a.gencb and a.genq.
				// Note: run this code only if we turn on DoCallback + preSolve; TODO: if only turn on DoCallback, a.gencb will not be solved ...
				instr := site.instr.(ssa.CallInstruction)
				call := instr.Common()
				a.genCallBack(caller, instr, fn, site, call)
			}
			return 0 //not consider here
		}

		fnObj = a.genInvokeOnline(nil, nil, fn) //bz: if reaches here, fn can only be lib from import
	}
	if a.log != nil { //debug
		fmt.Fprintf(a.log, "------------------------------ ------------------------------ ---------------------------- \n")
	}

	// bz: we continue our Online process
	if a.log != nil { //debug
		fmt.Fprintf(a.log, "\n----------- GENERATING CONSTRAINTS HERE (%s) -------------------------- -----------------------\n", where)
	}

	a.genConstraintsOnline()

	if a.log != nil { //debug
		fmt.Fprintf(a.log, "\n------------------------------ ------------------------------ ---------------------------- \n")
	}

	return fnObj
}
