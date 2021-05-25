
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

//bz: strict version of sliceContains
func strictSliceContains(races []*raceInfo, curAddrs [2]ssa.Value) bool {
	for _, race := range races {
		exist := race.addrPair
		if exist[0] == curAddrs[0] && exist[1] == curAddrs[1] {
			return true
		}
		if exist[0] == curAddrs[1] && exist[1] == curAddrs[0] {
			return true
		}
	}
	return false
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

//bz: problem in insCall (@gorace/ssaInstructions.go:339)
// for a call with parameters, the a.recordIns will add the call ir inst to a.RWIns for parameter reads first
// later the exploredFunction will call sliceContainsInsInfoAt and return that the call has been traversed,
// but actually not, because it is a record for parameter read, not call
// -> if this is a parameter read, its stack should not include e as the last element (since e is pushed afterwards);
//    otherwise, this is a duplicate call
func sliceContainsInsInfoAt(s []*insInfo, e ssa.Instruction) int {
	for i := 0; i < len(s); i++ {
		r := s[i]
		if r.ins == e {
			idx := len(r.stack) - 1
			if idx < 0 {
				continue
			}
			if r.stack[idx].ssaIns != e {
				continue
			}
			return i
		}
	}
	return -1
}

func sliceContainsNode(slice []graph.Node, node graph.Node) bool {
	for _, n := range slice {
		if n.Value == node.Value {
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
