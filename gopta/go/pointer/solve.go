// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pointer

// This file defines a naive Andersen-style solver for the inclusion
// constraint system.

import (
	"fmt"
	"github.com/april1989/origin-go-tools/go/myutil/flags"
	"go/types"
)

type solverState struct {
	complex []constraint // complex constraints attached to this node
	copyTo  nodeset      // simple copy constraint edges
	pts     nodeset      // points-to set of this node
	prevPTS nodeset      // pts(n) in previous iteration (for difference propagation)
}

//bz: to limit the size of pts
var (
	ptsLimit  int
	skipIDs   map[int]int //these pts reach the ptsLimit, skip their solving if added to worklist
	skipNewFn bool        //bz: do we skip create new fn/cgn/constraints on the fly in invokeConstraint.solve
)

func (a *analysis) solve() {
	if flags.DoPerformance { //bz: performance dump info
		a.num_constraints = 0
		fmt.Println("#constraints (before solve()): ", len(a.constraints))
		fmt.Println("#cgnodes (before solve()): ", len(a.cgnodes))
		fmt.Println("#nodes (before solve()): ", len(a.nodes))

		if flags.PrintCGNodes { //bz: debug
			fmt.Println("\nDump cgnodes (before solve()): ")
			for i, cgn := range a.cgnodes {
				fmt.Println(i, ". ", cgn.String())
			}
		}
	}

	start("Solving")
	if a.log != nil {
		fmt.Fprintf(a.log, "\n\n==== Solving constraints\n\n")
	}

	// Solver main loop: separate to avoid frequent bool check of whether we do ptslimit
	if flags.PTSLimit == 0 {
		a.solveDefault()
	} else {
		a.solveLimit()
	}

	//bz: back to normal workflow
	if !a.nodes[0].solve.pts.IsEmpty() {
		panic(fmt.Sprintf("pts(0) is nonempty: %s", &a.nodes[0].solve.pts))
	}

	// Release working state (but keep final PTS).
	for _, n := range a.nodes {
		n.solve.complex = nil
		n.solve.copyTo.Clear()
		n.solve.prevPTS.Clear()
	}

	if a.log != nil {
		fmt.Fprintf(a.log, "Solver done\n")

		// Dump solution.
		for i, n := range a.nodes {
			if !n.solve.pts.IsEmpty() {
				fmt.Fprintf(a.log, "pts(n%d) = %s : %s\n", i, &n.solve.pts, n.typ)
			}
		}
	}
	stop("Solving")
}

//bz: default solve(); use when flags.PTSLimit == 0
func (a *analysis) solveDefault() {
	// Solver main loop.
	var delta nodeset
	for {
		// Add new constraints to the graph:
		// static constraints from SSA on round 1,
		// dynamic constraints from reflection thereafter.
		a.processNewConstraints()

		var x int
		if !a.work.TakeMin(&x) {
			break // empty worklist
		}

		id := nodeid(x)
		if a.log != nil {
			fmt.Fprintf(a.log, "\tnode n%d\n", id)
		}

		n := a.nodes[id]

		// Difference propagation.
		delta.Difference(&n.solve.pts.Sparse, &n.solve.prevPTS.Sparse)
		if delta.IsEmpty() {
			continue
		}
		if a.log != nil {
			//fmt.Fprintf(a.log, "\t\tpts(n%d : %s) = %s + %s\n", id, n.typ, &delta, &n.solve.prevPTS)  //bz: too verbose
			fmt.Fprintf(a.log, "\t\tpts(n%d : %s) = %s + ... \n", id, n.typ, &delta)
		}
		n.solve.prevPTS.Copy(&n.solve.pts.Sparse)

		// Apply all resolution rules attached to n.
		a.solveConstraints(n, &delta)

		if a.log != nil {
			fmt.Fprintf(a.log, "\t\tpts(n%d) = %s\n", id, &n.solve.pts)
		}
	}
}

