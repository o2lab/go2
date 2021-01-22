// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pointer

// This file defines the main datatypes and Analyze function of the pointer analysis.

import (
	"fmt"
	"go/token"
	"go/types"
	"io"
	"os"
	"reflect"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"

	"github.tamu.edu/April1989/go_tools/go/ssa"
	"github.tamu.edu/April1989/go_tools/go/types/typeutil"
)

const (
	// optimization options; enable all when committing
	optRenumber = false // enable renumbering optimization (makes logs hard to read)
	optHVN      = true // enable pointer equivalence via Hash-Value Numbering

	// debugging options; disable all when committing
	debugHVN           = false // enable assertions in HVN
	debugHVNVerbose    = false // enable extra HVN logging
	debugHVNCrossCheck = false // run solver with/without HVN and compare (caveats below)
	debugTimers        = false // show running time of each phase
)

// object.flags bitmask values.
const (
	otTagged   = 1 << iota // type-tagged object
	otIndirect             // type-tagged object with indirect payload
	otFunction             // function object
)

// An object represents a contiguous block of memory to which some
// (generalized) pointer may point.
//
// (Note: most variables called 'obj' are not *objects but nodeids
// such that a.nodes[obj].obj != nil.)
//
type object struct {
	// flags is a bitset of the node type (ot*) flags defined above.
	flags uint32

	// Number of following nodes belonging to the same "object"
	// allocation.  Zero for all other nodes.
	size uint32

	// data describes this object; it has one of these types:
	//
	// ssa.Value	for an object allocated by an SSA operation.
	// types.Type	for an rtype instance object or *rtype-tagged object.
	// string	for an instrinsic object, e.g. the array behind os.Args.
	// nil		for an object allocated by an instrinsic.
	//		(cgn provides the identity of the intrinsic.)
	data interface{}

	// The call-graph node (=context) in which this object was allocated.
	// May be nil for global objects: Global, Const, some Functions.
	cgn *cgnode //bz: -> make call-site sensitive here
}

// nodeid denotes a node.
// It is an index within analysis.nodes.
// We use small integers, not *node pointers, for many reasons:
// - they are smaller on 64-bit systems.
// - sets of them can be represented compactly in bitvectors or BDDs.
// - order matters; a field offset can be computed by simple addition.
type nodeid uint32

// A node is an equivalence class of memory locations.
// Nodes may be pointers, pointed-to locations, neither, or both.
//
// Nodes that are pointed-to locations ("labels") have an enclosing
// object (see analysis.enclosingObject).
//
type node struct {
	// If non-nil, this node is the start of an object
	// (addressable memory location).
	// The following obj.size nodes implicitly belong to the object;
	// they locate their object by scanning back.
	obj *object

	// The type of the field denoted by this node.  Non-aggregate,
	// unless this is an tagged.T node (i.e. the thing
	// pointed to by an interface) in which case typ is that type.
	typ types.Type

	// subelement indicates which directly embedded subelement of
	// an object of aggregate type (struct, tuple, array) this is.
	subelement *fieldInfo // e.g. ".a.b[*].c"

	// Solver state for the canonical node of this pointer-
	// equivalence class.  Each node is created with its own state
	// but they become shared after HVN.
	solve *solverState

	//bz: want context match for receiver/params/results between calls
	callsite []*callsite
}

