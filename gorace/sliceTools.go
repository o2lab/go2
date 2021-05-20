package main

import (
	"github.com/april1989/origin-go-tools/go/ssa"
	"github.com/twmb/algoimpl/go/graph"
	"go/token"
)

// insToCallStack will return the callStack of the input instructions that called a function but did not return
func insToCallStack(allIns []*insInfo) ([]*ssa.Function, string) {
	var callStack []*ssa.Function
	for _, anIns := range allIns {
		if fnCall, ok := anIns.ins.(*ssa.Call); ok {
			callStack = append(callStack, fnCall.Call.StaticCallee())
		} else if _, ok1 := anIns.ins.(*ssa.Return); ok1 && len(callStack) > 0 { // TODO: need to consider function with multiple return statements
			callStack = callStack[:len(callStack)-1]
		}
	}
	csStr := ""
	for _, fn := range callStack { // combine into one string because slices are not comparable
		if fn != nil {
			csStr += fn.Name()
		}
	}
	return callStack, csStr
}

// sliceContains if the e value is present in the slice, s, of ssa values that true, and false otherwise
// bz: update: needs to match both ssa.Value and same goids (may fron another possible goroutine/path)
func sliceContains(a *analysis, races []*raceInfo, curAddrs [2]ssa.Value, goID1, goID2 int) bool {
	_, goID1twin := a.getMyGoTwin(goID1)
	_, goID2twin := a.getMyGoTwin(goID2)
	for _, race := range races {
		exist := race.addrPair
		existIDs := race.goIDs

		if exist[0] == curAddrs[0] && exist[1] == curAddrs[1] {
			if (existIDs[0] == goID1 && existIDs[1] == goID2) ||
				//bz: loop id also count as the same
				(existIDs[0] == goID1twin && existIDs[1] == goID2) ||
				(existIDs[0] == goID1 && existIDs[1] == goID2twin) ||
				(existIDs[0] == goID1twin && existIDs[1] == goID2twin) {
				return true
			}
		}
		if exist[0] == curAddrs[1] && exist[1] == curAddrs[0] {
			if (existIDs[1] == goID1 && existIDs[0] == goID2) ||
				//bz: loop id also count as the same
				(existIDs[1] == goID1twin && existIDs[0] == goID2) ||
				(existIDs[1] == goID1 && existIDs[0] == goID2twin) ||
				(existIDs[1] == goID1twin && existIDs[0] == goID2twin) {
				return true
			}
		}
	}
	return false
}

func stackContainsDefer(stack []*fnCallInfo) bool {
	for _, eachIns := range stack {
		if _, isDefer := eachIns.ssaIns.(*ssa.Defer); isDefer {
			return true
		}
	}
	return false
}

func noGoAfterFn(stack []*fnCallInfo, divFnAt int) bool {
	for i := divFnAt; i < len(stack); i++ {
		if stack[i].ssaIns != nil && i != 0 {
			if _, isGo := stack[i].ssaIns.(*ssa.Go); isGo {
				return false
			}
		}
	}
	return true
}

func sliceContainsFnCall(s []*fnCallInfo, e fnCallInfo) bool {
	for _, a := range s {
		if a.fnIns.Pos() == e.fnIns.Pos() && a.ssaIns.Pos() == e.ssaIns.Pos() {
			return true
		}
	}
	return false
}

//bz: i guess your intended to check whether e is other tests ... update .
func sliceContainsFnCtr(s []*ssa.Function, e *ssa.Function) int {
	ctr := 0
	if e != nil {
		for _, a := range s {
			if a != nil && a.Pos() == e.Pos() {
				ctr++
			}
		}
	}
	return ctr
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

func sliceContainsBlocAt(s []*ssa.BasicBlock, e *ssa.BasicBlock) int {
	for i, b := range s {
		if b == e {
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

func sliceContainsFreeVar(s []*ssa.FreeVar, e *ssa.FreeVar) bool {
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

func sliceContainsInsInfoAt(s []*insInfo, e ssa.Instruction) int {
	for i := 0; i < len(s); i++ {
		if s[i].ins == e {
			return i
		}
	}
	return -1
}

// lockSetContainsAt returns the index of e in s
func (a *analysis) lockSetContainsAt(s map[int][]*lockInfo, e ssa.Value, goID int) int {
	var aPos, bPos token.Pos
	for i, k := range s[goID] {
		var locA, locB ssa.Value
		switch aType := k.locAddr.(type) {
		case *ssa.Global:
			aPos = aType.Pos()
		case *ssa.FieldAddr:
			if aType1, ok1 := aType.X.(*ssa.UnOp); ok1 {
				if aType2, ok2 := aType1.X.(*ssa.FieldAddr); ok2 {
					aPos = aType2.X.Pos()
					locA = aType2.X
				} else {
					aPos = aType1.X.Pos()
					locA = aType1.X
				}
			} else {
				aPos = aType.X.Pos()
				locA = aType.X
			}
		}
		switch eType := e.(type) {
		case *ssa.Global:
			bPos = eType.Pos()
		case *ssa.FieldAddr:
			if eType1, ok2 := eType.X.(*ssa.UnOp); ok2 {
				if eType2, ok3 := eType1.X.(*ssa.FieldAddr); ok3 {
					bPos = eType2.X.Pos()
					locB = eType2.X
				} else {
					bPos = eType1.X.Pos()
					locB = eType1.X
				}
			} else {
				bPos = eType.X.Pos()
				locB = eType.X
			}
		}
		if aPos == bPos {
			return i
		}
		if a.sameAddress(locA, locB, goID, goID) {
			return i
		}
	}
	return -1
}

// getRcvChan returns channel name of receive Op
func (a *analysis) getRcvChan(ins *ssa.UnOp) string {
	for ch, rIns := range a.chanRcvs {
		if sliceContainsRcv(rIns, ins) { // channel receive
			return ch
		}
	}
	return ""
}

func (a *analysis) getSndChan(ins *ssa.Send) string {
	for ch, sIns := range a.chanSnds {
		if sliceContainsSnd(sIns, ins) { // channel receive
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

func lockSetVal(s map[int][]*lockInfo, goID int) []token.Pos {
	res := make([]token.Pos, 0)
	for _, ls := range s[goID] {
		res = append(res, ls.locAddr.Pos())
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