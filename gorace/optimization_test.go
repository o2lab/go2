package main

import (
	"testing"

	"github.com/twmb/algoimpl/go/graph"
)

// ============================================================================
// Phase 1: BFS Queue Optimization Tests
// ============================================================================

func TestQueueBasicOperations(t *testing.T) {
	g := graph.New(graph.Directed)
	n1 := g.MakeNode()
	n2 := g.MakeNode()
	n3 := g.MakeNode()

	q := &queue{}

	// Empty queue
	if !q.isEmpty() {
		t.Error("new queue should be empty")
	}

	// Enqueue and dequeue single element
	q.enQueue(n1)
	if q.isEmpty() {
		t.Error("queue should not be empty after enqueue")
	}
	if q.size() != 1 {
		t.Errorf("queue size should be 1, got %d", q.size())
	}

	got := q.deQueue()
	if got != n1 {
		t.Error("dequeued node should match enqueued node")
	}
	if !q.isEmpty() {
		t.Error("queue should be empty after dequeuing only element")
	}

	// FIFO order
	q2 := &queue{}
	q2.enQueue(n1)
	q2.enQueue(n2)
	q2.enQueue(n3)
	if q2.size() != 3 {
		t.Errorf("queue size should be 3, got %d", q2.size())
	}

	first := q2.deQueue()
	second := q2.deQueue()
	third := q2.deQueue()
	if first != n1 || second != n2 || third != n3 {
		t.Error("queue should maintain FIFO order")
	}
	if !q2.isEmpty() {
		t.Error("queue should be empty after dequeuing all elements")
	}
}

func TestQueueInterleavedOps(t *testing.T) {
	g := graph.New(graph.Directed)
	n1 := g.MakeNode()
	n2 := g.MakeNode()
	n3 := g.MakeNode()
	n4 := g.MakeNode()

	q := &queue{}
	q.enQueue(n1)
	q.enQueue(n2)
	got := q.deQueue()
	if got != n1 {
		t.Error("expected n1")
	}
	q.enQueue(n3)
	q.enQueue(n4)
	if q.size() != 3 {
		t.Errorf("expected size 3, got %d", q.size())
	}

	// Should get n2, n3, n4 in order
	if q.deQueue() != n2 {
		t.Error("expected n2")
	}
	if q.deQueue() != n3 {
		t.Error("expected n3")
	}
	if q.deQueue() != n4 {
		t.Error("expected n4")
	}
	if !q.isEmpty() {
		t.Error("queue should be empty")
	}
}

func TestQueueLargeVolume(t *testing.T) {
	g := graph.New(graph.Directed)
	const N = 10000
	nodes := make([]graph.Node, N)
	for i := 0; i < N; i++ {
		nodes[i] = g.MakeNode()
		*nodes[i].Value = i
	}

	q := &queue{}
	for _, n := range nodes {
		q.enQueue(n)
	}
	if q.size() != N {
		t.Errorf("expected size %d, got %d", N, q.size())
	}

	for i := 0; i < N; i++ {
		node := q.deQueue()
		if *node.Value != *nodes[i].Value {
			t.Errorf("at position %d: expected value %v, got %v", i, *nodes[i].Value, *node.Value)
			break
		}
	}
	if !q.isEmpty() {
		t.Error("queue should be empty after dequeuing all")
	}
}

// ============================================================================
// Phase 1: BFS Visited Set (map-based) Tests
// ============================================================================

// TestSliceContainsNode tests the current O(n) lookup function.
// After optimization, this function should still work OR be replaced by map lookups
// with identical behavior.
func TestSliceContainsNodeBasic(t *testing.T) {
	g := graph.New(graph.Directed)
	n1 := g.MakeNode()
	*n1.Value = "a"
	n2 := g.MakeNode()
	*n2.Value = "b"
	n3 := g.MakeNode()
	*n3.Value = "a" // same content as n1 but different node

	slice := []graph.Node{n1, n2}

	if !sliceContainsNode(slice, n1) {
		t.Error("should find n1 in slice")
	}
	if !sliceContainsNode(slice, n2) {
		t.Error("should find n2 in slice")
	}
	// sliceContainsNode compares n.Value (the pointer), not *n.Value (the content).
	// n3 is a different node with a different .Value pointer, so it should NOT be found.
	if sliceContainsNode(slice, n3) {
		t.Error("different node (different .Value pointer) should NOT be found even with same content")
	}
}