//bz: solve(); use when flags.PTSLimit > 0
func (a *analysis) solveLimit() {
	//setting
	ptsLimit = flags.PTSLimit
	if len(a.constraints) > 5000000 {
		skipNewFn = true //bz: too much ... cannot finish
	}
	skipIDs = make(map[int]int)
	if flags.DoPrintInfo {
		fmt.Println(" *** PTS Limit:", ptsLimit, "*** ")
	}

	// Solver main loop.
	var delta nodeset
	for {
		// Add new constraints to the graph:
		// static constraints from SSA on round 1,
		// dynamic constraints from reflection thereafter.
		a.processNewConstraints()

		var x int
		if !a.work.TakeMin(&x) {
			break // empty worklist
		}

		if _, ok := skipIDs[x]; ok { //bz: skip its solving for those pts size reach limit
			continue
		}

		id := nodeid(x)
		if a.log != nil {
			fmt.Fprintf(a.log, "\tnode n%d\n", id)
		}

		n := a.nodes[id]

		// Difference propagation.
		delta.Difference(&n.solve.pts.Sparse, &n.solve.prevPTS.Sparse)
		if delta.IsEmpty() {
			continue
		}
		if a.log != nil {
			//fmt.Fprintf(a.log, "\t\tpts(n%d : %s) = %s + %s\n", id, n.typ, &delta, &n.solve.prevPTS)  //bz: too verbose
			fmt.Fprintf(a.log, "\t\tpts(n%d : %s) = %s + ... \n", id, n.typ, &delta)
		}

		//n.solve.prevPTS.Copy(&n.solve.pts.Sparse) //bz: copy then check
		//if n.solve.pts.Len() >= ptsLimit {
		//	skipIDs[x] = x
		//}

		if n.solve.pts.Len() >= ptsLimit { //bz: check then copy
			skipIDs[x] = x
			n.solve.prevPTS.Clear()
			continue
		}else {
			n.solve.prevPTS.Copy(&n.solve.pts.Sparse)
		}


		// Apply all resolution rules attached to n.
		a.solveConstraints(n, &delta)

		if a.log != nil {
			fmt.Fprintf(a.log, "\t\tpts(n%d) = %s\n", id, &n.solve.pts)
		}
	}
}

// processNewConstraints takes the new constraints from a.constraints
// and adds them to the graph, ensuring
// that new constraints are applied to pre-existing labels and
// that pre-existing constraints are applied to new labels.
//
func (a *analysis) processNewConstraints() {
	// Take the slice of new constraints.
	// (May grow during call to solveConstraints.)
	if flags.DoPerformance && len(a.constraints) > 0 {
		a.num_constraints = a.num_constraints + len(a.constraints)
		//if len(a.constraints) > 0 { //bz: debug: changed
		//	fmt.Println("#constraints (during solve()): ", a.num_constraints)
		//	fmt.Println("#cgnodes (during solve()): ", len(a.cgnodes))
		//}
	}
	constraints := a.constraints
	a.constraints = nil

	if a.config.Log != nil {
		fmt.Fprintf(a.log, "\t\tnew constraints........... #%d\n", len(constraints))
	}
	// Initialize points-to sets from addr-of (base) constraints.
	for _, c := range constraints {
		if c, ok := c.(*addrConstraint); ok {
			dst := a.nodes[c.dst]
			dst.solve.pts.add(c.src)

			// Populate the worklist with nodes that point to
			// something initially (due to addrConstraints) and
			// have other constraints attached.
			// (A no-op in round 1.)
			if !dst.solve.copyTo.IsEmpty() || len(dst.solve.complex) > 0 {
				a.addWork(c.dst) //bz: original code: add to worklist
			}
		}
	}

	// Attach simple (copy) and complex constraints to nodes.
	var stale nodeset
	for _, c := range constraints {
		var id nodeid
		switch c := c.(type) {
		case *addrConstraint:
			// base constraints handled in previous loop
			continue
		case *copyConstraint:
			// simple (copy) constraint
			id = c.src
			a.nodes[id].solve.copyTo.add(c.dst)
		default:
			// complex constraint
			id = c.ptr()
			solve := a.nodes[id].solve
			solve.complex = append(solve.complex, c)
		}

		if n := a.nodes[id]; !n.solve.pts.IsEmpty() {
			if !n.solve.prevPTS.IsEmpty() {
				stale.add(id)
			}
			a.addWork(id)
		}
	}
	if a.config.Log != nil {
		fmt.Fprintf(a.log, "\t\t......................\n")
	}

	// Apply new constraints to pre-existing PTS labels.
	var space [50]int
	for _, id := range stale.AppendTo(space[:0]) {
		n := a.nodes[nodeid(id)]
		if a.config.Log != nil {
			fmt.Fprintf(a.log, "\t\tstale %d: pts(%s) = %s + %s\n", id, n.typ, &n.solve.prevPTS, &n.solve.prevPTS)
		}
		a.solveConstraints(n, &n.solve.prevPTS)
	}
}

