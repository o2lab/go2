package sparsesets

import (
	"bytes"
)

// bz: implementation-specific setting
const (
	//ExpansionFactor = 1.5                //how much do we expand if array length is not enough
	MaxInt          = int(^uint(0) >> 1) //max value in sparse array
	MinInt = -MaxInt - 1
)

// bz: this is a sparse set implementation according to https://www.geeksforgeeks.org/sparse-set/
// in order to replace /containner/intsets/sparse.go (since it does deep copy, which limits the performance)
// for smooth replacement, this will have the same api as intset.Sparse, also will borrow the size of fields
//
// TODO: (1) not sure about this performance, will test
//       (2) from Jeff: "im not expecting len(pts) > 1000, ... ", ... so let's set ptsLimit when using this?
//       (3) do we use ExpansionFactor or double linked-list like block in intset.Sparse?
//          ExpansionFactor -> deep copy requires a lot of time ...
//          double linked-list -> estimate the size of block/array
//       => check console-grpc-dist.txt, console-ethereum-dist.txt (callback), console-kubernetes-dist.txt (callback):
//       did a statistic survey on the mains of these benchmarks:
//       1. 96% has <10 obj in a pts;
//       2. 92% has idx < 5000 as the min obj idx;
//       3. 90% has < 10 distance between max and min obj idx
//       similar trends in other benchmarks
//       so, use linked-list (not double), 1st block will have maxVal == 6000 -> this can cover the majority cases
//       if needs to expend, expend by 1000
type SparseDense struct {
	root *block //bz: why not use pointer ?
}

//bz: len(dense) == len(sparse)
type block struct {
	dense  []int // Stores the actual elements; according to nodeid, use at least 32bytes -> int or int32?
	sparse []int // This is like bit-vector where we use elements as index. Here
	//values are not binary, but indexes of dense array.
	maxCapacity int    // Maximum value this set can store. Size of sparse[]/dense[] is equal to maxVal + 1.
	n           int    // Current number of elements in Set.
	next        *block // next block, can be nil
}

//Let x be the element to be inserted. If x is greater than maxVal or n (current number of elements) is greater than equal to capacity, we return.
//If none of the above conditions is true, we insert x in dense[] at index n (position after last element in a 0 based indexed array),
//increment n by one (Current number of elements) and store n (index of x in dense[]) at sparse[x].
func (b *block) insert(x int) bool {
	if x > b.maxCapacity || b.n >= b.maxCapacity {
		// we need a new block
		return false
	}
	if b.has(x) {
		return false
	}

	b.dense[b.n] = x
	b.n++
	b.sparse[x] = b.n
	return true
}

//To delete an element x, we replace it with last element in dense[]
//  and update index of last element in sparse[].
//	Finally decrement n by 1. -> bz: will not use this now
func (b *block) remove(x int) bool {
	if !b.has(x) {
		return false
	}
	//example:
	//n        = 3
	//dense[]  = {3, 5, 7, _}
	//sparse[] = {_, _, _, 0, _, 1, _, 2, _, _,}
	// -> remove 3
	//n        = 2
	//dense[]  = {7, 5, _, _}
	//sparse[] = {_, _, _, _, _, 1, _, 0, _, _,}

	idx := b.sparse[x] //idx of x in dense
	lastIdx := b.n - 1 //idx of last in dense
	valX := b.dense[idx]
	valLast := b.dense[lastIdx]

	b.dense[idx] = b.dense[lastIdx] //replace
	b.sparse[valX + 1] = 0 //reset
	b.sparse[valLast + 1] = idx //update

	b.n--
	return true
}

//To search an element x, we use x as index in sparse[]. The value sparse[x] is used as index in dense[].
//	And if value of dense[sparse[x]] is equal to x, we return dense[x]. Else we return -1.
func (b *block) has(x int) bool {
	idx := b.sparse[x] //default initial element value is 0
	if b.dense[idx] == x {
		return true
	}
	return false
}

//bz: the last element.  bad efficiency?
func (b *block) max() int {
	for i := len(b.sparse) - 1; i >= 0; i-- {
		idx := b.sparse[i]
		max := b.dense[idx]
		if max != 0 {
			return max
		}
	}
	panic("BUG: empty block")
}