// An analysis instance holds the state of a single pointer analysis problem.
type analysis struct {
	config      *Config                     // the client's control/observer interface
	prog        *ssa.Program                // the program being analyzed
	log         io.Writer                   // log stream; nil to disable
	panicNode   nodeid                      // sink for panic, source for recover
	nodes       []*node                     // indexed by nodeid --> bz: pointer/reference/var/func/cgn
	flattenMemo map[types.Type][]*fieldInfo // memoization of flatten()
	trackTypes  map[types.Type]bool         // memoization of shouldTrack()
	constraints []constraint                // set of constraints
	cgnodes     []*cgnode        // all cgnodes       --> bz: nodes in cg; will copy to callgraph.cg at the end
	genq        []*cgnode        // queue of functions to generate constraints for
	intrinsics  map[*ssa.Function]intrinsic // non-nil values are summaries for intrinsic fns
	globalval   map[ssa.Value]nodeid        // node for each global ssa.Value          ---> bz: localval/globalval: only used in valueNode() and setValueNode() for each function, will be nil.
	localval    map[ssa.Value]nodeid        // node for each local ssa.Value           ---> bz: BUT the key will be replaced if multiple ctx exist
	globalobj   map[ssa.Value]nodeid        // maps v to sole member of pts(v), if singleton      ---> bz: for makeclosure, fn is not enough
	localobj    map[ssa.Value]nodeid        // maps v to sole member of pts(v), if singleton      ---> bz: only used in objectNode()
	atFuncs     map[*ssa.Function]bool      // address-taken functions (for presolver)
	mapValues   []nodeid                    // values of makemap objects (indirect in HVN)
	work        nodeset                     // solver's worklist
	//result      *Result                     // results of the analysis
	track       track                       // pointerlike types whose aliasing we track
	deltaSpace  []int                       // working space for iterating over PTS deltas

	// Reflection & intrinsics:
	hasher              typeutil.Hasher // cache of type hashes
	reflectValueObj     types.Object    // type symbol for reflect.Value (if present)
	reflectValueCall    *ssa.Function   // (reflect.Value).Call
	reflectRtypeObj     types.Object    // *types.TypeName for reflect.rtype (if present)
	reflectRtypePtr     *types.Pointer  // *reflect.rtype
	reflectType         *types.Named    // reflect.Type
	rtypes              typeutil.Map    // nodeid of canonical *rtype-tagged object for type T
	reflectZeros        typeutil.Map    // nodeid of canonical T-tagged object for zero value
	runtimeSetFinalizer *ssa.Function   // runtime.SetFinalizer

	//bz: my record
	fn2cgnodeIdx        map[*ssa.Function][]int //bz: a map of fn with a set of its cgnodes represented by the indexes of cgnodes[]
	// NOW also used for static and invoke calls TODO: may be should use nodeid not int (idx) ?
	closures            map[*ssa.Function]*Ctx2nodeid //bz: solution for makeclosure
    result              *ResultWCtx                   //bz: our result, dump all
    closureWOGo         map[nodeid]nodeid                     //bz: solution@field actualCallerSite []*callsite of cgnode type

	considerReflect     bool //bz: whether we have reflect in exclusions
}

// enclosingObj returns the first node of the addressable memory
// object that encloses node id.  Panic ensues if that node does not
// belong to any object.
func (a *analysis) enclosingObj(id nodeid) nodeid {
	// Find previous node with obj != nil.
	for i := id; i >= 0; i-- {
		n := a.nodes[i]
		if obj := n.obj; obj != nil {
			if i+nodeid(obj.size) <= id {
				break // out of bounds
			}
			return i
		}
	}
	panic("node has no enclosing object") //bz: this panics when including global, so ... do not panic?
}

// labelFor returns the Label for node id.
// Panic ensues if that node is not addressable.
func (a *analysis) labelFor(id nodeid) *Label {
	return &Label{
		obj:        a.nodes[a.enclosingObj(id)].obj,
		subelement: a.nodes[id].subelement,
	}
}

func (a *analysis) warnf(pos token.Pos, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	if a.log != nil {
		fmt.Fprintf(a.log, "%s: warning: %s\n", a.prog.Fset.Position(pos), msg)
	}
	a.result.Warnings = append(a.result.Warnings, Warning{pos, msg})
}