func TestSliceContainsNodeEmpty(t *testing.T) {
	g := graph.New(graph.Directed)
	n := g.MakeNode()
	*n.Value = "x"
	if sliceContainsNode(nil, n) {
		t.Error("nil slice should not contain anything")
	}
	if sliceContainsNode([]graph.Node{}, n) {
		t.Error("empty slice should not contain anything")
	}
}

func TestSliceContainsNodeDistinctValues(t *testing.T) {
	g := graph.New(graph.Directed)
	n1 := g.MakeNode()
	*n1.Value = 1
	n2 := g.MakeNode()
	*n2.Value = 2
	n3 := g.MakeNode()
	*n3.Value = 3

	slice := []graph.Node{n1, n2}
	if sliceContainsNode(slice, n3) {
		t.Error("should not find n3 with value 3")
	}
}

// TestVisitedMapSemantics tests that a map[interface{}]bool replacement for the
// visited slice produces identical results to sliceContainsNode. This validates
// the Phase 1 optimization approach.
func TestVisitedMapSemantics(t *testing.T) {
	g := graph.New(graph.Directed)

	nodes := make([]graph.Node, 100)
	for i := 0; i < 100; i++ {
		nodes[i] = g.MakeNode()
		*nodes[i].Value = i
	}

	// Simulate BFS visited tracking with both approaches
	sliceVisited := []graph.Node{}
	mapVisited := make(map[interface{}]bool)

	// Add every other node
	for i := 0; i < 100; i += 2 {
		sliceVisited = append(sliceVisited, nodes[i])
		mapVisited[*nodes[i].Value] = true
	}

	// Verify both produce same results for all nodes
	for i := 0; i < 100; i++ {
		sliceResult := sliceContainsNode(sliceVisited, nodes[i])
		mapResult := mapVisited[*nodes[i].Value]
		if sliceResult != mapResult {
			t.Errorf("node %d: slice says %v, map says %v", i, sliceResult, mapResult)
		}
	}
}

// TestVisitedMapWithPointerValues tests map behavior when node values are
// pointers (a likely real-world scenario given goIns contains interface types).
func TestVisitedMapWithPointerValues(t *testing.T) {
	g := graph.New(graph.Directed)

	type testKey struct {
		id int
	}
	k1 := &testKey{1}
	k2 := &testKey{2}
	k3 := &testKey{1} // same field values as k1, different pointer

	n1 := g.MakeNode()
	*n1.Value = k1
	n2 := g.MakeNode()
	*n2.Value = k2
	n3 := g.MakeNode()
	*n3.Value = k3

	visited := make(map[interface{}]bool)
	visited[*n1.Value] = true

	// Pointer identity: k1 != k3 even though both have id=1
	if visited[*n3.Value] {
		t.Error("different pointer with same fields should NOT match in map (pointer identity)")
	}
	if !visited[*n1.Value] {
		t.Error("same pointer should match")
	}
	if visited[*n2.Value] {
		t.Error("k2 should not be in visited")
	}
}

// ============================================================================
// Phase 1: BFS Reachability Tests (Graph Structure)
// ============================================================================

// TestBFSLinearChain tests reachability in a simple linear graph: A -> B -> C -> D
func TestBFSLinearChain(t *testing.T) {
	g := graph.New(graph.Directed)
	a := g.MakeNode()
	*a.Value = "A"
	b := g.MakeNode()
	*b.Value = "B"
	c := g.MakeNode()
	*c.Value = "C"
	d := g.MakeNode()
	*d.Value = "D"

	g.MakeEdge(a, b)
	g.MakeEdge(b, c)
	g.MakeEdge(c, d)

	// Forward reachability
	if !bfsReachable(g, a, d) {
		t.Error("A should reach D")
	}
	if !bfsReachable(g, a, b) {
		t.Error("A should reach B")
	}
	if !bfsReachable(g, b, d) {
		t.Error("B should reach D")
	}

	// Not reachable in reverse direction (directed graph)
	if bfsReachable(g, d, a) {
		t.Error("D should NOT reach A in directed graph")
	}
	if bfsReachable(g, c, a) {
		t.Error("C should NOT reach A")
	}

	// Self-reachability: with our BFS impl, from==to is immediately found when dequeued.
	// This is expected behavior — a node trivially reaches itself.
	if !bfsReachable(g, a, a) {
		t.Error("A should reach itself (trivial self-reachability)")
	}
}