//bz: the first element.  bad efficiency?
func (b *block) min() int {
	for i := 0; i < len(b.sparse); i++ {
		idx := b.sparse[i]
		min := b.dense[idx]
		if min != 0 {
			return min
		}
	}
	panic("BUG: empty block")
}

func (b *block) empty() bool {
	return b.n == 0
}

func (b *block) len() int {
	return b.n
}


// -- SparseDense --------------------------------------------------------------

var none block //bz: borrow idea from intset.Sparse

func (s *SparseDense) init() {
	if s.root == nil { //initialize
		s.root = &block{
			dense:       make([]int, 6000),
			sparse:      make([]int, 6000),
			maxCapacity: 6000,
			n:           0,
			next:        nil,
		}
	}

	root := s.root
	if root.next == nil {
		root.next = root
	}
}

func (s *SparseDense) first() *block {
	s.init()
	if s.root.n == 0 {
		return &none
	}
	return s.root
}

func (s *SparseDense) next(b *block) *block {
	if b.next == s.root {
		return &none
	}
	return b.next
}

//bz: the last block
func (s *SparseDense) last() *block {
	var last *block
	for b := s.first(); b != &none; b = s.next(b) {
		last = b
	}
	return last
}

// removeBlock removes a block and returns the block that followed it (or end if
// it was the last block).
// TODO: bz: implement
func (s *SparseDense) removeBlock(b *block) *block {
	return s.root
}

func (s *SparseDense) IsEmpty() bool {
	return s.root.n == 0 || s.root.next == nil
}

func (s *SparseDense) Len() int {
	var l int
	for b := s.first(); b != &none; b = s.next(b) {
		l += b.len()
	}
	return l
}

// Max returns the maximum element of the set s, or MinInt if s is empty.
func (s *SparseDense) Max() int {
	if s.IsEmpty() {
		return MinInt
	}
	return s.last().max()
}

// Min returns the minimum element of the set s, or MaxInt if s is empty.
func (s *SparseDense) Min() int {
	if s.IsEmpty() {
		return MaxInt
	}
	return s.root.min()
}

// Insert adds x to the set s, and reports whether the set grew.
//bz: for most cases, s.root is enough
func (s *SparseDense) Insert(x int) bool {
	if x < s.root.maxCapacity {
		return s.root.insert(x)
	}

	//TODO: bz: create new block(s) or we ignore them ...

	return false
}

// Remove removes x from the set s, and reports whether the set shrank.
//bz: for most cases, s.root is enough
func (s *SparseDense) Remove(x int) bool {
	if x < s.root.maxCapacity {
		return s.root.remove(x)
	}

	//TODO: bz: tmp we ignore them ...

	return false
}

func (s *SparseDense) Clear() {
	s.root = &block{
		dense:       nil,
		sparse:      nil,
		maxCapacity: s.root.maxCapacity,
		n:           0,
		next:        nil,
	}
}

//bz: see @container/intsets/sparse.go:416
func (s *SparseDense) TakeMin(p *int) bool {
	if s.IsEmpty() {
		return false
	}
	*p = s.root.min()
	if s.root.empty() {
		s.removeBlock(s.root)
	}
	return true
}

// Has reports whether x is an element of the set s.
func (s *SparseDense) Has(x int) bool {
	if x < s.root.maxCapacity {
		return s.root.has(x)
	}

	//TODO: bz: tmp we ignore them ...

	return false
}

// Copy sets s to the value of x. s -> x
func (s *SparseDense) Copy(x *SparseDense) {
	if s == x {
		return
	}


}

// UnionWith sets s to the union s ∪ x, and reports whether s grew.
func (s *SparseDense) UnionWith(x *SparseDense) bool {
	return true
}

func (s *SparseDense) String() string {
	var buf bytes.Buffer
	buf.WriteRune('{')
	//for i := range s.dense {
	//	if i >= 0 {
	//		buf.WriteString(", ")
	//	}
	//	buf.WriteRune('n')
	//	fmt.Fprintf(&buf, "%d", s.dense[i])
	//}
	buf.WriteRune('}')
	return buf.String()
}