// computeTrackBits sets a.track to the necessary 'track' bits for the pointer queries.
func (a *analysis) computeTrackBits() {
	if len(a.config.extendedQueries) != 0 {
		// TODO(dh): only track the types necessary for the query.
		a.track = trackAll //bz: we want this trackAll, but we do not set this
		return
	}
	var queryTypes []types.Type
	for v := range a.config.Queries {
		queryTypes = append(queryTypes, v.Type())
	}
	for v := range a.config.IndirectQueries {
		queryTypes = append(queryTypes, mustDeref(v.Type()))
	}
	for _, t := range queryTypes {
		switch t.Underlying().(type) {
		case *types.Chan:
			a.track |= trackChan
		case *types.Map:
			a.track |= trackMap
		case *types.Pointer:
			a.track |= trackPtr
		case *types.Slice:
			a.track |= trackSlice
		case *types.Interface:
			a.track = trackAll
			return
		}
		if rVObj := a.reflectValueObj; rVObj != nil && types.Identical(t, rVObj.Type()) {
			a.track = trackAll
			return
		}
	}
}


// Analyze runs the pointer analysis with the scope and options
// specified by config, and returns the (synthetic) root of the callgraph.
//
// Pointer analysis of a transitively closed well-typed program should
// always succeed.  An error can occur only due to an internal bug.
//
// bz: updated, works for context-sensitive but result does not include context-sensitive call graph
func Analyze(config *Config) (result *ResultWCtx, err error) { //Result
	if config.Mains == nil {
		return nil, fmt.Errorf("no main/test packages to analyze (check $GOROOT/$GOPATH)")
	}
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("internal error in pointer analysis: %v (please report this bug)", p)
			fmt.Fprintln(os.Stderr, "Internal panic in pointer analysis:")
			debug.PrintStack()
		}
	}()

	a := &analysis{
		config:      config,
		log:         config.Log,
		prog:        config.prog(),
		globalval:   make(map[ssa.Value]nodeid),
		globalobj:   make(map[ssa.Value]nodeid),
		flattenMemo: make(map[types.Type][]*fieldInfo),
		trackTypes:  make(map[types.Type]bool),
		atFuncs:     make(map[*ssa.Function]bool),
		hasher:      typeutil.MakeHasher(),
		intrinsics:  make(map[*ssa.Function]intrinsic),
		//result: &Result{
		//	Queries:         make(map[ssa.Value]Pointer),
		//	IndirectQueries: make(map[ssa.Value]Pointer),
		//},
		result: &ResultWCtx{
			Queries:         make(map[ssa.Value][]PointerWCtx),
			IndirectQueries: make(map[ssa.Value][]PointerWCtx),
		},
		deltaSpace: make([]int, 0, 100),
		//bz: i did not clear these after offline TODO: do I ?
		fn2cgnodeIdx: make(map[*ssa.Function][]int),
		closures:     make(map[*ssa.Function]*Ctx2nodeid),
	}

	if false {
		a.log = os.Stderr // for debugging crashes; extremely verbose
	}

	var mode string //which pta is running
	if a.config.Origin {
		mode = strconv.Itoa(a.config.K) + "-ORIGIN-SENSITIVE"
	} else if a.config.CallSiteSensitive {
		mode = strconv.Itoa(a.config.K) + "-CFA"
	} else {
		mode = "CONTEXT-INSENSITIVE"
	}

	if a.log != nil {
		fmt.Fprintln(a.log, "==== Starting analysis: " + mode)
	}
	fmt.Println(" *** MODE: " + mode + " *** ")

	// Pointer analysis requires a complete program for soundness.
	// Check to prevent accidental misconfiguration.
	for _, pkg := range a.prog.AllPackages() {
		// (This only checks that the package scope is complete,
		// not that func bodies exist, but it's a good signal.)
		if !pkg.Pkg.Complete() {
			return nil, fmt.Errorf(`pointer analysis requires a complete program yet package %q was incomplete`, pkg.Pkg.Path())
		}
	}

	if reflect := a.prog.ImportedPackage("reflect"); reflect != nil {
		rV := reflect.Pkg.Scope().Lookup("Value")
		a.reflectValueObj = rV
		a.reflectValueCall = a.prog.LookupMethod(rV.Type(), nil, "Call")
		a.reflectType = reflect.Pkg.Scope().Lookup("Type").Type().(*types.Named)
		a.reflectRtypeObj = reflect.Pkg.Scope().Lookup("rtype")
		a.reflectRtypePtr = types.NewPointer(a.reflectRtypeObj.Type())

		// Override flattening of reflect.Value, treating it like a basic type.
		tReflectValue := a.reflectValueObj.Type()
		a.flattenMemo[tReflectValue] = []*fieldInfo{{typ: tReflectValue}}

		// Override shouldTrack of reflect.Value and *reflect.rtype.
		// Always track pointers of these types.
		a.trackTypes[tReflectValue] = true
		a.trackTypes[a.reflectRtypePtr] = true

		a.rtypes.SetHasher(a.hasher)
		a.reflectZeros.SetHasher(a.hasher)
	}
	if runtime := a.prog.ImportedPackage("runtime"); runtime != nil {
		a.runtimeSetFinalizer = runtime.Func("SetFinalizer")
	}
	a.computeTrackBits() //bz: use when there is input queries before running this analysis; we do not need this for now?

	a.generate()   //bz: a preprocess for reflection/runtime/import libs
	a.showCounts() //bz: print out size ...

	if optRenumber {
		a.renumber()
	}

	N := len(a.nodes) // excludes solver-created nodes

	if optHVN { //bz: default true
		if debugHVNCrossCheck { //default : false
			// Cross-check: run the solver once without
			// optimization, once with, and compare the
			// solutions.
			savedConstraints := a.constraints

			a.solve()
			a.dumpSolution("A.pts", N)

			// Restore.
			a.constraints = savedConstraints
			for _, n := range a.nodes {
				n.solve = new(solverState)
			}
			a.nodes = a.nodes[:N]

			// rtypes is effectively part of the solver state.
			a.rtypes = typeutil.Map{}
			a.rtypes.SetHasher(a.hasher)
		}

		a.hvn()
	}

	if debugHVNCrossCheck {
		runtime.GC()
		runtime.GC()
	}

	if a.log != nil {
		fmt.Fprintln(a.log, "==== Starting solving and generating constraints Online ====")
	}

	a.solve() //bz: officially starts here

	// Compare solutions.
	if optHVN && debugHVNCrossCheck {
		a.dumpSolution("B.pts", N)

		if !diff("A.pts", "B.pts") {
			return nil, fmt.Errorf("internal error: optimization changed solution")
		}
	}

	// Create callgraph.Nodes in deterministic order.
	if cg := a.result.CallGraph; cg != nil {
		for _, caller := range a.cgnodes {
			cg.CreateNodeWCtx(caller) //bz: changed
		}
	}

	// Add dynamic edges to call graph.
	var space [100]int
	for _, caller := range a.cgnodes {
		for _, site := range caller.sites {
			for _, callee := range a.nodes[site.targets].solve.pts.AppendTo(space[:0]) {
				a.callEdge(caller, site, nodeid(callee))
			}
		}
	}

	return a.result, nil
}