// TestBFSDisconnectedGraph tests that unreachable nodes are correctly identified
func TestBFSDisconnectedGraph(t *testing.T) {
	g := graph.New(graph.Directed)
	a := g.MakeNode()
	*a.Value = "A"
	b := g.MakeNode()
	*b.Value = "B"
	c := g.MakeNode()
	*c.Value = "C"
	d := g.MakeNode()
	*d.Value = "D"

	// Two disconnected components: A -> B, C -> D
	g.MakeEdge(a, b)
	g.MakeEdge(c, d)

	if bfsReachable(g, a, c) {
		t.Error("A should NOT reach C (disconnected)")
	}
	if bfsReachable(g, a, d) {
		t.Error("A should NOT reach D (disconnected)")
	}
	if bfsReachable(g, b, c) {
		t.Error("B should NOT reach C")
	}
}

// TestBFSCycleHandling tests that BFS doesn't infinite-loop on cycles
func TestBFSCycleHandling(t *testing.T) {
	g := graph.New(graph.Directed)
	a := g.MakeNode()
	*a.Value = "A"
	b := g.MakeNode()
	*b.Value = "B"
	c := g.MakeNode()
	*c.Value = "C"

	// Cycle: A -> B -> C -> A
	g.MakeEdge(a, b)
	g.MakeEdge(b, c)
	g.MakeEdge(c, a)

	// Should complete without hanging
	if !bfsReachable(g, a, c) {
		t.Error("A should reach C")
	}
	if !bfsReachable(g, b, a) {
		t.Error("B should reach A (via cycle)")
	}
	// Self-reachability: from==to is found immediately when first node is dequeued
	if !bfsReachable(g, a, a) {
		t.Error("A should reach itself (trivial self-reachability)")
	}
}

// TestBFSSelfLoop tests a node with an edge to itself
func TestBFSSelfLoop(t *testing.T) {
	g := graph.New(graph.Directed)
	a := g.MakeNode()
	*a.Value = "A"
	b := g.MakeNode()
	*b.Value = "B"

	g.MakeEdge(a, a) // self-loop
	g.MakeEdge(a, b)

	// BFS should still work, not hang
	if !bfsReachable(g, a, b) {
		t.Error("A should reach B despite self-loop")
	}
}

// TestBFSDenseGraph tests BFS on a nearly-complete graph (edge case from plan section 4.7)
func TestBFSDenseGraph(t *testing.T) {
	g := graph.New(graph.Directed)
	const N = 50
	nodes := make([]graph.Node, N)
	for i := 0; i < N; i++ {
		nodes[i] = g.MakeNode()
		*nodes[i].Value = i
	}

	// Create nearly complete graph (every node connects to every later node)
	for i := 0; i < N; i++ {
		for j := i + 1; j < N; j++ {
			g.MakeEdge(nodes[i], nodes[j])
		}
	}

	// First node should reach last
	if !bfsReachable(g, nodes[0], nodes[N-1]) {
		t.Error("node 0 should reach node N-1 in dense graph")
	}
	// Last should not reach first (DAG)
	if bfsReachable(g, nodes[N-1], nodes[0]) {
		t.Error("node N-1 should NOT reach node 0 (DAG)")
	}
}

// TestBFSDiamondGraph tests BFS on a diamond-shaped graph with multiple paths
func TestBFSDiamondGraph(t *testing.T) {
	g := graph.New(graph.Directed)
	top := g.MakeNode()
	*top.Value = "top"
	left := g.MakeNode()
	*left.Value = "left"
	right := g.MakeNode()
	*right.Value = "right"
	bottom := g.MakeNode()
	*bottom.Value = "bottom"

	g.MakeEdge(top, left)
	g.MakeEdge(top, right)
	g.MakeEdge(left, bottom)
	g.MakeEdge(right, bottom)

	if !bfsReachable(g, top, bottom) {
		t.Error("top should reach bottom via either path")
	}
	if bfsReachable(g, left, right) {
		t.Error("left should NOT reach right")
	}
	if bfsReachable(g, bottom, top) {
		t.Error("bottom should NOT reach top")
	}
}

