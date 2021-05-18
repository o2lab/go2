// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pointer

// This file defines the internal (context-sensitive) call graph.

import (
	"fmt"
	"go/token"
	"strconv"

	"github.com/april1989/origin-go-tools/go/ssa"
)

var mainID nodeid //bz: record the target of root call to main method ID, NO REAL use, convenient for debug
var DEBUG bool    //bz: do we debug ...

//bz: update mainID, since opt.go will renumbering everything and change this
func UpdateMainID(newid nodeid) {
	mainID = newid
}

//bz: update DEBUG, whether we use mainID and UpdateMainID() in contourkFull()
func UpdateDEBUG(debug bool) {
	DEBUG = debug
}

//bz: this is the cgnode created by pointer analysis -> we force to use k callersite
type cgnode struct {
	fn         *ssa.Function
	obj        nodeid      // start of this contour's object block
	sites      []*callsite // ordered list of callsites within this function
	callersite []*callsite // where called from, if known; nil for shared contours ----> bz: k-caller site and context
	idx        int         // the index of this in a.cgnodes[]

	actualCallerSite [][]*callsite //bz: makeclosure of func and pass as a parameter, then it is called by a new go routine; these two ops are far away from each other.
	// e.g., Kubernetes.81091 : func (g *genericScheduler) findNodesThatFit().
	// cannot update this actualCallerSite during the iteration (genConstraint <-> solve), since its genDynamicCall()
	// -> I did not find a good way to fix this, so I attached the new go routine ctx here as actual_callersite
	//    so that the pointer can use this to match the query ctx. This field is updated when finalizing call graph

	localval map[ssa.Value]nodeid //bz: going to store things here and take away to get rid of queries
	localobj map[ssa.Value]nodeid //bz: same as above
}

//bz: my test use only
func (n *cgnode) Getlocalval() map[ssa.Value]nodeid {
	return n.localval
}

//bz: do i have a shared contour as my context?
func (n *cgnode) IsSharedContour() bool {
	return n.callersite == nil || len(n.callersite) == 0 || n.callersite[0] == nil
}

//bz: check existence of cs in n.actualCallerSite; if no, append it
func (n *cgnode) updateActualCallerSite(cs []*callsite) {
	exist := false
	for _, actual := range n.actualCallerSite {
		if equalCallSite(actual, cs) {
			exist = true
			break
		}
	}
	if exist {
		return
	}
	n.actualCallerSite = append(n.actualCallerSite, cs)
}

//bz: going to replace and store (borrow them a pointer) a.localval and a.localobj here when calling genFunc() (gen.go)
//only initialize when they are nil, reuse if exist (happens only if IsMySynthetic = true)
//only copy if func is in analysis scope
//return true if reuse
func (n *cgnode) initLocalMaps() bool {
	if n.localval == nil && n.localobj == nil { //bz: they should both be nil/exist
		n.localval = make(map[ssa.Value]nodeid)
		n.localobj = make(map[ssa.Value]nodeid)
		return false
	}
	return true
}

//bz: if not use queries, do this renumbering
func (n *cgnode) renumber(renumbering []nodeid) {
	for v, oldID := range n.localval {
		n.localval[v] = renumbering[oldID]
	}
	for obj, oldID := range n.localobj {
		n.localobj[obj] = renumbering[oldID]
	}
}

//bz: renumber used for hvn
func (n *cgnode) renumberHVN(mapping []nodeid) {
	for v, oldID := range n.localval {
		nid := mapping[oldID]
		if nid == 0 || nid == oldID { //bz: not updated or not re-mapped
			continue
		}
		n.localval[v] = nid
	}
	for obj, oldID := range n.localobj {
		nid := mapping[oldID]
		if nid == 0 || nid == oldID { //bz: not updated or not re-mapped
			continue
		}
		n.localobj[obj] = nid
	}
}

// contour returns a description of this node's contour.
//bz: default but with adjustment. only used for log
func (n *cgnode) contour(isKcfa bool) string {
	if isKcfa { //bz: print out info for kcfa
		return n.contourkFull()
	}
	//bz: adjust for context-insensitive. Same as 1callsite, only showing the most recent callersite info
	if n.callersite == nil || len(n.callersite) == 0 || n.callersite[0] == nil {
		return "shared contour"
	}
	if n.callersite[0].instr != nil {
		return fmt.Sprintf("as called from %s", n.callersite[0].instr.Parent())
	}
	return fmt.Sprintf("as called to synthetic/intrinsic (targets=n%d)", n.callersite[0].targets)
}