// bz: lazy way
// AnalyzeWCtx runs the pointer analysis with the scope and options
// specified by config, and returns the (synthetic) root of the callgraph.
//
// Pointer analysis of a transitively closed well-typed program should
// always succeed.  An error can occur only due to an internal bug.
//
func AnalyzeWCtx(config *Config) (result *ResultWCtx, err error) { //Result
	if config.Mains == nil {
		return nil, fmt.Errorf("no main/test packages to analyze (check $GOROOT/$GOPATH)")
	}
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("internal error in pointer analysis: %v (please report this bug)", p)
			fmt.Fprintln(os.Stderr, "Internal panic in pointer analysis:")
			debug.PrintStack()
		}
	}()

	a := &analysis{
		config:      config,
		log:         config.Log,
		prog:        config.prog(),
		globalval:   make(map[ssa.Value]nodeid),
		globalobj:   make(map[ssa.Value]nodeid),
		flattenMemo: make(map[types.Type][]*fieldInfo),
		trackTypes:  make(map[types.Type]bool),
		atFuncs:     make(map[*ssa.Function]bool),
		hasher:      typeutil.MakeHasher(),
		intrinsics:  make(map[*ssa.Function]intrinsic),
		result: &ResultWCtx{
			Queries:         make(map[ssa.Value][]PointerWCtx),
			IndirectQueries: make(map[ssa.Value][]PointerWCtx),
			GlobalQueries:   make(map[ssa.Value][]PointerWCtx),
			ExtendedQueries: make(map[ssa.Value][]PointerWCtx),
			DEBUG:           config.DEBUG,
			DiscardQueries:  config.DiscardQueries,
		},
		deltaSpace: make([]int, 0, 100),
		//bz: i did not clear the following two after offline TODO: do I ?
		fn2cgnodeIdx: make(map[*ssa.Function][]int),
		closures:     make(map[*ssa.Function]*Ctx2nodeid),
		closureWOGo:  make(map[nodeid]nodeid),
	}

	if false {
		a.log = os.Stderr // for debugging crashes; extremely verbose
	}

	var mode string //which pta is running
	if a.config.Origin {
		mode = strconv.Itoa(a.config.K) + "-ORIGIN-SENSITIVE"
	} else if a.config.CallSiteSensitive {
		mode = strconv.Itoa(a.config.K) + "-CFA"
	} else {
		mode = "CONTEXT-INSENSITIVE"
	}

	UpdateDEBUG(a.config.DEBUG) //in pointer/callgraph

	//update analysis scope
	fmt.Println(" *** MODE: " + mode + " *** ")
	fmt.Println(" *** Analyze Scope ***************** ")
	if len(a.config.Scope) > 0 {
		for _, pkg := range a.config.Scope {
			fmt.Println(" - " + pkg)
		}
	}
	fmt.Println(" - command-line-arguments (default)")
	fmt.Println(" *********************************** ")
	if len(a.config.Mains) > 1 {
	    fmt.Println(" *** Multiple Mains **************** ")
	    for i, main := range a.config.Mains {
			fmt.Println(strconv.Itoa(i) + ": " + main.String())
		}
		fmt.Println(" *********************************** ")
	}
	if a.config.DiscardQueries {
		fmt.Println(" *** No Queries *** ")
	}else{
		fmt.Println(" *** Use Queries/IndirectQueries/ExtendedQueries *** ")
	}

	if a.log != nil {
		fmt.Fprintln(a.log, "==== Starting analysis and logging: ")
	}

	// Pointer analysis requires a complete program for soundness.
	// Check to prevent accidental misconfiguration.
	for _, pkg := range a.prog.AllPackages() {
		// (This only checks that the package scope is complete,
		// not that func bodies exist, but it's a good signal.)
		if !pkg.Pkg.Complete() {
			return nil, fmt.Errorf(`pointer analysis requires a complete program yet package %q was incomplete`, pkg.Pkg.Path())
		}
	}

	if !ContainString(a.config.Exclusions, "reflect") { //bz: only do if race checker considers
		a.considerReflect = true //update

		if reflect := a.prog.ImportedPackage("reflect"); reflect != nil {
			rV := reflect.Pkg.Scope().Lookup("Value")
			a.reflectValueObj = rV
			a.reflectValueCall = a.prog.LookupMethod(rV.Type(), nil, "Call")
			a.reflectType = reflect.Pkg.Scope().Lookup("Type").Type().(*types.Named)
			a.reflectRtypeObj = reflect.Pkg.Scope().Lookup("rtype")
			a.reflectRtypePtr = types.NewPointer(a.reflectRtypeObj.Type())

			// Override flattening of reflect.Value, treating it like a basic type.
			tReflectValue := a.reflectValueObj.Type()
			a.flattenMemo[tReflectValue] = []*fieldInfo{{typ: tReflectValue}}

			// Override shouldTrack of reflect.Value and *reflect.rtype.
			// Always track pointers of these types.
			a.trackTypes[tReflectValue] = true
			a.trackTypes[a.reflectRtypePtr] = true

			a.rtypes.SetHasher(a.hasher)
			a.reflectZeros.SetHasher(a.hasher)
		}
	}else{
		a.considerReflect = false //update -> do not consider 'reflect'
	}
	if !ContainString(a.config.Exclusions, "runtime") { //bz: only do if race checker considers
		if runtime := a.prog.ImportedPackage("runtime"); runtime != nil {
			a.runtimeSetFinalizer = runtime.Func("SetFinalizer")
		}
	}
	a.computeTrackBits() //bz: use when there is input queries before running this analysis; we do not need this for now?

	a.generate()   //bz: a preprocess for reflection/runtime/import libs
	a.showCounts() //bz: print out size ...

	if optRenumber { //bz: default true
		a.renumber()
	}

	N := len(a.nodes) // excludes solver-created nodes

	if optHVN { //bz: default true
		if debugHVNCrossCheck { //default : false
			// Cross-check: run the solver once without
			// optimization, once with, and compare the
			// solutions.
			savedConstraints := a.constraints

			a.solve()
			a.dumpSolution("A.pts", N)

			// Restore.
			a.constraints = savedConstraints
			for _, n := range a.nodes {
				n.solve = new(solverState)
			}
			a.nodes = a.nodes[:N]

			// rtypes is effectively part of the solver state.
			a.rtypes = typeutil.Map{}
			a.rtypes.SetHasher(a.hasher)
		}

		a.hvn() //default: do this hvn
	}

	if debugHVNCrossCheck {
		runtime.GC()
		runtime.GC()
	}

	a.solve() //bz: officially starts here

	// Compare solutions.
	if optHVN && debugHVNCrossCheck {
		a.dumpSolution("B.pts", N)

		if !diff("A.pts", "B.pts") {
			return nil, fmt.Errorf("internal error: optimization changed solution")
		}
	}

	// Create callgraph.Nodes in deterministic order.
	if cg := a.result.CallGraph; cg != nil {
		for _, caller := range a.cgnodes {
			cg.CreateNodeWCtx(caller) //bz: create if absent
		}
	}

	if a.log != nil { // log format
		fmt.Fprintf(a.log, "\n\n\nCall Graph -----> \n")
	}

	// Add dynamic edges to call graph.
	var space [100]int
	for _, caller := range a.cgnodes {
		for _, site := range caller.sites {
			for _, callee := range a.nodes[site.targets].solve.pts.AppendTo(space[:0]) {
				a.callEdge(caller, site, nodeid(callee))
			}
		}
	}

	//bz: update all callee actual ctx for a.closureWOGo
	a.updateActaulCallSites()

	//bz: just assign for the main method; not a good solution, will resolve later
	for _, cgn := range a.cgnodes {
		if cgn.fn == a.config.Mains[0].Func("main") {
			//this is the main methid in app
			a.result.main = cgn
		}
	}

	a.result.CallGraph.computeFn2CGNode() //bz: update Fn2CGNode for user API
	a.result.nodes = a.nodes //bz: just in case

	return a.result, nil
}