// solveConstraints applies each resolution rule attached to node n to
// the set of labels delta.  It may generate new constraints in
// a.constraints.
//
func (a *analysis) solveConstraints(n *node, delta *nodeset) {
	if delta.IsEmpty() {
		return
	}

	// Process complex constraints dependent on n.
	for _, c := range n.solve.complex {
		if a.log != nil {
			fmt.Fprintf(a.log, "\t\tconstraint %s\n", c)
		}
		c.solve(a, delta)
	}

	// Process copy constraints.
	var copySeen nodeset
	for _, x := range n.solve.copyTo.AppendTo(a.deltaSpace) {
		mid := nodeid(x)
		if copySeen.add(mid) {
			if a.nodes[mid].solve.pts.addAll(delta) {
				a.addWork(mid)
			}
		}
	}
}

// addLabel adds label to the points-to set of ptr and reports whether the set grew.
func (a *analysis) addLabel(ptr, label nodeid) bool {
	b := a.nodes[ptr].solve.pts.add(label)
	if b && a.log != nil {
		fmt.Fprintf(a.log, "\t\tpts(n%d) += n%d\n", ptr, label)
	}
	return b
}

func (a *analysis) addWork(id nodeid) {
	a.work.Insert(int(id))
	if a.log != nil {
		if a.online {
			fmt.Fprintf(a.log, "\t\tadd to work (online): n%d\n", id)
		} else {
			fmt.Fprintf(a.log, "\t\tadd to work: n%d\n", id)
		}
	}
}

// onlineCopy adds a copy edge.  It is called Online, i.e. during
// solving, so it adds edges and pts members directly rather than by
// instantiating a 'constraint'.
//
// The size of the copy is implicitly 1.
// It returns true if pts(dst) changed.
//
func (a *analysis) onlineCopy(dst, src nodeid) bool {
	if dst != src {
		if nsrc := a.nodes[src]; nsrc.solve.copyTo.add(dst) {
			if a.log != nil {
				fmt.Fprintf(a.log, "\t\t\tdynamic copy n%d <- n%d\n", dst, src)
			}
			// TODO(adonovan): most calls to onlineCopy
			// are followed by addWork, possibly batched
			// via a 'changed' flag; see if there's a
			// noticeable penalty to calling addWork here.
			return a.nodes[dst].solve.pts.addAll(&nsrc.solve.pts)
		}
	}
	return false
}

// Returns sizeof.
// Implicitly adds nodes to worklist.
//
// TODO(adonovan): now that we support a.copy() during solving, we
// could eliminate onlineCopyN, but it's much slower.  Investigate.
//
func (a *analysis) onlineCopyN(dst, src nodeid, sizeof uint32) uint32 {
	for i := uint32(0); i < sizeof; i++ {
		if a.onlineCopy(dst, src) {
			a.addWork(dst)
		}
		src++
		dst++
	}
	return sizeof
}

//bz: different solves for complex instructions
func (c *loadConstraint) solve(a *analysis, delta *nodeset) {
	var changed bool
	for _, x := range delta.AppendTo(a.deltaSpace) {
		k := nodeid(x)
		koff := k + nodeid(c.offset)
		if a.onlineCopy(c.dst, koff) {
			changed = true
		}
	}
	if changed {
		a.addWork(c.dst)
	}
}

func (c *storeConstraint) solve(a *analysis, delta *nodeset) {
	for _, x := range delta.AppendTo(a.deltaSpace) {
		k := nodeid(x)
		koff := k + nodeid(c.offset)
		if a.onlineCopy(koff, c.src) {
			a.addWork(koff)
		}
	}
}

func (c *offsetAddrConstraint) solve(a *analysis, delta *nodeset) {
	dst := a.nodes[c.dst]
	for _, x := range delta.AppendTo(a.deltaSpace) {
		k := nodeid(x)
		if dst.solve.pts.add(k + nodeid(c.offset)) {
			a.addWork(c.dst)
		}
	}
}

func (c *typeFilterConstraint) solve(a *analysis, delta *nodeset) {
	for _, x := range delta.AppendTo(a.deltaSpace) {
		ifaceObj := nodeid(x)
		tDyn, _, indirect := a.taggedValue(ifaceObj)
		if indirect {
			// TODO(adonovan): we'll need to implement this
			// when we start creating indirect tagged objects.
			panic("indirect tagged object")
		}

		//TODO: bz: this can cause panic if using REFLECTION and this x propagate to invoke constraints
		// that requires implemented function body. e.g., @google.golang.org/grpc.test has panic:
		//   n251518: no ssa.Function for func (fmt.Stringer).String() string
		// did not see this when REFLECTION is off
		// Tmp solution -> tDyn and c.typ cannot be exactly the same? this probably is not the root cause
		if a.config.Reflection {
			if types.AssignableTo(tDyn, c.typ) {
				if _, ok := c.typ.(*types.Pointer); !ok {
					if a.log != nil {
						fmt.Fprintf(a.log, "\t\tskip: n%d has type (%s) with no virtual function\n", ifaceObj, tDyn)
					}
					return
				}
				if a.addLabel(c.dst, ifaceObj) {
					a.addWork(c.dst)
				}
			}
		} else {
			if types.AssignableTo(tDyn, c.typ) {
				if a.addLabel(c.dst, ifaceObj) {
					a.addWork(c.dst)
				}
			}
		}
	}
}

