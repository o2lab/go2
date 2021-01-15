package pass

import (
	"fmt"
	"github.com/o2lab/go2/pointer"
	"github.com/sirupsen/logrus"
	"go/token"
	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/ssa"
)

type CallStack []*callgraph.Edge

func (cs CallStack) Copy() CallStack {
	stack := make(CallStack, len(cs))
	copy(stack, cs)
	return stack
}

type Access struct {
	Instr            ssa.Instruction
	Write            bool
	Addr             ssa.Value
	Pred             *Access
	PredSite         *callgraph.Edge
	AcquiredPointSet pointer.AccessPointSet

	// TODO: remove these
	AccessPoints   *pointer.AccessPointSet
	AcquiredValues []ssa.Value
	Stack          CallStack
	CrossThread    bool
}

func (a *Access) RacesWith(b *Access) bool {
	if !a.Write && !b.Write {
		return false
	}
	if a.AcquiredPointSet.Intersects(&b.AcquiredPointSet.Sparse) {
		return false
	}
	if !a.CrossThread && !b.CrossThread {
		return false
	}
	return true
}

func (a *Access) MutualExclusive(b *Access, queries map[ssa.Value]pointer.Pointer) bool {
	for _, acq1 := range a.AcquiredValues {
		for _, acq2 := range b.AcquiredValues {
			if queries[acq1].MayAlias(queries[acq2]) {
				return true
			}
		}
	}
	return false
}

func (a *Access) WriteConflictsWith(b *Access) bool {
	return a.Write || b.Write
}


func (a *Access) MayAlias(b *Access, q map[ssa.Value]pointer.Pointer) bool {
	return q[a.Addr].MayAlias(q[b.Addr])
}

func (a *Access) String() string {
	typ := "Read"
	if a.Write {
		typ = "Write"
	}
	return fmt.Sprintf("%s of %s, async=%t, acq=%+q", typ, a.Addr, a.CrossThread, a.AcquiredPointSet.AppendTo([]int{}))
}

func (a *Access) UnrollStack() CallStack {
	var res CallStack
	cur := a
	for cur.Pred != nil {
		res = append(res, cur.PredSite)
		cur = cur.Pred
	}
	return res
}

func (a *Access) StringWithPos(fset *token.FileSet) string {
	if a.Write {
		return fmt.Sprintf("Write of %s, Acquired: %+q, %s", a.Addr, a.AcquiredPointSet.AppendTo([]int{}), fset.Position(a.Instr.Pos()))
	}
	return fmt.Sprintf("Read of %s, Acquired: %+q, %s", a.Addr, a.AcquiredPointSet.AppendTo([]int{}), fset.Position(a.Instr.Pos()))
}

func PrintStack(stack CallStack) {
	for i := len(stack) - 1; i >= 0; i-- {
		e := stack[i]
		f := e.Callee.Func
		var pos token.Pos
		if e.Site != nil {
			pos = e.Site.Pos()
		} else {
			pos = f.Pos()
		}
		signature := fmt.Sprintf("%s", f.Name())
		logrus.Infof("    %s %v", signature, f.Prog.Fset.Position(pos))
	}
}

func (pass *FnPass) ReportRace(a1, a2 *Access) {
	fset := pass.Visitor.program.Fset
	logrus.Println("========== DATA RACE ==========")
	logrus.Printf("  %s", a1.StringWithPos(fset))
	logrus.Println("  Call stack:")
	PrintStack(a1.UnrollStack())
	PrintStack(pass.forwardStack)
	logrus.Printf("  %s", a2.StringWithPos(fset))
	logrus.Println("  Call stack:")
	PrintStack(a2.UnrollStack())
	PrintStack(pass.forwardStack)
	logrus.Println("===============================")
}