//bz: used by me
func ContainString(s []string, e string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}

//bz: used in race_checker
func ContainStringRelax(s []string, e string) bool {
	for _, a := range s {
		if strings.Contains(e, a) {
			return true
		}
	}
	return false
}

//bz: solution@field actualCallerSite []*callsite of cgnode type
//update the callee of nodes in a.closureWOGo
func (a *analysis) updateActaulCallSites() {
	cg := a.result.CallGraph
	var total nodeset
	waiting := a.closureWOGo
	for _, nid := range waiting {
		cgn := a.nodes[nid].obj.cgn
		total.Insert(cgn.idx)//record

		node := cg.GetNodeWCtx(cgn)
		for _, outEdge := range node.Out {
			target := outEdge.Callee.cgn
			if !total.Has(target.idx) {
				if a.log != nil {
					fmt.Fprintf(a.log, "* Update actualCallerSite for ----> \n   %s -> [%s] \n", target, cgn.contourkActualFull())
				}
				if a.config.DEBUG {
					fmt.Printf("* Update actualCallerSite for ----> \n   %s -> [%s] \n", target, cgn.contourkActualFull())
				}
				for _, actual := range cgn.actualCallerSite {
					target.actualCallerSite = append(target.actualCallerSite, actual) //update
				}
				waiting[target.obj] = target.obj //next round
			}
		}
	}
}