//bz: convenient
func contourK(n *cgnode, callersite []*callsite) string {
	var s string
	for idx, cs := range callersite {
		if cs == nil {
			s = s + strconv.Itoa(idx) + ":shared contour; "
			continue
		}
		if cs.instr != nil {
			s = s + strconv.Itoa(idx) + ":" + cs.String() + "; " //cs.instr.String() + "@" + cs.instr.Parent().String() + "; "
			continue
		}
		if n.fn.String() == "command-line-arguments.main" { //bz: the ctx is "called to synthetic/intrinsic func@n?"; which is root node calling to main.main
			s = s + strconv.Itoa(idx) + ":root call to command-line-arguments.main; "
			if DEBUG && mainID == 0 { //bz: initial just once; this has NO REAL use, just convenient for debug
				mainID = cs.targets
			}
			continue
		}
		if DEBUG && cs.targets == mainID { //bz: same as above
			s = s + strconv.Itoa(idx) + ":root call to command-line-arguments.main; "
			continue
		}
		s = s + strconv.Itoa(idx) + ":" + cs.String() + "; " //":" + "called to synthetic func@" + cs.targets.String() + "; " //func id + cgnode id
	}
	return s
}

//bz: adjust contour() to kcfa and origin ---> this is only used when printing out all call graph information
func (n *cgnode) contourkFull() string {
	var s string
	s = s + "["
	s = s + contourK(n, n.callersite)
	s = s + "]"
	return s
}

//bz:
func (n *cgnode) contourkActualFull() string {
	var s string
	s = s + "["
	for idx, actualcs := range n.actualCallerSite {
		s = s + "Actual " + strconv.Itoa(idx) + ": " + contourK(n, actualcs) + "; "
	}
	s = s + "]"
	return s
}

func (n *cgnode) String() string {
	return fmt.Sprintf("cg%d:%s@%s", n.obj, n.fn, n.contourkFull())
}

// A callsite represents a single call site within a cgnode;
// it is implicitly context-sensitive.
// callsites never represent calls to built-ins;
// they are handled as intrinsics.
// bz: this is the call site we used
type callsite struct {
	targets nodeid              // pts(·) contains objects for dynamically called functions
	instr   ssa.CallInstruction // the call instruction; nil for synthetic/intrinsic
	loopID  int                 // bz: origin -> loop id, value is 1 or 2; 0 is default value and means no loop TODO: how to get rid of this in other contexts?
	goInstr *ssa.Go             // TODO: bz: do we add this to match goID in race_checker ??
}

//bz: user api: race checke uses
func (c *callsite) GetLoopID() int {
	if c == nil {
		return -1
	}
	return c.loopID
}

//bz: to see if two callsites are the same
//tmp solution, to compare string ... otherwise too strict ...
//e.g., return c.targets == other.targets && c.instr.String() == other.instr.String() ----->  this might be too strict ...
func (c *callsite) equal(o *callsite) bool {
	if o == nil && c == nil {
		return true
	}
	if o == nil || c == nil {
		return false
	}
	cInstr := c.instr
	oInstr := o.instr
	if cInstr == nil && oInstr == nil {
		return c.targets == o.targets && c.loopID == o.loopID
	} else if cInstr == nil || oInstr == nil {
		return false //one is k callsite, one is from closure
	} else { // most cases, comparing between callsite and callsite
		return cInstr.String() == oInstr.String() && cInstr.Parent().String() == oInstr.Parent().String() && c.loopID == o.loopID
	}
}

//bz: equal without when one c.loopID == 0 and o.loopID is 1 or 2 or 0
// !! only used by existClosure()
func (c *callsite) loopEqual(o *callsite) bool {
	if o == nil && c == nil {
		return true
	}
	if o == nil || c == nil {
		return false
	}
	cInstr := c.instr
	oInstr := o.instr
	if cInstr == nil && oInstr == nil {
		return c.targets == o.targets && c.loopID == 0
	} else if cInstr == nil || oInstr == nil {
		return false //one is k callsite, one is from closure
	} else { // most cases, comparing between callsite and callsite
		return cInstr.String() == oInstr.String() && cInstr.Parent().String() == oInstr.Parent().String() && c.loopID == 0
	}
}

//bz: used in commonpart.go -> equal if instr equal
func (c *callsite) relaxEqual(o *callsite) bool {
	if o == nil && c == nil {
		return true
	}
	if o == nil || c == nil {
		return false
	}
	cInstr := c.instr
	oInstr := o.instr
	if cInstr == nil && oInstr == nil {
		return true
	} else if cInstr == nil || oInstr == nil {
		return false //one is k callsite, one is from closure
	} else { // most cases, comparing between callsite and callsite
		return cInstr.String() == oInstr.String()
	}
}

