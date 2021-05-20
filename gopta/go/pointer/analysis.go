// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pointer

// This file defines the main datatypes and Analyze function of the pointer analysis.

import (
	"fmt"
	"github.com/april1989/origin-go-tools/go/myutil/flags"
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
	"time"

	"github.com/april1989/origin-go-tools/go/ssa"
	"github.com/april1989/origin-go-tools/go/types/typeutil"
)

const (
	// debugging options; disable all when committing
	debugHVN           = false // enable assertions in HVN
	debugHVNVerbose    = false // enable extra HVN logging
	debugHVNCrossCheck = false // run solver with/without HVN and compare (caveats below)
	debugTimers        = false // show running time of each phase

	//bz: for callback: for recursive calls between lib call and callback fn (with goroutines spawned),
	// how many times of duplicate goroutines from different contexts do we consider?
	numGoCallback = 1
)

// object.flags bitmask values.
const (
	otTagged   = 1 << iota // type-tagged object
	otIndirect             // type-tagged object with indirect payload
	otFunction             // function object
)

//bz: a set of my config
var (
	// optimization options; enable all when committing
	// bz: THIS IS ORIGINALLY DECLARED IN CONST ABOVE
	//  only turn on these two opt when flags.DoCallback == true, since its not on-the-fly but presolve
	optRenumber = false // enable renumbering optimization (makes logs hard to read)
	optHVN      = false // enable pointer equivalence via Hash-Value Numbering

	//bz: for my performance
	maxTime time.Duration
	minTime time.Duration
	total   int64

	//bz: for user uses
	main2Result     map[*ssa.Package]*Result     //bz: return value of AnalyzeMultiMains(), skip redo everytime calls Analyze()
	main2ResultWCtx map[*ssa.Package]*ResultWCtx //bz: return value of AnalyzeMultiMains(), skip redo everytime calls Analyze()

	//bz: for my use: coverage
	allFns          map[string]string //bz: when DoCoverage = true: store all funcs within the scope/app, use map instead of array for an easier existence check
	cCmplFns        map[string]string //bz: c/c++ compiled functions, e.g., functions in xxx.pb.go, some of these functions will nolonger be used/invoked in the app
	showUnCoveredFn = false           //bz: whether print out those functions that we did not analyze
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
	cgnodes     []*cgnode                   // all cgnodes       --> bz: nodes in cg; will copy to callgraph.cg at the end
	genq        []*cgnode                   // queue of functions to generate constraints for
	intrinsics  map[*ssa.Function]intrinsic // non-nil values are summaries for intrinsic fns
	globalval   map[ssa.Value]nodeid        // node for each global ssa.Value          ---> bz: localval/globalval: only used in valueNode() and setValueNode() for each function, will be nil.
	localval    map[ssa.Value]nodeid        // node for each local ssa.Value           ---> bz: BUT the key will be replaced if multiple ctx exist
	globalobj   map[ssa.Value]nodeid        // maps v to sole member of pts(v), if singleton      ---> bz: for makeclosure, fn is not enough
	localobj    map[ssa.Value]nodeid        // maps v to sole member of pts(v), if singleton      ---> bz: only used in objectNode()
	atFuncs     map[*ssa.Function]bool      // address-taken functions (for presolver)
	mapValues   []nodeid                    // values of makemap objects (indirect in HVN)
	work        nodeset                     // solver's worklist
	//result      *Result                     // results of the analysis: default
	track      track // pointerlike types whose aliasing we track
	deltaSpace []int // working space for iterating over PTS deltas

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
	fn2cgnodeIdx map[*ssa.Function][]int //bz: a map of fn with a set of its cgnodes represented by the indexes of cgnodes[] -> when using contexts
	//TODO: may be should use nodeid not int (idx) ?
	closures      map[*ssa.Function]*Ctx2nodeid //bz: solution for makeclosure
	result        *ResultWCtx                   //bz: our result, dump all
	closureWOGo   map[nodeid]nodeid             //bz: solution@field actualCallerSite []*callsite of cgnode type
	isWithinScope bool                          //bz: whether the current genInstr() is working on a method within our scope
	online        bool                          //bz: whether a constraint is from genInvokeOnline() -> used for on-the-fly only, no callback

	//bz: performance-related data
	num_constraints int             //bz: performance
	numObjs         int             //bz: number of objects allocated
	numOrigins      int             //bz: number of origins
	preGens         []*ssa.Function //bz: number of pregenerated functions/cgs/constraints for reflection, os, runtime
	recordPreGen    bool            //bz: when to record preGens

	//bz: callback-related
	globalcb   map[string]*ssa.Function          //bz: a map of synthetic fakeFn and its fn -> cannot use map of newFunction directly ...
	callbacks  map[*ssa.Function]*Ctx2nodeid     //bz: fakeFn invoked by different context/call sites
	gencb      []*cgnode                         //bz: queue of functions to generate constraints from genCallBack, we solve these at the end
	cb2Callers map[*ssa.Function]*callbackRecord //bz: record the relations among: callback fn, caller lib fn and its context to avoid recursive calls

	//bz: preSolve-related
	curIter int //bz: for debug, the ith iteration of the loop in preSolve() TODO: maybe move to analysis as a field

	//bz: test-related
	isMain bool            //whether this analysis obj is allocated for a main? otherwise, for a test
	tests  []*ssa.Function //all test fns that following the go test name rules
	/** bz:
	  we do have panics when turn on hvn optimization. panics are due to that hvn wrongly computes sccs.
	  wrong sccs is because some pointers are not marked as indirect (but marked in default).
	  This not-marked behavior is because we do not create function pointers for those functions that
	  we skip their cgnode/func/constraints creation in offline generate(). So we keep a record here.

	  we ONLY record this skipTypes when optHVN is on and mark indirect in genStaticCall()
	*/
	skipTypes map[string]string //bz: a record of skipped methods in generate() off-line
}