//bz: panic always happens; hardcode to avoid this ...
func (c *untagConstraint) solve(a *analysis, delta *nodeset) {
	predicate := types.AssignableTo
	if c.exact {
		predicate = types.Identical
	}
	for _, x := range delta.AppendTo(a.deltaSpace) {
		ifaceObj := nodeid(x)
		tDyn, v, indirect := a.taggedValue(ifaceObj)
		if indirect {
			// TODO(adonovan): we'll need to implement this
			// when we start creating indirect tagged objects.
			panic("indirect tagged object")
		}

		if predicate(tDyn, c.typ) {
			// Copy payload sans tag to dst.
			//
			// TODO(adonovan): opt: if tDyn is
			// nonpointerlike we can skip this entire
			// constraint, perhaps.  We only care about
			// pointers among the fields.
			a.onlineCopyN(c.dst, v, a.sizeof(tDyn))
		}
	}
}

//bz: this solves for invoke calls, needs to be updated for kcfa
func (c *invokeConstraint) solve(a *analysis, delta *nodeset) {
	for _, x := range delta.AppendTo(a.deltaSpace) {
		ifaceObj := nodeid(x)
		tDyn, v, indirect := a.taggedValue(ifaceObj)
		if indirect {
			// TODO(adonovan): we'll need to implement this
			// when we start creating indirect tagged objects.
			panic("indirect tagged object")
		}

		// Look up the concrete method.
		fn := a.prog.LookupMethod(tDyn, c.method.Pkg(), c.method.Name())
		if fn == nil {
			panic(fmt.Sprintf("n%d: no ssa.Function for %s", c.iface, c.method))
		}
		sig := fn.Signature
		fnObj := a.globalobj[fn] // dynamic calls use shared contour  ---> bz: fnObj is nodeid

		if fnObj == 0 { //bz: because a.objectNode(fn) was not called during gen phase or fn is stored at other fields
			if skipNewFn {
				//bz: already too many cgn/fn ... skip creating on the fly TODO: this needs to be fixed
				return
			}
			//bz: this should not create new constraints anymore; just retrieve the existing nodeid for fn
			fnObj = a.genMissingFn(fn, c.caller, c.site, "online") // should not create any new things if using DoCallback && preSolve()
			if fnObj == 0 {                                        //return 0 for out of scope functions
				continue
			}
		} else {
			if a.log != nil { //debug
				fmt.Fprintf(a.log, "!! ALREADY EXIST INVOKE FUNC: "+fn.String()+"\n")
			}
		}
		// bz: back to normal workflow
		c.eachSolve(a, fnObj, sig, v)
	}
}

//bz: no need to match context for caller/callee, everything is created unique for each context,
//if a constraint exists, it is correct
func (c *invokeConstraint) eachSolve(a *analysis, fnObj nodeid, sig *types.Signature, v nodeid) {
	// Make callsite's fn variable point to identity of
	// concrete method.  (There's no need to add it to
	// worklist since it never has attached constraints.)
	a.addLabel(c.params, fnObj)

	// Extract value and connect to method's receiver.
	// Copy payload to method's receiver param (arg0).
	arg0 := a.funcParams(fnObj)
	recvSize := a.sizeof(sig.Recv().Type())
	a.onlineCopyN(arg0, v, recvSize)

	src := c.params + 1 // skip past identity
	dst := arg0 + nodeid(recvSize)

	// Copy caller's argument block to method formal parameters.
	paramsSize := a.sizeof(sig.Params())
	a.onlineCopyN(dst, src, paramsSize)
	src += nodeid(paramsSize)
	dst += nodeid(paramsSize)

	// Copy method results to caller's result block.
	resultsSize := a.sizeof(sig.Results())
	a.onlineCopyN(src, dst, resultsSize)
}

func (c *addrConstraint) solve(a *analysis, delta *nodeset) {
	panic("addr is not a complex constraint")
}

func (c *copyConstraint) solve(a *analysis, delta *nodeset) {
	panic("copy is not a complex constraint")
}