// TestBFSEmptyGraph tests BFS when graph has nodes but no edges
func TestBFSEmptyGraph(t *testing.T) {
	g := graph.New(graph.Directed)
	a := g.MakeNode()
	*a.Value = "A"
	b := g.MakeNode()
	*b.Value = "B"

	if bfsReachable(g, a, b) {
		t.Error("no edges means no reachability")
	}
}

// ============================================================================
// Phase 2: Reachability Cache Tests
// ============================================================================

// TestCacheKeySymmetry ensures that (A->B) and (B->A) are cached separately
func TestCacheKeySymmetry(t *testing.T) {
	cache := make(map[[4]interface{}]bool)

	key1 := [4]interface{}{"insA", 0, "insB", 1}
	key2 := [4]interface{}{"insB", 1, "insA", 0}

	cache[key1] = true
	cache[key2] = false

	if cache[key1] != true {
		t.Error("A->B should be true")
	}
	if cache[key2] != false {
		t.Error("B->A should be false")
	}
}

// TestCacheKeyDistinctGoIDs ensures same instructions in different goroutines
// produce distinct cache keys
func TestCacheKeyDistinctGoIDs(t *testing.T) {
	cache := make(map[[4]interface{}]bool)

	key1 := [4]interface{}{"ins1", 0, "ins2", 1}
	key2 := [4]interface{}{"ins1", 0, "ins2", 2} // different toGo
	key3 := [4]interface{}{"ins1", 3, "ins2", 1} // different fromGo

	cache[key1] = true
	cache[key2] = false
	cache[key3] = false

	if cache[key1] != true {
		t.Error("key1 should be true")
	}
	if cache[key2] != false {
		t.Error("key2 should be false")
	}
	if cache[key3] != false {
		t.Error("key3 should be false")
	}
}

// TestCacheHitRate simulates repeated queries to verify caching benefit
func TestCacheHitRate(t *testing.T) {
	cache := make(map[[4]interface{}]bool)
	hits := 0
	misses := 0

	// Simulate 100 queries with some repeats
	queries := make([][4]interface{}, 0, 200)
	for i := 0; i < 10; i++ {
		for j := 0; j < 10; j++ {
			q := [4]interface{}{i, 0, j, 1}
			queries = append(queries, q)
			queries = append(queries, q) // duplicate query
		}
	}

	for _, q := range queries {
		if _, ok := cache[q]; ok {
			hits++
		} else {
			misses++
			cache[q] = true // simulate BFS result
		}
	}

	if hits != 100 {
		t.Errorf("expected 100 cache hits, got %d", hits)
	}
	if misses != 100 {
		t.Errorf("expected 100 cache misses, got %d", misses)
	}
}

// TestCacheSizeGrowth checks that cache size is bounded by actual queries
func TestCacheSizeGrowth(t *testing.T) {
	cache := make(map[[4]interface{}]bool)
	const numGoroutines = 20
	const numIns = 50

	// Simulate worst case: every pair queried
	for fromGo := 0; fromGo < numGoroutines; fromGo++ {
		for toGo := fromGo + 1; toGo < numGoroutines; toGo++ {
			for fromIns := 0; fromIns < numIns; fromIns++ {
				for toIns := 0; toIns < numIns; toIns++ {
					key := [4]interface{}{fromIns, fromGo, toIns, toGo}
					cache[key] = false
				}
			}
		}
	}

	expectedMax := numGoroutines * (numGoroutines - 1) / 2 * numIns * numIns
	if len(cache) > expectedMax {
		t.Errorf("cache size %d exceeds expected max %d", len(cache), expectedMax)
	}
}

// ============================================================================
// Phase 3: mutuallyExcluded() Path Building Tests
// ============================================================================