//bz: for callback use only
func (a *analysis) GetMySyntheticFn(base *ssa.Function) *ssa.Function {
	for n, fn := range a.globalcb {
		if strings.HasPrefix(n, base.String()) {
			return fn
		}
	}
	return nil
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
		a.track = trackAll //bz: we want this trackAll, but we do not set this  --> update: set it
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

var main2Analysis map[*ssa.Package]*Result //bz: skip redo everytime calls Analyze()

//bz: expose to my test.go only
func GetMain2ResultWCtx() map[*ssa.Package]*ResultWCtx {
	return main2ResultWCtx
}

//bz: fill in the result
func translateQueries(val ssa.Value, id nodeid, cgn *cgnode, result *Result, _result *ResultWCtx) {
	if cgn == nil && !flags.DoCompare { //global var
		//bz: default algo only has Queries and IndirectQueries in its result, if we want to do
		// comparison, we need to record these GlobalQueries to Queries/IndirectQueries
		//Update: one val can map to multiple pts,
		// e.g., n618499 and n63251 in /google.golang.org/grpc/test.test@commit5bc9b325fa575e8938292b45c29292401de9bb8a
		// n63251 is a global object, and n618499 is a pointer with the same type of n63251, they share the same key v in GlobalQueries
		// we do not want the object, its useless (its pts is empty and confuse the query)
		// tmp solution -> check the type of id, if is an heap alloc, skip its record;
		//     others like function pointer/constant can also be *ssa.Global, and we record them
		_, ok1 := val.(*ssa.FreeVar)
		_, ok2 := val.(*ssa.Global)
		if ok1 || ok2 {
			obj := _result.a.nodes[id].obj
			if obj != nil {
				if _, ok := obj.data.(*ssa.Global); ok {
					return //this is an heap alloc, an object, pts is meaning less
				}
			}

			ptr := PointerWCtx{_result.a, id, nil}
			ptrs, ok := result.GlobalQueries[val]
			if !ok {
				// First time?  Create the canonical query node.
				ptrs = make([]PointerWCtx, 1)
				ptrs[0] = ptr
			} else {
				ptrs = append(ptrs, ptr)
			}
			result.GlobalQueries[val] = ptrs
			return
		}
	}

	t := val.Type()
	if CanPoint(t) {
		ptr := PointerWCtx{_result.a, id, cgn}
		ptrs, ok := result.Queries[val]
		if !ok {
			// First time?  Create the canonical query node.
			ptrs = make([]PointerWCtx, 1)
			ptrs[0] = ptr
		} else {
			ptrs = append(ptrs, ptr)
		}
		result.Queries[val] = ptrs
	} else { //indirect
		ptr := PointerWCtx{_result.a, id, cgn}
		ptrs, ok := result.IndirectQueries[val]
		if !ok {
			// First time?  Create the canonical query node.
			ptrs = make([]PointerWCtx, 1)
			ptrs[0] = ptr
		} else {
			ptrs = append(ptrs, ptr)
		}
		result.IndirectQueries[val] = ptrs
	}
}

//bz: print out config in console
func printConfig(config *Config) {
	var mode string //which pta is running
	if config.Origin {
		mode = strconv.Itoa(config.K) + "-ORIGIN-SENSITIVE"
	} else if config.CallSiteSensitive {
		mode = strconv.Itoa(config.K) + "-CFA"
	} else {
		mode = "CONTEXT-INSENSITIVE"
	}
	fmt.Println(" *** MODE: " + mode + " *** ")
	fmt.Println(" *** Level: " + strconv.Itoa(config.Level) + " *** ")
	//bz: change to default, remove flags
	fmt.Println(" *** Use Queries/IndirectQueries *** ")
	fmt.Println(" *** Use Default Queries API *** ")
	if config.TrackMore {
		fmt.Println(" *** Track All Types *** ")
	} else {
		fmt.Println(" *** Default Type Tracking (skip basic types) *** ")
	}
	if config.DoCallback {
		fmt.Println(" *** Do Callback & Synthetic Lib Fn *** ")
	} else {
		fmt.Println(" *** No Callback *** ")
	}
	if flags.DoCallback { //bz: see comments of optHVN
		optHVN = true
		optRenumber = true
	} else { //turn it off for on-the-fly
		optHVN = false
		optRenumber = false
	}

	if flags.DoPerformance { //bz: this is from my main, i want them to print out
		if optRenumber {
			fmt.Println(" *** optRenumber ON *** ")
		} else {
			fmt.Println(" *** optRenumber OFF *** ")
		}
		if optHVN {
			fmt.Println(" *** optHVN ON *** ")
		} else {
			fmt.Println(" *** optHVN OFF *** ")
		}
	}

	fmt.Println(" *** Analyze Scope ***************** ")
	if len(config.Scope) > 0 {
		for _, pkg := range config.Scope {
			fmt.Println(" - " + pkg)
		}
	}
	fmt.Println(" *********************************** ")
	if len(config.imports) == 0 {
		fmt.Println(" *** No Import Libs **************** ")
	} else {
		fmt.Println(" *** Import Libs ******************* ")
		if len(config.imports) > 0 {
			for _, pkg := range config.imports {
				fmt.Print(pkg + ", ")
			}
			fmt.Println()
		}
		fmt.Println(" *********************************** ")
	}
	if len(config.Exclusion) == 0 {
		fmt.Println(" *** No Excluded Pkgs **************** ")
	} else {
		fmt.Println(" *** Excluded Pkgs ******************* ")
		if len(config.Exclusion) > 0 {
			for _, pkg := range config.Exclusion {
				fmt.Print(pkg + ", ")
			}
			fmt.Println()
		}
		fmt.Println(" *********************************** ")
	}
}

//bz: user api, to analyze multiple mains sequentially
func AnalyzeMultiMains(config *Config) (results map[*ssa.Package]*Result, err error) {
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

	//if len(config.Mains) == 1 {
	//	panic("This API is for analyzing MULTIPLE mains. If analyzing one main, please use pointer.Analyze().")
	//}

	maxTime = 0
	minTime = 1000000000

	if flags.DoPrintInfo {
		printConfig(config)

		if flags.DoTests {
			fmt.Println(" *** Multiple Mains/Tests ********** ")
		} else {
			fmt.Println(" *** Multiple Mains **************** ")
		}
	}

	for i, main := range config.Mains { //analyze mains
		//create a config
		var _mains []*ssa.Package
		_mains = append(_mains, main)

		//bz: !! turn on reflection if includes tests requires base objs, e.g., grpc/internal/cache/TestCacheExpire
		doReflect := config.Reflection
		isMain := true
		if flags.DoTests && strings.HasSuffix(main.Pkg.Path(), ".test") || main.IsMainTest {
			doReflect = true
			isMain = false
		}

		_config := &Config{
			Mains:          _mains,
			Reflection:     doReflect,
			BuildCallGraph: config.BuildCallGraph,
			Log:            config.Log,
			//CallSiteSensitive: true, //kcfa
			Origin: config.Origin, //origin
			//shared config
			K:          config.K,
			LimitScope: config.LimitScope, //bz: only consider app methods now -> no import will be considered
			DEBUG:      config.DEBUG,      //bz: rm all printed out info in console
			Scope:      config.Scope,      //bz: analyze scope + input path
			Exclusion:  config.Exclusion,  //bz: copied from race_checker if any
			TrackMore:  config.TrackMore,  //bz: track pointers with all types
			DoCallback: config.DoCallback, //bz: do callback
			Level:      config.Level,      //bz: see pointer.Config
		}

		if flags.DoPerformance {
			fmt.Println("\n\n", i, ": "+main.String(), " ... ")
		}

		//we initially run the analysis
		start := time.Now()
		_result, err := AnalyzeWCtx(_config, isMain)
		if err != nil {
			return nil, err
		}

		translateResult(_result, main)
		elapse := time.Now().Sub(start)
		if maxTime < elapse {
			maxTime = elapse
		}
		if minTime > elapse {
			minTime = elapse
		}
		total = total + elapse.Milliseconds()

		//performance
		if doReflect { //default off
			fmt.Println(i, ": "+main.String(), " (use "+elapse.String()+") *** Reflection ON ***")
		} else {
			fmt.Println(i, ": "+main.String(), " (use "+elapse.String()+")")
		}

		if flags.PTSLimit != 0 && flags.DoDiff {
			//bz: i want to see the not covered functions when turn on/off ptsLimit,
			// so do one time analysis with ptsLimit off and compare; turn this off by setting flags.DoDiff = false
			ptsLimit := flags.PTSLimit //store original setting
			flags.PTSLimit = 0         //turn off

			//run analysis again
			fmt.Println("\n\n", i, ": "+main.String(), " (No PTSLimit) ... ")
			start := time.Now()
			fullResult, err := AnalyzeWCtx(_config, isMain)
			if err != nil {
				return nil, err
			}
			elapse := time.Now().Sub(start)

			if doReflect { //default off
				fmt.Println(i, ": "+main.String(), " (No PTSLimit, use "+elapse.String()+") *** Reflection ON ***")
			} else {
				fmt.Println(i, ": "+main.String(), " (No PTSLimit, use "+elapse.String()+")")
			}

			//compare diff
			fmt.Println("\nThe uncovered functions when turning on PTSLimit: ")
			s := 0
			in := 0
			limitCFn2CGNode := _result.CallGraph.Fn2CGNode
			a := _result.a
			for ffn, _ := range fullResult.CallGraph.Fn2CGNode {
				if _, ok := limitCFn2CGNode[ffn]; !ok {
					//not exist when turn on PTSLimit
					fmt.Println(ffn.String())
					s++
					if a.withinScope(ffn.String()) {
						in++
					}
				}
			}
			fmt.Println("Finish. #Total: ", s, " (#WithinScope: ", in, ")")

			//done comparison
			flags.PTSLimit = ptsLimit //set it back
		}
	}
	fmt.Println(" *********************************** ")

	//bz: i want this...
	fmt.Println("Total: ", (time.Duration(total)*time.Millisecond).String()+".")
	fmt.Println("Max: ", maxTime.String()+".")
	fmt.Println("Min: ", minTime.String()+".")
	fmt.Println("Avg: ", float32(total)/float32(len(config.Mains))/float32(1000), "s.")

	if flags.DoCoverage { //bz: total coverage
		computeTotalCoverage()
	}

	results = main2Result
	return results, nil
}

//bz: change to default api; only analyze mains here
//but result does not include call graph (a.result.CallGraph),
//since the type does not match and race checker also does not use this
func Analyze(config *Config) (result *Result, err error) {
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

	main := config.Mains[0] //bz: currently only handle one main
	if result, ok := main2Result[main]; ok {
		//we already done the analysis, now find and wrap the result
		return result, nil
	}

	//setting
	if flags.DoCallback { //bz: see comments of optHVN
		optHVN = true
		optRenumber = true
	}

	//bz: !! turn on reflection if includes tests requires base objs, e.g., grpc/internal/cache/TestCacheExpire
	isMain := true
	if flags.DoTests && strings.HasSuffix(main.Pkg.Path(), ".test") || main.IsMainTest {
		config.Reflection = true
		isMain = false
	}

	//we initially run the analysis
	_result, err := AnalyzeWCtx(config, isMain)
	if err != nil {
		return nil, err
	}

	if main.IsMainTest && len(_result.a.tests) == 0 {
		//TODO: bz: main.IsMainTest == true but we cannot find/link the test func now
		//  reset to main mode, not test mode
		_result.a.isMain = true
	}

	result = translateResult(_result, main)
	return result, nil
}

// bz: AnalyzeWCtx runs the pointer analysis with the scope and options
// specified by config, and returns the (synthetic) root of the callgraph.
//
// Pointer analysis of a transitively closed well-typed program should
// always succeed.  An error can occur only due to an internal bug.
//
func AnalyzeWCtx(config *Config, isMain bool) (result *ResultWCtx, err error) { //Result
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
		},
		deltaSpace: make([]int, 0, 100),
		//bz: mine
		fn2cgnodeIdx: make(map[*ssa.Function][]int),
		closures:     make(map[*ssa.Function]*Ctx2nodeid),
		closureWOGo:  make(map[nodeid]nodeid),
		skipTypes:    make(map[string]string),
		callbacks:    make(map[*ssa.Function]*Ctx2nodeid),
		globalcb:     make(map[string]*ssa.Function),
		cb2Callers:   make(map[*ssa.Function]*callbackRecord),
		curIter:      0,
		isMain:       isMain,
	}

	if false {
		a.log = os.Stderr // for debugging crashes; extremely verbose
	}

	//if len(a.config.Mains) > 1 {
	//	panic("This API is for analyzing ONE main. If analyzing multiple mains, please use pointer.AnalyzeMultiMains().")
	//}

	UpdateDEBUG(a.config.DEBUG) //in pointer/callgraph, print out info changes

	//update analysis import
	imports := a.config.Mains[0].Pkg.Imports()
	if len(imports) > 0 {
		for _, _import := range imports {
			a.config.imports = append(a.config.imports, _import.Name())
		}
	}

	if flags.DoPrintInfo {
		printConfig(a.config)
	}

	if flags.DoCoverage && allFns == nil {
		a.collectFnsWScope()
		//for fn, _ := range allFns { //bz: debug
		//	fmt.Println(fn)
		//}
		//return nil, fmt.Errorf("done")
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

	//a.computeTrackBits() //bz: use when there is input queries before running this analysis; -> update: we do not need this. just set a.track to trackAll below
	a.track = trackAll

	a.generate()   //bz: a preprocess for reflection/runtime/import libs
	a.showCounts() //bz: print out size ...

	if optRenumber { //bz: default true
		fmt.Println("Renumbering ...")
		start := time.Now() //bz: i add performance
		a.renumber()
		elapsed := time.Now().Sub(start)
		fmt.Println("Renumber using ", elapsed)
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

		fmt.Println("HVNing ...")
		start := time.Now() //bz: i add performance
		a.hvn()             //default: do this hvn
		elapsed := time.Now().Sub(start)
		fmt.Println("HVN using ", elapsed) //bz: i want to know how slow it is ...
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
	a.updateActualCallSites()

	//bz: just assign for the main method; not a good solution, will resolve later
	//  do the same for tests, however, all test functions (under the same test pkg and same main) in to one root.
	for _, cgn := range a.cgnodes {
		if cgn.fn == a.config.Mains[0].Func("main") {
			//this is the main methid in app
			a.result.main = cgn
		}
	}

	if a.log != nil { // dump call graph
		fmt.Fprintf(a.log, "\n\n\nCall Graph -----> \n")
		printed := make(map[int]int)
		cg := a.result.CallGraph
		list := make([]*Node, 1)
		list[0] = cg.Root
		for len(list) > 0 {
			node := list[0]
			list = list[1:]
			for _, out := range node.Out {
				fmt.Fprintf(a.log, "\t%s (from %s) \n\t\t-> %s\n", out.Site, node.cgn.String(), out.Callee.String())
				if printed[out.Callee.ID] == 0 { // not printed before
					list = append(list, out.Callee)
					printed[out.Callee.ID] = out.Callee.ID
				}
			}
		}
	}

	a.result.CallGraph.computeFn2CGNode() //bz: update Fn2CGNode for user API
	a.result.a = a                        //bz: update

	if flags.DoPerformance { //bz: performance test; dump info
		fmt.Println("--------------------- Performance ------------------------")
		fmt.Println("#Pre-generated cgnodes: ", len(a.preGens))
		fmt.Println("#pts: ", len(a.nodes)) //this includes all kinds of pointers, e.g., cgnode, func, pointer
		fmt.Println("#constraints (totol num): ", a.num_constraints)
		fmt.Println("#cgnodes (totol num): ", len(a.cgnodes))
		fmt.Println("#nodes (totol num): ", len(a.nodes))
		//fmt.Println("#func (totol num): ", len(a.fn2cgnodeIdx))
		//numTyp := 0
		//for _, track := range a.trackTypes {
		//	if track {
		//		numTyp++
		//	}
		//}
		//fmt.Println("#tracked types (totol num): ", numTyp)
		fmt.Println("#fns (totol num): ", len(a.result.CallGraph.Fn2CGNode))
		fmt.Println("#tracked types (totol num): trackAll")   //bz: updated a.track = trackAll, skip this number
		fmt.Println("#origins (totol num): ", a.numOrigins+1) //bz: main is not included here
		fmt.Println("#objs (totol num): ", a.numObjs)
		fmt.Println("\nCall Graph: (cgnode based: function + context) \n#Nodes: ", len(a.result.CallGraph.Nodes))
		fmt.Println("#Edges: ", a.result.CallGraph.GetNumEdges())

		if a.config.DoCallback {
			fmt.Println("\nCallback: \n#Synthetic Fn: ", len(a.callbacks))
			fmt.Println("#Callback Fn: ", len(a.cb2Callers))
		}

		if flags.DoCoverage {
			a.computeCoverage()
		}

		//fmt.Println("\n\n=================================== #", len(a.result.CallGraph.Fn2CGNode)) //bz: debug
		//for fn, _ := range a.result.CallGraph.Fn2CGNode {
		//	s := fn.String()
		//	if strings.HasPrefix(s, "(*reflect.rtype)") {
		//		continue
		//	}
		//	fmt.Println(s)
		//}
	}

	return a.result, nil
}

//bz: translate to default return value, and update main2Result
func translateResult(_result *ResultWCtx, main *ssa.Package) *Result {
	result := &Result{
		Queries:         make(map[ssa.Value][]PointerWCtx),
		IndirectQueries: make(map[ssa.Value][]PointerWCtx),
		GlobalQueries:   make(map[ssa.Value][]PointerWCtx),
	}

	//go through each cgnode
	callgraph := _result.CallGraph
	fns := callgraph.Fn2CGNode
	for _, cgns := range fns {
		for _, cgn := range cgns {
			for val, id := range cgn.localval {
				if id == 0 {
					continue
				}
				translateQueries(val, id, cgn, result, _result)
			}

			for obj, id := range cgn.localobj {
				if id == 0 {
					continue
				}
				translateQueries(obj, id, cgn, result, _result)
			}
		}
	}
	for val, id := range _result.a.globalval {
		if id == 0 {
			continue
		}
		translateQueries(val, id, nil, result, _result)
	}
	for obj, id := range _result.a.globalobj {
		if id == 0 {
			continue
		}
		translateQueries(obj, id, nil, result, _result)
	}

	//upate
	if main2Result == nil {
		main2Result = make(map[*ssa.Package]*Result)
	}
	main2Result[main] = result
	result.a = _result.a

	//udpate: test only
	if main2ResultWCtx == nil {
		main2ResultWCtx = make(map[*ssa.Package]*ResultWCtx)
	}
	main2ResultWCtx[main] = _result

	//also udpate _result for new api
	_result.Queries = result.Queries
	_result.IndirectQueries = result.IndirectQueries
	_result.GlobalQueries = result.GlobalQueries

	return result
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
func (a *analysis) updateActualCallSites() {
	if a.log != nil {
		fmt.Fprintf(a.log, "\n\n")
	}
	cg := a.result.CallGraph
	var total nodeset
	waiting := a.closureWOGo
	for len(waiting) > 0 {
		next := make(map[nodeid]nodeid) //next iteration
		for _, nid := range waiting {
			cgn := a.nodes[nid].obj.cgn
			total.Insert(cgn.idx) //record

			node := cg.GetNodeWCtx(cgn)
			for _, outEdge := range node.Out {
				target := outEdge.Callee.cgn
				if !total.Has(target.idx) {
					//update
					if cgn.actualCallerSite != nil {
						if a.log != nil {
							fmt.Fprintf(a.log, "* Update actualCallerSitefor ----> \n%s -> [%s] \n", target, cgn.contourkActualFull())
						}
						if a.config.DEBUG {
							fmt.Printf("* Update actualCallerSite for ----> \n%s -> [%s] \n", target, cgn.contourkActualFull())
						}
						for _, actual := range cgn.actualCallerSite {
							target.updateActualCallerSite(actual)
						}
						next[target.obj] = target.obj //next round
					}
				}
			}
		}
		waiting = next
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
		//bz: only if caller is from app
		if !equalCallSite(caller.callersite, callee.callersite) {
			if a.log != nil {
				fmt.Fprintf(a.log, "Update actualCallerSite for ----> \n%s -> [%s] \n", callee, caller.contourkFull())
			}
			if a.config.DEBUG {
				fmt.Printf("Update actualCallerSite for ----> \n%s -> [%s] \n", callee, caller.contourkFull())
			}
			callee.updateActualCallerSite(caller.callersite) //update
		}
	}

	if cg := a.result.CallGraph; cg != nil {
		// TODO(adonovan): opt: I would expect duplicate edges
		// (to wrappers) to arise due to the elimination of
		// context information, but I haven't observed any.
		// Understand this better.
		cg.AddEdge(cg.CreateNodeWCtx(caller), site.instr, cg.CreateNodeWCtx(callee)) //bz: changed
	}

	if a.log != nil {
		fmt.Fprintf(a.log, "\t%s -> %s\n", site, callee)
	}

	// Warn about calls to non-intrinsic external functions.
	// TODO(adonovan): de-dup these messages.
	if fn := callee.fn; fn.Blocks == nil && a.findIntrinsic(fn) == nil && !fn.IsMySynthetic { //bz: we create synthetic funcs (cause this warning), skip this warn.
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
		fmt.Fprintf(a.log, "#constraints:\t%d\n", len(a.constraints))
		var lines []string
		for t, n := range counts {
			line := fmt.Sprintf("%7d  (%2d%%)\t%s", n, 100*n/len(a.constraints), t)
			lines = append(lines, line)
		}
		sort.Sort(sort.Reverse(sort.StringSlice(lines)))
		for _, line := range lines {
			fmt.Fprintf(a.log, "\t%s\n", line)
		}

		fmt.Fprintf(a.log, "#nodes:\t%d\n", len(a.nodes))

		// Show number of pointer equivalence classes.
		m := make(map[*solverState]bool)
		for _, n := range a.nodes {
			m[n.solve] = true
		}
		fmt.Fprintf(a.log, "#ptsets:\t%d\n", len(m))
	}

	if flags.DoCallback && optHVN { //bz: add showcount to console
		counts := make(map[reflect.Type]int)
		for _, c := range a.constraints {
			counts[reflect.TypeOf(c)]++
		}
		fmt.Println("#constraints:\t", len(a.constraints))

		var lines []string
		for t, n := range counts {
			line := fmt.Sprintf("%7d  (%2d%%)\t%s", n, 100*n/len(a.constraints), t)
			lines = append(lines, line)
		}
		sort.Sort(sort.Reverse(sort.StringSlice(lines)))
		for _, line := range lines {
			fmt.Println("\t", line)
		}

		fmt.Println("#nodes:\t", len(a.nodes))

		// Show number of pointer equivalence classes.
		m := make(map[*solverState]bool)
		for _, n := range a.nodes {
			m[n.solve] = true
		}
		fmt.Println("#ptsets:\t", len(m))
	}
}

//bz: when DoCoverage = true: collect all functions in the scope, stored in allFns
func (a *analysis) collectFnsWScope() {
	allFns = make(map[string]string)
	cCmplFns = make(map[string]string)

	tmp := make(map[*ssa.Function]*ssa.Function)
	for _, T := range a.prog.RuntimeTypes() {
		_type := T.String()
		if a.withinScope(_type) {
			if isInterface(T) {
				continue //interface has no concrete function, skip
			}

			//bz: a.prog.MethodSets.MethodSet(T): this is over-over-over-approximate ... some unimplemented functions can appear here, e.g.,
			// (google.golang.org/grpc/credentials/google.testCreds).OverrideServerName -> struct @ credentials/google/google_test.go,
			// but interface function declared @ credentials/credentials.go
			// however, such function does not exist ... want to replace by switch, but what if we miss functions??
			mset := a.prog.MethodSets.MethodSet(T)
			for i, n := 0, mset.Len(); i < n; i++ {
				m := a.prog.MethodValue(mset.At(i))
				tmp[m] = m
			}

			//switch typ := T.(type) {
			//case *types.Named:
			//	for i:= 0; i< typ.NumMethods(); i++ {
			//		m := typ.Method(i)
			//		fn := a.prog.declaredFunc(m) //bz: this is the declared function in programs
			//		tmp[fn] = fn
			//	}
			//
			//case *types.Pointer:
			//
			//case *types.Struct:
			//
			//default:
			//}
		}
	}
	for _, pkg := range a.prog.AllPackages() {
		for _, mem := range pkg.Members {
			if fn, ok := mem.(*ssa.Function); ok {
				if a.withinScope(fn.Pkg.String()) {
					tmp[fn] = fn
				}
			}
		}
	}

	for _, m := range tmp {
		s := m.String()

		//bz: identify fns in xxx.pb.go
		pos := m.Pos()
		if pos.IsValid() {
			filename := a.prog.Fset.Position(pos).Filename
			if strings.HasSuffix(filename, ".pb.go") {
				cCmplFns[s] = s
			}
		}

		allFns[s] = s
	}
}

//bz: when DoCoverage = true: compute (#analyzed fn/#total fn) in a program for this main
func (a *analysis) computeCoverage() {
	covered := make(map[string]string)
	closure := make(map[string]string)
	other := make(map[string]string) //others can be lib, reflect, <root>

	for fn, _ := range a.result.CallGraph.Fn2CGNode {
		s := fn.String()
		if _, ok := allFns[s]; ok {
			covered[s] = s
		} else {
			subs := strings.Split(s, "$")
			if len(subs) == 1 {
				other[s] = s
			} else {
				sub := subs[0]
				if _, ok := allFns[sub]; ok {
					closure[s] = s
				}
			}
		}

		//bz: we have a mismatch here, e.g., in priority test from grpc:
		// (*google.golang.org/grpc/xds/internal/balancer/priority.s).Teardown in allFns
		//  -> (google.golang.org/grpc/xds/internal/balancer/priority.s).Teardown in our cg
		// or vice versa. Actaully, these two should have no diff when we handle their constraints ??
		// SOLUTION -> convert the mismatch manually
		if s[0:2] == "(*" { //this is a pointer type
			s = "(" + s[2:] //remove pointer
			if _, ok := allFns[s]; ok {
				covered[s] = s
			}
		} else if string(s[0]) == "(" && string(s[1]) != "*" { //this is not a pointer type
			s = "(*" + s[1:] //make it a pointer type
			if _, ok := allFns[s]; ok {
				covered[s] = s
			}
		}
	}

	//TODO: bz: how many uncovered are from cCmplGen?
	fmt.Println("\n#Coverage: ", (float64(len(covered))/float64(len(allFns)))*100, "%\t (#total: ", len(allFns), ", #compiled: ", len(cCmplFns),
		", #analyzed: ", len(covered), ", #analyzed$: ", len(closure), ", #others: ", len(other), ")") //others can be lib, reflect, <root>
}

//bz: when DoCoverage = true: compute (#analyzed fn/#total fn) in a program for ALL mains
func computeTotalCoverage() {
	covered := make(map[string]string)
	closure := make(map[string]string)
	other := make(map[string]string) //others can be lib, reflect, <root>

	for _, result := range main2ResultWCtx {
		for fn, _ := range result.CallGraph.Fn2CGNode {
			s := fn.String()
			if _, ok := allFns[s]; ok {
				covered[s] = s
			} else {
				subs := strings.Split(s, "$")
				if len(subs) == 1 {
					other[s] = s
				} else {
					sub := subs[0]
					if _, ok := allFns[sub]; ok {
						closure[s] = s
					}
				}
			}

			//bz: we have a mismatch here, e.g.,
			// (*google.golang.org/grpc/xds/internal/balancer/priority.s).Teardown in allFns
			//  -> (google.golang.org/grpc/xds/internal/balancer/priority.s).Teardown in our cg
			// or vice versa. Actaully, these two fn should have no diff when we handle their constraints.
			// SOLUTION -> convert the mismatch manually
			if s[0:2] == "(*" { //this is a pointer type
				s = "(" + s[2:] //remove pointer
				if _, ok := allFns[s]; ok {
					covered[s] = s
				}
			} else if string(s[0]) == "(" && string(s[1]) != "*" { //this is not a pointer type
				s = "(*" + s[1:] //make it a pointer type
				if _, ok := allFns[s]; ok {
					covered[s] = s
				}
			}
		}
	}

	fmt.Println("Coverage: ", (float64(len(covered))/float64(len(allFns)))*100, "%\t (#total: ", len(allFns), ", #compiled: ", len(cCmplFns),
		", #analyzed: ", len(covered), ", #analyzed$: ", len(closure), ", #others: ", len(other), ")")

	if showUnCoveredFn { //bz: not exposed to user yet
		fmt.Println("====================================================================")
		fmt.Println("DUMP UNCOVERED FUNCTIONS: (#", len(allFns)-len(covered), ")")
		for _, fn := range allFns {
			if _, ok := covered[fn]; ok {
				continue
			}
			fmt.Println(fn)
		}
		fmt.Println("====================================================================")
	}
}

//bz: stay here as a reference
//// Analyze runs the pointer analysis with the scope and options
//// specified by config, and returns the (synthetic) root of the callgraph.
////
//// Pointer analysis of a transitively closed well-typed program should
//// always succeed.  An error can occur only due to an internal bug.
////
//// bz: updated, works for context-sensitive but result does not include context-sensitive call graph
//func Analyze(config *Config) (result *ResultWCtx, err error) { //Result
//	if config.Mains == nil {
//		return nil, fmt.Errorf("no main/test packages to analyze (check $GOROOT/$GOPATH)")
//	}
//	defer func() {
//		if p := recover(); p != nil {
//			err = fmt.Errorf("internal error in pointer analysis: %v (please report this bug)", p)
//			fmt.Fprintln(os.Stderr, "Internal panic in pointer analysis:")
//			debug.PrintStack()
//		}
//	}()
//
//	a := &analysis{
//		config:      config,
//		log:         config.Log,
//		prog:        config.prog(),
//		globalval:   make(map[ssa.Value]nodeid),
//		globalobj:   make(map[ssa.Value]nodeid),
//		flattenMemo: make(map[types.Type][]*fieldInfo),
//		trackTypes:  make(map[types.Type]bool),
//		atFuncs:     make(map[*ssa.Function]bool),
//		hasher:      typeutil.MakeHasher(),
//		intrinsics:  make(map[*ssa.Function]intrinsic),
//		//result: &Result{
//		//	Queries:         make(map[ssa.Value]Pointer),
//		//	IndirectQueries: make(map[ssa.Value]Pointer),
//		//},
//		result: &ResultWCtx{
//			Queries:         make(map[ssa.Value][]PointerWCtx),
//			IndirectQueries: make(map[ssa.Value][]PointerWCtx),
//		},
//		deltaSpace: make([]int, 0, 100),
//		//bz: i did not clear these after offline TODO: do I ?
//		fn2cgnodeIdx: make(map[*ssa.Function][]int),
//		closures:     make(map[*ssa.Function]*Ctx2nodeid),
//	}
//
//	if false {
//		a.log = os.Stderr // for debugging crashes; extremely verbose
//	}
//
//	var mode string //which pta is running
//	if a.config.Origin {
//		mode = strconv.Itoa(a.config.K) + "-ORIGIN-SENSITIVE"
//	} else if a.config.CallSiteSensitive {
//		mode = strconv.Itoa(a.config.K) + "-CFA"
//	} else {
//		mode = "CONTEXT-INSENSITIVE"
//	}
//
//	if a.log != nil {
//		fmt.Fprintln(a.log, "==== Starting analysis: " + mode)
//	}
//	fmt.Println(" *** MODE: " + mode + " *** ")
//
//	// Pointer analysis requires a complete program for soundness.
//	// Check to prevent accidental misconfiguration.
//	for _, pkg := range a.prog.AllPackages() {
//		// (This only checks that the package scope is complete,
//		// not that func bodies exist, but it's a good signal.)
//		if !pkg.Pkg.Complete() {
//			return nil, fmt.Errorf(`pointer analysis requires a complete program yet package %q was incomplete`, pkg.Pkg.Path())
//		}
//	}
//
//	if reflect := a.prog.ImportedPackage("reflect"); reflect != nil {
//		rV := reflect.Pkg.Scope().Lookup("Value")
//		a.reflectValueObj = rV
//		a.reflectValueCall = a.prog.LookupMethod(rV.Type(), nil, "Call")
//		a.reflectType = reflect.Pkg.Scope().Lookup("Type").Type().(*types.Named)
//		a.reflectRtypeObj = reflect.Pkg.Scope().Lookup("rtype")
//		a.reflectRtypePtr = types.NewPointer(a.reflectRtypeObj.Type())
//
//		// Override flattening of reflect.Value, treating it like a basic type.
//		tReflectValue := a.reflectValueObj.Type()
//		a.flattenMemo[tReflectValue] = []*fieldInfo{{typ: tReflectValue}}
//
//		// Override shouldTrack of reflect.Value and *reflect.rtype.
//		// Always track pointers of these types.
//		a.trackTypes[tReflectValue] = true
//		a.trackTypes[a.reflectRtypePtr] = true
//
//		a.rtypes.SetHasher(a.hasher)
//		a.reflectZeros.SetHasher(a.hasher)
//	}
//	if runtime := a.prog.ImportedPackage("runtime"); runtime != nil {
//		a.runtimeSetFinalizer = runtime.Func("SetFinalizer")
//	}
//	a.computeTrackBits() //bz: use when there is input queries before running this analysis; we do not need this for now?
//
//	a.generate()   //bz: a preprocess for reflection/runtime/import libs
//	a.showCounts() //bz: print out size ...
//
//	if optRenumber {
//		a.renumber()
//	}
//
//	N := len(a.nodes) // excludes solver-created nodes
//
//	if optHVN { //bz: default true
//		if debugHVNCrossCheck { //default : false
//			// Cross-check: run the solver once without
//			// optimization, once with, and compare the
//			// solutions.
//			savedConstraints := a.constraints
//
//			a.solve()
//			a.dumpSolution("A.pts", N)
//
//			// Restore.
//			a.constraints = savedConstraints
//			for _, n := range a.nodes {
//				n.solve = new(solverState)
//			}
//			a.nodes = a.nodes[:N]
//
//			// rtypes is effectively part of the solver state.
//			a.rtypes = typeutil.Map{}
//			a.rtypes.SetHasher(a.hasher)
//		}
//
//		a.hvn()
//	}
//
//	if debugHVNCrossCheck {
//		runtime.GC()
//		runtime.GC()
//	}
//
//	if a.log != nil {
//		fmt.Fprintln(a.log, "==== Starting solving and generating constraints Online ====")
//	}
//
//	a.solve() //bz: officially starts here
//
//	// Compare solutions.
//	if optHVN && debugHVNCrossCheck {
//		a.dumpSolution("B.pts", N)
//
//		if !diff("A.pts", "B.pts") {
//			return nil, fmt.Errorf("internal error: optimization changed solution")
//		}
//	}
//
//	// Create callgraph.Nodes in deterministic order.
//	if cg := a.result.CallGraph; cg != nil {
//		for _, caller := range a.cgnodes {
//			cg.CreateNodeWCtx(caller) //bz: changed
//		}
//	}
//
//	// Add dynamic edges to call graph.
//	var space [100]int
//	for _, caller := range a.cgnodes {
//		for _, site := range caller.sites {
//			for _, callee := range a.nodes[site.targets].solve.pts.AppendTo(space[:0]) {
//				a.callEdge(caller, site, nodeid(callee))
//			}
//		}
//	}
//
//	return a.result, nil
//}
