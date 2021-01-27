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
	"github.tamu.edu/April1989/go_tools/container/intsets"
	"github.tamu.edu/April1989/go_tools/go/ssa"
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

	withinScope = false //bz: whether the current genInstr() is working on a method within our scope
	Online      = false //bz: whether a constraint is from genOnline()
)

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

	if a.config.DiscardQueries {
		return //bz: skip recording queries
	}

	// Default: Due to context-sensitivity, we may encounter the same Value
	// in many contexts. We merge them to a canonical node, since
	// that's what all clients want.
	// Record the (v, id) relation if the client has queried pts(v).
	//!!!! bz : this part is evil ... they may considered the performance issue,
	// BUT we want to directly query after running pointer analysis, not run after each query...
	// from the code@https://github.tamu.edu/jeffhuang/go2/blob/master/race_checker/pointerAnalysis.go
	// seems like we only query pointers, so CURRENTLY only record for pointers in app methods
	// -> go to commit@acb4db0349f131f8d10ddbec6d4fb686258becca (or comment out below for now)
	// to check original code
	t := v.Type()
	if cgn == nil {
		if !withinScope {
			return // not interested
		}
		//bz: this might be the root cgn, interface, from global, etc.
		//NOW, put the a.globalobj[] also into query, since a lot of thing is stored there, e.g.,
		//*ssa.FreeVar (but I PERSONALLY do not want *ssa.Function, *ssa.Global, *ssa.Function, *ssa.Const,
		//exclude now)
		if a.log != nil {
			fmt.Fprintf(a.log, "nil cgn in setValueNode(): v:"+v.Type().String()+" "+v.String()+"\n")
		}
		switch v.(type) {
		case *ssa.FreeVar:
			//bz: Global object. But are they unique mapping/replaced when put into a.globalval[]?
			// a.globalobj[v] = n0  --> nothing stored, do not use this
			a.recordGlobalQueries(t, cgn, v, id)
		case *ssa.Global:
			//Updated: bz: capture global var, e.g., race_checker/tests/runc_simple.go:31
			a.recordGlobalQueries(t, cgn, v, id)
		}
		return //else: nothing to record
	}

	if a.withinScope(cgn.fn.String()) { //record queries
		//if a.config.DEBUG {
		//	fmt.Println("query (in): " + t.String())
		//}
		if CanPoint(t) {
			a.recordQueries(t, cgn, v, id)
		} else
		//bz: this condition is copied from go2: indirect queries
		if underType, ok := v.Type().Underlying().(*types.Pointer); ok && CanPoint(underType.Elem()) {
			a.recordIndirectQueries(t, cgn, v, id)
		} else { //bz: extended queries for debug --> might be global (cgn == nil) or local (cgn != nil)
			a.recordExtendedQueries(t, cgn, v, id)
		}
	}
}

func (a *analysis) recordExtendedQueries(t types.Type, cgn *cgnode, v ssa.Value, id nodeid) {
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
func (a *analysis) recordGlobalQueries(t types.Type, cgn *cgnode, v ssa.Value, id nodeid) {
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
		cgn:  cgn,
		data: data,
	}
	objNode.obj = o //bz: points-to heap ? this is the only place that assigned to node.obj

	return o
}