//bz: equal if goInstr equal
func (c *callsite) goEqual(o *callsite) bool {
	if o == nil || c == nil {
		return false
	}
	cGo := c.goInstr
	oGo := o.goInstr
	if cGo == nil && oGo == nil {
		return false
	} else if cGo == nil || cGo == nil {
		return false //one has go, another does not
	} else { // most cases, comparing between goInstr
		return cGo == oGo
	}
}

func (c *callsite) String() string {
	if c == nil {
		return "nil"
	}
	if c.instr != nil {
		//return c.instr.Common().Description() //bz: original code
		if c.loopID == 0 {
			return c.instr.String() + "@" + c.instr.Parent().String()
		} else {
			return "Loop" + strconv.Itoa(c.loopID) + " & " + c.instr.String() + "@" + c.instr.Parent().String()
		}
	}

	if c.loopID == 0 {
		return "synthetic function call@" + c.targets.String()
	} else {
		return "Loop" + strconv.Itoa(c.loopID) + " & " + "synthetic function call@" + c.targets.String()
	}
}

// pos returns the source position of this callsite, or token.NoPos if implicit.
func (c *callsite) pos() token.Pos {
	if c.instr != nil {
		return c.instr.Pos()
	}
	return token.NoPos
}

//bz: to record the 1callsite from caller to nodeid for makeclosure, work together with a.closures[]
// for origin, this 1callsite contains the loop id if directly invoked, if too many levels we will lose it ...
//TODO: full callchain ??
type Ctx2nodeid struct {
	ctx2nodeid map[*callsite][]nodeid
}

//bz: renumbering func for opt
func (r *Ctx2nodeid) renumber(renumbering []nodeid) {
	for site, oldIDs := range r.ctx2nodeid {
		newIDs := make([]nodeid, len(oldIDs))
		for idx, oldID := range oldIDs {
			newIDs[idx] = renumbering[oldID]
		}
		r.ctx2nodeid[site] = newIDs
	}
}

//bz: renumbering func for hvn
func (r *Ctx2nodeid) renumberHVN(mapping []nodeid) {
	for cs, array := range r.ctx2nodeid {
		objs := make([]nodeid, len(array))
		for idx, ele := range array {
			nid := mapping[ele]
			if nid == 0 || nid == ele { //bz: not updated or not re-mapped
				continue
			}
			objs[idx] = nid
		}
		r.ctx2nodeid[cs] = objs
	}
}

//////////////////////////////// call graph for end users ////////////////////////////////

//bz: for user
type GraphWCtx struct {
	Root      *Node                       // the distinguished root node
	Nodes     map[*cgnode]*Node           // all nodes by cgnode
	Fn2CGNode map[*ssa.Function][]*cgnode // a map

	numEdges int //bz: perforamnce, number of edges
}

func (g *GraphWCtx) GetNumEdges() int {
	return g.numEdges
}

// bz: New returns a new Graph with the specified root node.
// TODO: bz: do we need lock here if run in parallel?
func NewWCtx(root *cgnode) *GraphWCtx {
	g := &GraphWCtx{Nodes: make(map[*cgnode]*Node), Fn2CGNode: make(map[*ssa.Function][]*cgnode)}
	g.Root = g.CreateNodeWCtx(root)
	return g
}

// bz: CreateNode returns the Node for fn, creating it if not present.
func (g *GraphWCtx) CreateNodeWCtx(cgn *cgnode) *Node {
	n, ok := g.Nodes[cgn]
	if !ok {
		n = &Node{cgn: cgn, ID: len(g.Nodes)}
		g.Nodes[cgn] = n
	}
	return n
}

//bz:
func (g *GraphWCtx) GetNodeWCtx(cgn *cgnode) *Node {
	return g.Nodes[cgn]
}

//bz: compute at final
func (g *GraphWCtx) computeFn2CGNode() {
	for cgn := range g.Nodes {
		m := g.Fn2CGNode[cgn.fn]
		m = append(m, cgn)
		g.Fn2CGNode[cgn.fn] = m
	}
}