// TestReverseSlice tests that the append-then-reverse approach produces
// the same result as the current prepend approach.
func TestReverseSlice(t *testing.T) {
	// Simulate the prepend approach (current code)
	prependResult := []int{}
	chain := []int{5, 3, 1, 0} // goCaller chain ending at 0
	for _, id := range chain {
		if id == 0 {
			break
		}
		prependResult = append([]int{id}, prependResult...)
	}

	// Simulate the append-then-reverse approach (optimized)
	appendResult := []int{}
	for _, id := range chain {
		if id == 0 {
			break
		}
		appendResult = append(appendResult, id)
	}
	// Reverse in-place
	for l, r := 0, len(appendResult)-1; l < r; l, r = l+1, r-1 {
		appendResult[l], appendResult[r] = appendResult[r], appendResult[l]
	}

	if len(prependResult) != len(appendResult) {
		t.Fatalf("length mismatch: prepend=%d, append=%d", len(prependResult), len(appendResult))
	}
	for i := range prependResult {
		if prependResult[i] != appendResult[i] {
			t.Errorf("mismatch at %d: prepend=%d, append=%d", i, prependResult[i], appendResult[i])
		}
	}
}

// TestReverseEmptySlice tests edge case of empty path
func TestReverseEmptySlice(t *testing.T) {
	result := []int{}
	for l, r := 0, len(result)-1; l < r; l, r = l+1, r-1 {
		result[l], result[r] = result[r], result[l]
	}
	if len(result) != 0 {
		t.Error("empty slice should remain empty after reverse")
	}
}

// TestReverseSingleElement tests path with single goroutine
func TestReverseSingleElement(t *testing.T) {
	result := []int{42}
	for l, r := 0, len(result)-1; l < r; l, r = l+1, r-1 {
		result[l], result[r] = result[r], result[l]
	}
	if len(result) != 1 || result[0] != 42 {
		t.Error("single element slice should remain unchanged")
	}
}

// TestPrependVsAppendReverseDeepChain tests with a deep goroutine call chain
func TestPrependVsAppendReverseDeepChain(t *testing.T) {
	// Simulate goCaller chain: 100 -> 99 -> 98 -> ... -> 1 -> 0
	goCaller := make(map[int]int)
	for i := 1; i <= 100; i++ {
		goCaller[i] = i - 1
	}

	// Prepend approach
	prependPath := []int{}
	id := 100
	for id > 0 {
		prependPath = append([]int{id}, prependPath...)
		id = goCaller[id]
	}

	// Append-reverse approach
	appendPath := []int{}
	id = 100
	for id > 0 {
		appendPath = append(appendPath, id)
		id = goCaller[id]
	}
	for l, r := 0, len(appendPath)-1; l < r; l, r = l+1, r-1 {
		appendPath[l], appendPath[r] = appendPath[r], appendPath[l]
	}

	if len(prependPath) != len(appendPath) {
		t.Fatalf("length mismatch: %d vs %d", len(prependPath), len(appendPath))
	}
	for i := range prependPath {
		if prependPath[i] != appendPath[i] {
			t.Errorf("index %d: prepend=%d, append=%d", i, prependPath[i], appendPath[i])
		}
	}

	// Verify order is root -> leaf (1, 2, 3, ..., 100)
	for i, v := range prependPath {
		if v != i+1 {
			t.Errorf("expected path[%d]=%d, got %d", i, i+1, v)
		}
	}
}

// ============================================================================
// Phase 4: Condition Reorder Tests
// ============================================================================