// makeFunctionObject creates and returns a new function object
// (contour) for fn, and returns the id of its first node.  It also
// enqueues fn for subsequent constraint generation.
//
// For a context-sensitive contour, callersite identifies the sole
// callsite; for shared contours, caller is nil.
//
func (a *analysis) makeFunctionObject(fn *ssa.Function, callersite *callsite) nodeid {
	if a.log != nil {
		fmt.Fprintf(a.log, "\t---- makeFunctionObject %s\n", fn)
	}

	// obj is the function object (identity, params, results).
	obj := a.nextNode()
	cgn := a.makeCGNode(fn, obj, callersite)
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
func (a *analysis) makeFunctionObjectWithContext(caller *cgnode, fn *ssa.Function, callersite *callsite, closure *ssa.MakeClosure, loopID int) (nodeid, bool) {
	if a.log != nil {
		fmt.Fprintf(a.log, "\t---- makeFunctionObjectWithContext (kcfa) %s\n", fn)
	}

	if a.config.DEBUG {
		fmt.Printf("\t---- makeFunctionObjectWithContext (kcfa) for %s\n", fn)
	}

	if a.config.Origin && callersite == nil && closure != nil {
		//origin: case 2: fn is make closure -> we checked before calling this, now needs to create it
		// and will update the a.closures[] outside
		// !! for origin, this is the only place triggering context switch !!
		obj, _ := a.makeCGNodeAndRelated(fn, caller, nil, closure, loopID)
		return obj, true
	}

	//if we can find an existing cgnode/obj -> update: remove the duplicate cgnode added to a.genq[]
	var existFnIdx []int
	var multiFn bool
	var existNodeID nodeid
	var isNew bool
	if a.config.Origin { //bz: origin -> we only create new callsite[] for specific instr, not everyone
		if _, ok := callersite.instr.(*ssa.Go); callersite == nil || ok {
			existFnIdx, multiFn, existNodeID, isNew = a.existContextForComb(fn, callersite, caller)
			if !isNew {
				return existNodeID, isNew
			}
		} else {
			existFnIdx, multiFn, existNodeID, isNew = a.existContextFor(fn, caller)
			if !isNew {
				return existNodeID, isNew
			}
		}
	} else if a.config.CallSiteSensitive { //bz: for kcfa
		existFnIdx, multiFn, existNodeID, isNew = a.existContextForComb(fn, callersite, caller)
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
	a.updateFn2NodeID(fn, multiFn, newFnIdx, existFnIdx)

	return obj, true
}

//bz: if exist this caller for fn?
func (a *analysis) existContextFor(fn *ssa.Function, caller *cgnode) ([]int, bool, nodeid, bool) {
	existFnIdx, multiFn := a.fn2cgnodeIdx[fn]
	if multiFn { //check if we already have the caller + callsite ?? recursive/duplicate call
		for i, existIdx := range existFnIdx { // idx -> index of fn cgnode in a.cgnodes[]
			_fnCGNode := a.cgnodes[existIdx]
			if a.equalContextFor(_fnCGNode.callersite, caller.callersite) { //check all callsites
				//duplicate combination, return this
				if a.log != nil { //debug
					fmt.Fprintf(a.log, "    EXIST**: "+strconv.Itoa(i+1)+"th: K-CALLSITE -- "+_fnCGNode.contourkFull()+"\n")
				}
				if a.config.DEBUG {
					fmt.Printf("    EXIST**: " + strconv.Itoa(i+1) + "th: K-CALLSITE -- " + _fnCGNode.contourkFull() + "\n")
				}
				return existFnIdx, multiFn, _fnCGNode.obj, false
			}
		}
	}
	return existFnIdx, multiFn, 0, true
}

//bz: if two existCSs and curCallerCSs are the same
func (a *analysis) equalContextFor(existCSs []*callsite, curCallerCSs []*callsite) bool {
	if len(existCSs) != len(curCallerCSs) {
		return false
	}
	for _, existCS := range existCSs {
		for _, curCallerCS := range curCallerCSs {
			if !existCS.equal(curCallerCS) {
				return false
			}
		}
	}
	return true
}

//bz: if exist this callsite + caller for fn?
func (a *analysis) existContextForComb(fn *ssa.Function, callersite *callsite, caller *cgnode) ([]int, bool, nodeid, bool) {
	existFnIdx, multiFn := a.fn2cgnodeIdx[fn]
	if multiFn { //check if we already have the caller + callsite ?? recursive/duplicate call
		for i, existIdx := range existFnIdx { // idx -> index of fn cgnode in a.cgnodes[]
			_fnCGNode := a.cgnodes[existIdx]
			if a.equalContextForComb(_fnCGNode.callersite, callersite, caller.callersite) { //check all callsites
				//duplicate combination, return this
				if a.log != nil { //debug
					fmt.Fprintf(a.log, "    EXIST**: "+strconv.Itoa(i+1)+"th: K-CALLSITE -- "+_fnCGNode.contourkFull()+"\n")
				}
				if a.config.DEBUG {
					fmt.Printf("    EXIST**: " + strconv.Itoa(i+1) + "th: K-CALLSITE -- " + _fnCGNode.contourkFull() + "\n")
				}
				return existFnIdx, multiFn, _fnCGNode.obj, false
			}
		}
	}
	return existFnIdx, multiFn, 0, true
}

//bz: if two (existCSs == cur + curCallerCSs) are the same
func (a *analysis) equalContextForComb(existCSs []*callsite, cur *callsite, curCallerCSs []*callsite) bool {
	for i, existCS := range existCSs {
		switch i {
		case 0: //[0] is the most recent
			if !existCS.equal(cur) {
				return false
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
	//TODO: this ifelse is a bit huge ....
	if a.config.Mains[0].Func("main") == fn { //bz: give the main method a context, instead of using shared contour
		single := a.createSingleCallSite(callersite)
		cgn = &cgnode{fn: fn, obj: obj, callersite: single}

	} else {                 // other functions
		if a.config.Origin { //bz: for origin-sensitive
			if callersite == nil { //we only create new context for make closure and go instruction
				var fnkcs []*callsite
				goInstr := a.isGoNext(closure)
				if goInstr != nil { //case 2: create one with only target, make closure is not ssa.CallInstruction
					special := &callsite{targets: obj, goInstr: goInstr}
					if loopID != -1 { //handle loop TODO: will this affect exist checking?
						special = &callsite{targets: obj, loopID: loopID, goInstr: goInstr}
					}
					fnkcs = a.createKCallSite(caller.callersite, special)
				} else { // use parent context, since no go invoke afterwards (currently reachable);
					//update: we will update the parent ctx (including loopID) later
					a.closureWOGo[obj] = obj //record
					fnkcs = caller.callersite
				}
				cgn = &cgnode{fn: fn, obj: obj, callersite: fnkcs}
			} else if goInstr, ok := callersite.instr.(*ssa.Go); ok { //case 1 and 3: this is a *ssa.GO without closure
				special := callersite
				special.goInstr = goInstr //update
				if loopID != -1 {         //handle loop TODO: will this affect exist checking?
					special = &callsite{targets: callersite.targets, instr: callersite.instr, loopID: loopID, goInstr: goInstr}
				}
				fnkcs := a.createKCallSite(caller.callersite, special)
				cgn = &cgnode{fn: fn, obj: obj, callersite: fnkcs}
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
	a.endObject(obj, cgn, fn).flags |= otFunction

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
		panic(fmt.Sprintf("not a tagged object: n%d", obj))
	}
	return n.typ, obj + 1, flags&otIndirect != 0
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

		if Online { //bz: Online solving
			a.addWork(dst)
			if a.log != nil {
				fmt.Fprintf(a.log, "%s\n", " -> add Online constraint to worklist: "+dst.String()+" "+src.String())
			}
		}

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
	if withinScope || a.shouldTrack(T) {
		a.addConstraint(&addrConstraint{id, obj})
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
	if !withinScope && !a.shouldTrack(T) {
		return
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

//bz: do pta use our context-sensitive algo? which algo? if true, must be in scope
func (a *analysis) considerMyContext(fn string) bool {
	return a.considerKCFA(fn) || a.considerOrigin(fn)
}

//bz: return whether we do origin-sensitive on this closure !!! only on closure
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
		if strings.Contains(method, "command-line-arguments") { //default scope
			return true
		} else {
			if len(a.config.Scope) > 0 { //user assigned scope
				for _, pkg := range a.config.Scope {
					if strings.Contains(method, pkg) && !strings.Contains(method, "google.golang.org/grpc/grpclog") {
						return true
					}
				}
			}
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

//bz: to check whether a go routine is inside a loop; also record the result in *analysis
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

//  ------------- bz : the following several functions generate constraints for different method calls --------------
// genStaticCall generates constraints for a statically dispatched function call.
// bz: force call site here
func (a *analysis) genStaticCall(caller *cgnode, instr ssa.CallInstruction, site *callsite, call *ssa.CallCommon, result nodeid) {
	fn := call.StaticCallee()
	if !a.withinScope(fn.String()) && !a.withinScope(caller.fn.String()) {
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
	//var isNew bool //bz: whether obj is a new cgnode

	//bz: for origin-sensitive, we have two cases:
	//case 1: no closure, directly invoke static function: e.g., go Producer(t0, t1, t3), we create a new context for it
	//case 2: has closure: make closure has been created earlier, here find the çreated obj and use its context
	//case 3: no closure, but invoke virtual function: e.g., go (*ccBalancerWrapper).watcher(t0), we create a new context for it
	if a.considerMyContext(fn.String()) {
		//bz: simple brute force solution; start to be kcfa from main.main.go
		if a.config.DEBUG {
			fmt.Println("CAUGHT APP METHOD -- " + fn.String()) //debug
		}
		_, ok := site.instr.(*ssa.Go)
		if ok { //bz: invoke a go routine --> detail check for different context-sensitivities
			if a.config.DEBUG { //debug
				fmt.Println("        BUT ssa.GO -- " + site.instr.String() + "   LET'S SEE.")
			}
			a.genStaticCallForGoCall(caller, instr, site, call, result) //bz: direct the handling outside and return
			return
		}

		//for kcfa: we need a new contour
		//for origin: whatever left, we use caller context
		obj, _ = a.makeFunctionObjectWithContext(caller, fn, site, nil, -1)
	} else {
		//default: context-insensitive
		if a.shouldUseContext(fn) {
			obj = a.makeFunctionObject(fn, site) // new contour
		} else {
			obj = a.objectNode(nil, fn) // shared contour
		}
	}

	if obj != 0 {
		a.genStaticCallCommon(caller, obj, site, call, result)
	}
}

//bz: we take these special cases for *ssa.Go outside normal workflow; both kcfa and origin
func (a *analysis) genStaticCallForGoCall(caller *cgnode, instr ssa.CallInstruction, site *callsite, call *ssa.CallCommon, result nodeid) {
	fn := call.StaticCallee()
	objs, ok, _ := a.existClosure(fn, caller.callersite[0])
	if ok { //exist closure, add its new context, but no constraints
	} else { //bz: case 1 and 3: we need a new contour and a new context for origin
		if a.config.Origin && a.isInLoop(fn, instr) {
			objs = make([]nodeid, 2)
			//bz: loop problem -> two origin contexts here
			obj1, _ := a.makeFunctionObjectWithContext(caller, fn, site, nil, 1)
			objs[0] = obj1
			obj2, _ := a.makeFunctionObjectWithContext(caller, fn, site, nil, 2)
			objs[1] = obj2
		} else { //kcfa (no loop handling); and origin with no loop
			objs = make([]nodeid, 1)
			obj, _ := a.makeFunctionObjectWithContext(caller, fn, site, nil, -1)
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
// bz: working on this
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
func (a *analysis) genOnline(caller *cgnode, site *callsite, fn *ssa.Function) nodeid {
	//set status
	Online = true

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

	Online = false //set back
}

//bz: special handling for invoke, doing something like genMethodsOf() and valueNode() for invoke calls; called Online
//must be global
func (a *analysis) valueNodeInvoke(caller *cgnode, site *callsite, fn *ssa.Function) nodeid {
	if caller == nil && site == nil { //requires shared contour
		obj := a.valueNode(fn)
		return obj + 1 //bz: why obj is not the right one? not tagged with otFunction ??
	}

	//similar with valueNode(),  created on demand. Instead of a.globalval[], we use a.fn2cgnodeid[]
	_, _, obj, isNew := a.existContextForComb(fn, site, caller)
	if isNew {
		var comment string
		if a.log != nil {
			comment = fn.String()
		}
		var id = a.addNodes(fn.Type(), comment)
		if obj = a.objectNodeSpecial(caller, nil, site, fn, -1); obj != 0 {
			a.addressOf(fn.Type(), id, obj)
		}
		//a.setValueNode(m, id, nil) //bz: do we need this?? for now, no since we will not use it
		a.atFuncs[fn] = true // Methods of concrete types are address-taken functions.
		return obj
	}
	return obj
}

// genInvoke generates constraints for a dynamic method invocation.
// bz: NOTE: not every x.m() is treated by genInvoke() here, some is treated by genStaticCall(),
// move the genFunc() for invoke calls Online
func (a *analysis) genInvoke(caller *cgnode, site *callsite, call *ssa.CallCommon, result nodeid) {
	if call.Value.Type() == a.reflectType {
		a.genInvokeReflectType(caller, site, call, result)
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
//bz: for now use 1-callsite (original defaut), TODO: if necessary, update to kcfa
func (a *analysis) genInvokeReflectType(caller *cgnode, site *callsite, call *ssa.CallCommon, result nodeid) {
	// Look up the concrete method.
	fn := a.prog.LookupMethod(a.reflectRtypePtr, call.Method.Pkg(), call.Method.Name())
	if !a.withinScope(fn.String()) && !a.withinScope(caller.fn.String()) {
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
	ids, ok, _ := a.existClosure(fn, callersite) //checked
	if !ok {                                     //not exist
		var comment string
		if a.log != nil {
			comment = v.String()
		}

		var objs []nodeid
		if a.config.Origin && a.isInLoop(cgn.fn, closure) { // handle loop only in origin-sensitive
			ids = make([]nodeid, 2)
			objs = make([]nodeid, 2)
			loopID := 1 //cannot find a better iterator ...
			for loopID < 3 {
				id, obj := a.valueNodeClosureInternal(cgn, closure, v, loopID, comment)
				//udpate
				ids[loopID-1] = id
				objs[loopID-1] = obj
				loopID++
			}
		} else { // create a single cgnode
			ids = make([]nodeid, 1)
			objs = make([]nodeid, 1)
			id, obj := a.valueNodeClosureInternal(cgn, closure, v, -1, comment)
			//udpate
			ids[0] = id
			objs[0] = obj
		}

		//update for new closures
		var c2id = make(map[*callsite][]nodeid)
		c2id[callersite] = objs
		a.closures[fn] = &Ctx2nodeid{c2id} //we do not know the matching go instr now
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
func (a *analysis) existClosure(fn *ssa.Function, callersite *callsite) ([]nodeid, bool, map[*callsite][]nodeid) {
	_map, ok := a.closures[fn]
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

//bz: special handling for objectNode()
func (a *analysis) objectNodeSpecial(cgn *cgnode, closure *ssa.MakeClosure, site *callsite, v ssa.Value, loopID int) nodeid {
	fn, _ := v.(*ssa.Function)      // must be Global object.
	if a.withinScope(fn.String()) { //create cgnode/constraints here;
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
				if a.considerMyContext(v.String()) {
					if isClosure {
						panic("WRONG PATH @objectNode() FOR MAKE CLOSURE: " + v.String())
					}
				} else { //normal case
					obj = a.makeFunctionObject(v, nil)
				}

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

		case *ssa.MakeSlice:
			obj = a.nextNode()
			a.addNodes(sliceToArray(v.Type()), "makeslice")
			a.endObject(obj, cgn, v)

		case *ssa.MakeChan:
			obj = a.nextNode()
			a.addNodes(v.Type().Underlying().(*types.Chan).Elem(), "makechan")
			a.endObject(obj, cgn, v)

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

	case *ssa.Alloc, *ssa.MakeSlice, *ssa.MakeChan, *ssa.MakeMap, *ssa.MakeInterface:
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
		} else { //context-insensitive
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

			// Is key valid?
			if tTuple.At(1).Type() != tInvalid {
				sz += ksize
			} else {
				odst += ksize
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

// bz: check whether the stmt after make closure is go: they must be in the same basic block
// Update: this is too strict, things happens in Kubernetes88331: (*command-line-arguments.PriorityQueue).Run
// now update to if there exists a go that refers instr, but they still must be in the same basic block
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
					} else if _, ok := val.(*ssa.Function); ok {
						args := goInstr.Call.Args
						closure := args[0]    // this should be closure
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

//bz: adjust for data structure changes
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
	root := a.makeCGNode(r, 0, nil)

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
			if a.considerMyContext(fn.String()) { //bz: give the main method a context, instead of using shared contour
				a.copy(targets, a.valueNodeInvoke(root, site, fn), 1)
			} else {
				a.copy(targets, a.valueNode(fn), 1)
			}
		}
	}

	return root
}

// genFunc generates constraints for function fn.
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

	if a.config.DiscardQueries {
		//bz: we do replace a.localval and a.localobj by cgn's
		cgn.initLocalMaps()
		a.localval = cgn.localval
		a.localobj = cgn.localobj
	} else {
		a.localval = make(map[ssa.Value]nodeid)
		a.localobj = make(map[ssa.Value]nodeid)
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
		for _, instr := range b.Instrs { //
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
				a.setValueNode(instr, id, cgn)
			}

			// Record all address-taken functions (for presolver).
			rands := instr.Operands(space[:0])
			if call, ok := instr.(ssa.CallInstruction); ok && !call.Common().IsInvoke() {
				// Skip CallCommon.Value in "call" mode.
				// TODO(adonovan): fix: relies on unspecified ordering.  Specify it.
				rands = rands[1:]
			}
			for _, rand := range rands {
				if atf, ok := (*rand).(*ssa.Function); ok {
					a.atFuncs[atf] = true // bz: what is this ??
				}
			}
		}
	}

	//bz: we only want to track global values used in the app methods
	withinScope = a.considerMyContext(fn.String()) //global bool
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

	// Create nodes and constraints for all methods of reflect.rtype.
	// (Shared contours are used by dynamic calls to reflect.Type
	// methods---typically just String().)
	if rtype := a.reflectRtypePtr; rtype != nil {
		a.genMethodsOf(rtype)
	}

	root := a.genRootCalls()

	if a.config.BuildCallGraph { //bz:
		a.result.CallGraph = NewWCtx(root)
	}

	// Create nodes and constraints for all methods of all types
	// that are dynamically accessible via reflection or interfaces.
	for _, T := range a.prog.RuntimeTypes() {
		_type := T.String()
		if a.considerMyContext(_type) {
			//bz: we want to make function (called by interfaces) later for kcfa, here uses share contour
			if a.log != nil {
				fmt.Fprintf(a.log,"SKIP genMethodsOf() offline for type: " + T.String() + "\n")
			}
			continue
		}

		if a.withinScope(_type) || a.fromImports(_type) { //bz: generate for both
			a.genMethodsOf(T)
		} else {
			if a.log != nil {
				fmt.Fprintf(a.log, "EXCLUDE genMethodsOf() offline for type: " + T.String() + "\n")
			}
		}
	}
	if a.config.DEBUG {
		fmt.Fprintf(a.log,"\n Done genMethodsOf() offline. \n")
	}
	// Generate constraints for functions as they become reachable
	// from the roots.  (No constraints are generated for functions
	// that are dead in this analysis scope.)  ---> bz: want to generate cgn called by interfaces here, so it can have kcfa not shared contour
	for len(a.genq) > 0 {
		cgn := a.genq[0]
		a.genq = a.genq[1:]
		a.genFunc(cgn)
	}

	// The runtime magically allocates os.Args; so should we.
	if !(ContainString(a.config.Exclusion, "os") && ContainString(a.config.Exclusion, "runtime")) {
		//bz: we are trying to skip this iff "runtime" and "os" are both in exclusions
		if os := a.prog.ImportedPackage("os"); os != nil {
			// In effect:  os.Args = new([1]string)[:]
			T := types.NewSlice(types.Typ[types.String])
			obj := a.addNodes(sliceToArray(T), "<command-line args>")
			a.endObject(obj, nil, "<command-line args>")
			a.addressOf(T, a.objectNode(nil, os.Var("Args")), obj)
		}
	}

	// Discard generation state, to avoid confusion after node renumbering.
	if a.config.K == 0 { //bz: when we are using kcfa or origin, we still need this when genInvoke on-the-fly
		a.panicNode = 0
		a.globalval = nil
	}
	a.localval = nil
	a.localobj = nil

	stop("Constraint generation")
}