// A Node represents a node in a call graph.
// bz: add uint32 type here (copied from analysis.go.nodeid type, since cannot share),
// use this for context-sensitive to retrieve cgn
// also updated the related functions in this file, skip their comments
type Node struct {
	cgn *cgnode // the cgnode this node represents
	ID  int     // 0-based sequence number  ----> bz: useful ??
	In  []*Edge // unordered set of incoming call edges (n.In[*].Callee == n)
	Out []*Edge // unordered set of outgoing call edges (n.Out[*].Caller == n)
}

//bz: user API: get *ssa.Function
func (n *Node) GetFunc() *ssa.Function {
	return n.cgn.fn
}

//bz: user API: get cgn with its function (*ssa.Function) and context ([]*callsite)
func (n *Node) GetCGNode() *cgnode {
	return n.cgn
}

//bz: add API for users; get contexts
func (n *Node) GetContext() []*callsite {
	return n.cgn.callersite
}

func (n *Node) String() string {
	return fmt.Sprintf("n%d:%s", n.ID, n.cgn)
}

// A Edge represents an edge in the call graph.
//
// Site is nil for edges originating in synthetic or intrinsic
// functions, e.g. reflect.Call or the root of the call graph.
type Edge struct {
	Caller *Node
	Site   ssa.CallInstruction
	Callee *Node
}

func (e Edge) String() string {
	return fmt.Sprintf("%s --(%s)--> %s", e.Caller, e.Site, e.Callee)
}

func (e Edge) Description() string {
	var prefix string
	switch e.Site.(type) {
	case nil:
		return "synthetic call"
	case *ssa.Go:
		prefix = "concurrent "
	case *ssa.Defer:
		prefix = "deferred "
	}
	return prefix + e.Site.Common().Description()
}

func (e Edge) Pos() token.Pos {
	if e.Site == nil {
		return token.NoPos
	}
	return e.Site.Pos()
}

// AddEdge adds the edge (caller, site, callee) to the call graph.
// Elimination of duplicate edges is the caller's responsibility.
func (g *GraphWCtx) AddEdge(caller *Node, site ssa.CallInstruction, callee *Node) {
	e := &Edge{caller, site, callee}
	callee.In = append(callee.In, e)
	caller.Out = append(caller.Out, e)
	g.numEdges++
}

// DeleteNode removes node n and its edges from the graph g.
// (NB: not efficient for batch deletion.)
func (g *GraphWCtx) DeleteNode(n *Node) {
	n.deleteIns()
	n.deleteOuts()
	delete(g.Nodes, n.GetCGNode())
}

//bz: make the following methods be obj-oriented to make it thread safe
// deleteIns deletes all incoming edges to n.
func (n *Node) deleteIns() {
	for _, e := range n.In {
		n.removeOutEdge(e)
	}
	n.In = nil
}

// deleteOuts deletes all outgoing edges from n.
func (n *Node) deleteOuts() {
	for _, e := range n.Out {
		n.removeInEdge(e)
	}
	n.Out = nil
}

// removeOutEdge removes edge.Caller's outgoing edge 'edge'.
func (node *Node) removeOutEdge(edge *Edge) {
	caller := edge.Caller
	n := len(caller.Out)
	for i, e := range caller.Out {
		if e == edge {
			// Replace it with the final element and shrink the slice.
			caller.Out[i] = caller.Out[n-1]
			caller.Out[n-1] = nil // aid GC
			caller.Out = caller.Out[:n-1]
			return
		}
	}
	panic("edge not found: " + edge.String())
}

// removeInEdge removes edge.Callee's incoming edge 'edge'.
func (node *Node) removeInEdge(edge *Edge) {
	caller := edge.Callee
	n := len(caller.In)
	for i, e := range caller.In {
		if e == edge {
			// Replace it with the final element and shrink the slice.
			caller.In[i] = caller.In[n-1]
			caller.In[n-1] = nil // aid GC
			caller.In = caller.In[:n-1]
			return
		}
	}
	panic("edge not found: " + edge.String())
}

//bz: used in commonpart.go
func (g *GraphWCtx) GetNodesForFn(fn *ssa.Function) []*Node {
	cgns := g.Fn2CGNode[fn]
	var result []*Node
	for _, cgn := range cgns {
		n := g.GetNodeWCtx(cgn)
		if n == nil {
			continue
		}
		result = append(result, n)
	}
	return result
}

//bz: if two []*callsite, csa and csb, are the same
func equalCallSite(csa []*callsite, csb []*callsite) bool {
	if len(csa) != len(csb) {
		return false
	}
	size := len(csa)
	for i:= 0; i<size; i++ {
		if !csa[i].equal(csb[i]) {
			return false
		}
	}
	return true
}