// callEdge is called for each edge in the callgraph.
// calleeid is the callee's object node (has otFunction flag).
func (a *analysis) callEdge(caller *cgnode, site *callsite, calleeid nodeid) {
	obj := a.nodes[calleeid].obj
	if obj.flags&otFunction == 0 {
		panic(fmt.Sprintf("callEdge %s -> n%d: not a function object", site, calleeid))
	}
	callee := obj.cgn

	//bz: solution@field actualCallerSite []*callsite of cgnode type
	if a.closureWOGo[calleeid] != 0 {
		if !a.equalContextFor(caller.callersite, callee.callersite) {
			if a.log != nil {
				fmt.Fprintf(a.log, "Update actualCallerSite for ----> \n   %s -> [%s] \n", callee, caller.contourkFull())
			}
			if a.config.DEBUG {
				fmt.Printf("Update actualCallerSite for ----> \n   %s -> [%s] \n", callee, caller.contourkFull())
			}
			callee.actualCallerSite = append(callee.actualCallerSite, caller.callersite) //update
		}
	}

	if cg := a.result.CallGraph; cg != nil {
		// TODO(adonovan): opt: I would expect duplicate edges
		// (to wrappers) to arise due to the elimination of
		// context information, but I haven't observed any.
		// Understand this better.
		AddEdge(cg.CreateNodeWCtx(caller), site.instr, cg.CreateNodeWCtx(callee)) //bz: changed
	}

	if a.log != nil {
		fmt.Fprintf(a.log, "\tcall edge %s -> %s\n", site, callee)
	}

	// Warn about calls to non-intrinsic external functions.
	// TODO(adonovan): de-dup these messages.
	if fn := callee.fn; fn.Blocks == nil && a.findIntrinsic(fn) == nil {
		a.warnf(site.pos(), "unsound call to unknown intrinsic: %s", fn)
		a.warnf(fn.Pos(), " (declared here)")
	}
}