// TestShortCircuitEvaluation verifies that reordering doesn't change final result.
// We simulate the condition chain with boolean functions tracking call order.
func TestShortCircuitEvaluation(t *testing.T) {
	tests := []struct {
		name     string
		sameAddr bool
		strict   bool // !strictSliceContains
		atomic   bool // !bothAtomic
		selectME bool // !selectMutEx
		lockSet  bool // !lockSetsIntersect
		reach1   bool // !reachable(i->j)
		reach2   bool // !reachable(j->i)
		mutExcl  bool // !mutuallyExcluded
		isRace   bool
	}{
		{
			name: "all pass - is race",
			sameAddr: true, strict: true, atomic: true, selectME: true,
			lockSet: true, reach1: true, reach2: true, mutExcl: true,
			isRace: true,
		},
		{
			name: "different address - not race",
			sameAddr: false,
			isRace:   false,
		},
		{
			name: "duplicate - not race",
			sameAddr: true, strict: false,
			isRace: false,
		},
		{
			name: "both atomic - not race",
			sameAddr: true, strict: true, atomic: false,
			isRace: false,
		},
		{
			name: "select mutual exclusion - not race",
			sameAddr: true, strict: true, atomic: true, selectME: false,
			isRace: false,
		},
		{
			name: "lock sets intersect - not race",
			sameAddr: true, strict: true, atomic: true, selectME: true, lockSet: false,
			isRace: false,
		},
		{
			name: "reachable i->j - not race",
			sameAddr: true, strict: true, atomic: true, selectME: true,
			lockSet: true, reach1: false,
			isRace: false,
		},
		{
			name: "reachable j->i - not race",
			sameAddr: true, strict: true, atomic: true, selectME: true,
			lockSet: true, reach1: true, reach2: false,
			isRace: false,
		},
		{
			name: "mutually excluded - not race",
			sameAddr: true, strict: true, atomic: true, selectME: true,
			lockSet: true, reach1: true, reach2: true, mutExcl: false,
			isRace: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Original order (from plan)
			originalResult := tc.sameAddr && tc.strict && tc.reach1 && tc.reach2 &&
				tc.atomic && tc.lockSet && tc.selectME && tc.mutExcl

			// Optimized order (from plan)
			optimizedResult := tc.sameAddr && tc.strict && tc.atomic && tc.selectME &&
				tc.lockSet && tc.reach1 && tc.reach2 && tc.mutExcl

			if originalResult != optimizedResult {
				t.Errorf("reorder changed result: original=%v, optimized=%v", originalResult, optimizedResult)
			}
			if originalResult != tc.isRace {
				t.Errorf("expected isRace=%v, got %v", tc.isRace, originalResult)
			}
		})
	}
}

// ============================================================================
// Phase 5: sliceContains* Function Tests
// ============================================================================

func TestSliceContainsStr(t *testing.T) {
	tests := []struct {
		slice    []string
		elem     string
		expected bool
	}{
		{nil, "x", false},
		{[]string{}, "x", false},
		{[]string{"a"}, "a", true},
		{[]string{"a"}, "b", false},
		{[]string{"a", "b", "c"}, "b", true},
		{[]string{"a", "b", "c"}, "d", false},
		{[]string{"", "b"}, "", true},
		{[]string{"abc", "def"}, "ab", false}, // no partial match
	}

	for _, tc := range tests {
		got := sliceContainsStr(tc.slice, tc.elem)
		if got != tc.expected {
			t.Errorf("sliceContainsStr(%v, %q) = %v, want %v", tc.slice, tc.elem, got, tc.expected)
		}
	}
}

func TestSliceContainsStrCtr(t *testing.T) {
	tests := []struct {
		slice    []string
		elem     string
		expected int
	}{
		{nil, "x", 0},
		{[]string{"a", "a", "a"}, "a", 3},
		{[]string{"a", "b", "a"}, "a", 2},
		{[]string{"a", "b", "c"}, "d", 0},
		{[]string{"a"}, "a", 1},
	}

	for _, tc := range tests {
		got := sliceContainsStrCtr(tc.slice, tc.elem)
		if got != tc.expected {
			t.Errorf("sliceContainsStrCtr(%v, %q) = %v, want %v", tc.slice, tc.elem, got, tc.expected)
		}
	}
}

// ============================================================================
// Edge Case: Nil Node Handling (Plan section 4.4)
// ============================================================================

// TestRWinsMapMissingKey validates behavior when RWinsMap doesn't contain a key.
// The plan proposes adding ok1/ok2 guards. This test documents current zero-value behavior.
func TestRWinsMapMissingKey(t *testing.T) {
	m := make(map[string]graph.Node)

	// Accessing non-existent key returns zero-value graph.Node
	zeroNode := m["nonexistent"]

	// Zero-value node should be distinguishable
	g := graph.New(graph.Directed)
	realNode := g.MakeNode()
	*realNode.Value = "real"

	// The zero-value node has nil Value
	if zeroNode.Value != nil {
		t.Log("zero-value graph.Node has non-nil Value field")
	}

	// After optimization: the guard should prevent BFS with nil nodes
	_, ok := m["nonexistent"]
	if ok {
		t.Error("missing key should return ok=false")
	}
}

