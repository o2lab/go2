package main

import "C"
import (
	"go/token"
	"golang.org/x/tools/go/ssa"
	"strings"
)

func insToCallStack(allIns []ssa.Instruction) ([]string, string) {
	var callStack []string
	var csStr string
	for _, anIns := range allIns {
		if fnCall, ok := anIns.(*ssa.Call); ok {
			callStack = append(callStack, fnCall.Call.Value.Name())
		} else if _, ok1 := anIns.(*ssa.Return); ok1 && len(callStack) > 0 {
			callStack = callStack[:len(callStack)-1]
		}
	}
	csStr = strings.Join(callStack, "...")
	return callStack, csStr
}

func sliceContains(s []ssa.Value, e ssa.Value) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}

func sliceContainsBloc(s []*ssa.BasicBlock, e *ssa.BasicBlock) bool {
	for _, a := range s {
		if a.Comment == e.Comment {
			return true
		}
	}
	return false
}

func sliceContainsStr(s []string, e string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}

func sliceContainsInsAt(s []ssa.Instruction, e ssa.Instruction) int {
	for i := 0; i < len(s); i++ {
		if s[i] == e {
			return i
		}
	}
	return -1
}

func deleteFromLockSet(s []ssa.Value, k int) []ssa.Value {
	var res []ssa.Value
	res = append(s[:k], s[k+1:]...)
	return res
}

func (a *analysis) lockSetContainsAt(s []ssa.Value, e ssa.Value) int {
	var aPos, bPos token.Pos
	for i, k := range s {
		switch aType := k.(type) {
		case *ssa.Global:
			aPos = aType.Pos()
		case *ssa.FieldAddr:
			if aType1, ok1 := aType.X.(*ssa.UnOp); ok1 {
				aPos = aType1.X.Pos()
			} else {
				aPos = aType.X.Pos()
			}
		}
		switch eType := e.(type) {
		case *ssa.Global:
			bPos = eType.Pos()
		case *ssa.FieldAddr:
			if eType1, ok2 := eType.X.(*ssa.UnOp); ok2 {
				bPos = eType1.X.Pos()
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