// dumpSolution writes the PTS solution to the specified file.
//
// It only dumps the nodes that existed before solving.  The order in
// which solver-created nodes are created depends on pre-solver
// optimization, so we can't include them in the cross-check.
//
func (a *analysis) dumpSolution(filename string, N int) {
	f, err := os.Create(filename)
	if err != nil {
		panic(err)
	}
	for id, n := range a.nodes[:N] {
		if _, err := fmt.Fprintf(f, "pts(n%d) = {", id); err != nil {
			panic(err)
		}
		var sep string
		for _, l := range n.solve.pts.AppendTo(a.deltaSpace) {
			if l >= N {
				break
			}
			fmt.Fprintf(f, "%s%d", sep, l)
			sep = " "
		}
		fmt.Fprintf(f, "} : %s\n", n.typ)
	}
	if err := f.Close(); err != nil {
		panic(err)
	}
}

// showCounts logs the size of the constraint system.  A typical
// optimized distribution is 65% copy, 13% load, 11% addr, 5%
// offsetAddr, 4% store, 2% others.
//
func (a *analysis) showCounts() {
	if a.log != nil {
		counts := make(map[reflect.Type]int)
		for _, c := range a.constraints {
			counts[reflect.TypeOf(c)]++
		}
		fmt.Fprintf(a.log, "# constraints:\t%d\n", len(a.constraints))
		var lines []string
		for t, n := range counts {
			line := fmt.Sprintf("%7d  (%2d%%)\t%s", n, 100*n/len(a.constraints), t)
			lines = append(lines, line)
		}
		sort.Sort(sort.Reverse(sort.StringSlice(lines)))
		for _, line := range lines {
			fmt.Fprintf(a.log, "\t%s\n", line)
		}

		fmt.Fprintf(a.log, "# nodes:\t%d\n", len(a.nodes))

		// Show number of pointer equivalence classes.
		m := make(map[*solverState]bool)
		for _, n := range a.nodes {
			m[n.solve] = true
		}
		fmt.Fprintf(a.log, "# ptsets:\t%d\n", len(m))
	}
}