// ============================================================================
// Benchmark: BFS with slice vs map visited set
// ============================================================================

func BenchmarkBFSSliceVisited(b *testing.B) {
	g := graph.New(graph.Directed)
	const N = 500
	nodes := make([]graph.Node, N)
	for i := 0; i < N; i++ {
		nodes[i] = g.MakeNode()
		*nodes[i].Value = i
	}
	// Create a chain with some branches
	for i := 0; i < N-1; i++ {
		g.MakeEdge(nodes[i], nodes[i+1])
		if i+2 < N {
			g.MakeEdge(nodes[i], nodes[i+2])
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Use slice-based visited (current approach)
		var visited []graph.Node
		q := &queue{}
		q.enQueue(nodes[0])
		for !q.isEmpty() {
			node := q.deQueue()
			if node == nodes[N-1] {
				break
			}
			for _, neighbor := range g.Neighbors(node) {
				if sliceContainsNode(visited, neighbor) {
					continue
				}
				visited = append(visited, neighbor)
				q.enQueue(neighbor)
			}
		}
	}
}

func BenchmarkBFSMapVisited(b *testing.B) {
	g := graph.New(graph.Directed)
	const N = 500
	nodes := make([]graph.Node, N)
	for i := 0; i < N; i++ {
		nodes[i] = g.MakeNode()
		*nodes[i].Value = i
	}
	for i := 0; i < N-1; i++ {
		g.MakeEdge(nodes[i], nodes[i+1])
		if i+2 < N {
			g.MakeEdge(nodes[i], nodes[i+2])
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Use map-based visited (optimized approach)
		visited := make(map[interface{}]bool)
		q := &queue{}
		q.enQueue(nodes[0])
		visited[*nodes[0].Value] = true
		for !q.isEmpty() {
			node := q.deQueue()
			if node == nodes[N-1] {
				break
			}
			for _, neighbor := range g.Neighbors(node) {
				if visited[*neighbor.Value] {
					continue
				}
				visited[*neighbor.Value] = true
				q.enQueue(neighbor)
			}
		}
	}
}

// BenchmarkMutuallyExcludedPathBuilding benchmarks prepend vs append-reverse
func BenchmarkPrependPath(b *testing.B) {
	goCaller := make(map[int]int)
	for i := 1; i <= 200; i++ {
		goCaller[i] = i - 1
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path := []int{}
		id := 200
		for id > 0 {
			path = append([]int{id}, path...)
			id = goCaller[id]
		}
	}
}

func BenchmarkAppendReversePath(b *testing.B) {
	goCaller := make(map[int]int)
	for i := 1; i <= 200; i++ {
		goCaller[i] = i - 1
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path := []int{}
		id := 200
		for id > 0 {
			path = append(path, id)
			id = goCaller[id]
		}
		for l, r := 0, len(path)-1; l < r; l, r = l+1, r-1 {
			path[l], path[r] = path[r], path[l]
		}
	}
}

// ============================================================================
// Helper: standalone BFS for testing graph reachability without full analysis
// ============================================================================

// bfsReachable performs a standalone BFS on a graph to test reachability
// without requiring the full analysis struct.
func bfsReachable(g *graph.Graph, from, to graph.Node) bool {
	visited := make(map[interface{}]bool)
	q := &queue{}
	q.enQueue(from)
	visited[*from.Value] = true // Phase 1 fix: add fromNode to visited

	for !q.isEmpty() {
		for size := q.size(); size > 0; size-- {
			node := q.deQueue()
			if node == to {
				return true
			}
			for _, neighbor := range g.Neighbors(node) {
				if visited[*neighbor.Value] {
					continue
				}
				visited[*neighbor.Value] = true
				q.enQueue(neighbor)
			}
		}
	}
	return false
}
