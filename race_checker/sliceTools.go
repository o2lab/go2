package main

import (
	"go/token"
	"golang.org/x/tools/go/ssa"
)

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

func sliceContainsIns(s []ssa.Instruction, e ssa.Instruction) bool {
	i := 0
	for i < len(s) {
		if s[i] == e {
			return true
		}
		i++
	}
	return false
}

func sliceContainsInsAt(s []ssa.Instruction, e ssa.Instruction) int {
	i := 0
	for s[i] != e {
		i++
	}
	return i
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
