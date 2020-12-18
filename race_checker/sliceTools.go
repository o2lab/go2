package main

import (
	"github.com/twmb/algoimpl/go/graph"
	"github.tamu.edu/April1989/go_tools/go/ssa"
	"go/token"
	"strings"
)

// insToCallStack will return the callStack of the input instructions that called a function but did not return
func insToCallStack(allIns []ssa.Instruction) ([]string, string) {
	var callStack []string
	var csStr string
	for _, anIns := range allIns {
		if fnCall, ok := anIns.(*ssa.Call); ok {
			callStack = append(callStack, fnCall.Call.Value.Name())
		} else if _, ok1 := anIns.(*ssa.Return); ok1 && len(callStack) > 0 { // TODO: need to consider function with multiple return statements
			callStack = callStack[:len(callStack)-1]
		}
	}
	csStr = strings.Join(callStack, "...") // combine into one string because slices are not comparable
	return callStack, csStr
}

// sliceContains if the e value is present in the slice, s, of ssa values that true, and false otherwise
func sliceContains(s []ssa.Value, e ssa.Value) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}

func sliceContainsRcv(s []*ssa.UnOp, e *ssa.UnOp) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}

func sliceContainsSnd(s []*ssa.Send, e *ssa.Send) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}

// sliceContainsBloc determines if BasicBlock e is in s
func sliceContainsBloc(s []*ssa.BasicBlock, e *ssa.BasicBlock) bool {
	for _, b := range s {
		if b == e {
			return true
		}
	}
	return false
}


func sliceContainsIntAt(s []int, e int) int {
	for i, a := range s {
		if a == e {
			return i
		}
	}
	return -1
}

// sliceContainsStr will determine if slice s has an element e of type string
func sliceContainsStr(s []string, e string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}

// sliceContainsStrCtr will determine how many times e is in s
func sliceContainsStrCtr(s []string, e string) int {
	counter := 0
	for _, a := range s {
		if a == e {
			counter++
		}
	}
	return counter
}

// sliceContainsInsAt finds the index of e in instruction slice s
func sliceContainsInsAt(s []ssa.Instruction, e ssa.Instruction) int {
	for i := 0; i < len(s); i++ {
		if s[i] == e {
			return i
		}
	}
	return -1
}

// deleteFromLockSet removes k from s
func (a *analysis) deleteFromLockSet(s []ssa.Value, k int) []ssa.Value {
	var res []ssa.Value
	res = append(s[:k], s[k+1:]...)
	return res
}

// lockSetContainsAt returns the index of e in s
func (a *analysis) lockSetContainsAt(s []ssa.Value, e ssa.Value) int {
	var aPos, bPos token.Pos
	for i, k := range s {
		switch aType := k.(type) {
		case *ssa.Global:
			aPos = aType.Pos()
		case *ssa.FieldAddr:
			if aType1, ok1 := aType.X.(*ssa.UnOp); ok1 {
				if aType2, ok2 := aType1.X.(*ssa.FieldAddr); ok2 {
					aPos = aType2.X.Pos()
				} else {
					aPos = aType1.X.Pos()
				}
			} else {
				aPos = aType.X.Pos()
			}
		}
		switch eType := e.(type) {
		case *ssa.Global:
			bPos = eType.Pos()
		case *ssa.FieldAddr:
			if eType1, ok2 := eType.X.(*ssa.UnOp); ok2 {
				if eType2, ok3 := eType1.X.(*ssa.FieldAddr); ok3 {
					bPos = eType2.X.Pos()
				} else {
					bPos = eType1.X.Pos()
				}
			} else {
				bPos = eType.X.Pos()
			}
		}
		if aPos == bPos {
			return i
		}
	}
	return -1
}

// getRcvChan returns channel name of receive Op
func (a *analysis) getRcvChan(ins *ssa.UnOp) string {
	for ch, rIns := range Analysis.chanRcvs {
		if sliceContainsRcv(rIns, ins) { // channel receive
			return ch
		}
	}
	return ""
}

// isReadySel returns whether or not the channel is awaited on (and ready) by a select statement
func (a *analysis) isReadySel(ch string) bool {
	for _, chStr := range a.selReady {
		if sliceContainsStr(chStr, ch) {
			return true
		}
	}
	return false
}

func lockSetVal (s []ssa.Value) []token.Pos {
	res := make([]token.Pos, len(s))
	for i, val := range s {
		res[i] = val.Pos()
	}
	return res
}

// self-defined queue for traversing Happens-Before Graph
type queue struct {
	data []graph.Node
}

func (q *queue) enQueue(v graph.Node) {
	q.data = append(q.data, v)
}
func (q *queue) deQueue() graph.Node {
	v := q.data[0]
	q.data = q.data[1:]
	return v
}
func (q *queue) isEmpty() bool {
	return len(q.data) == 0
}
func (q *queue) size() int {
	return len(q.data)
}