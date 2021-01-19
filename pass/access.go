package pass

import (
	"fmt"
	"github.com/o2lab/go2/go/callgraph"
	"github.com/o2lab/go2/go/ssa"
	"github.com/o2lab/go2/pointer"
	"github.com/sirupsen/logrus"
	"go/token"
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
	ReleasedPointSet pointer.AccessPointSet
	CrossThread      bool
}

// Subsumes is a relation that orders accesses by their likeliness of being racy.
// a subsumes x if (x, b) being a race pair implies that (x, a) is a racy pair for some access b.
// Precondition: a and x must access the same address.
func (a *Access) Subsumes(x *Access) bool {
	if (!a.Write && x.Write) || (!a.CrossThread && x.CrossThread) {
		return false
	}
	return a.AcquiredPointSet.SubsetOf(&x.AcquiredPointSet.Sparse)
}

func (a *Access) RacesWith(b *Access) bool {
	if !a.Write && !b.Write {
		return false
	}
	if a.AcquiredPointSet.Intersects(&b.ReleasedPointSet.Sparse) || a.ReleasedPointSet.Intersects(&b.AcquiredPointSet.Sparse) {
		return false
	}
	if !a.CrossThread && !b.CrossThread {
		return false
	}
	return true
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
	return fmt.Sprintf("%s of %s, async=%t, acq=%+v, rel=%+v", typ, a.Addr, a.CrossThread, a.AcquiredPointSet.AppendTo([]int{}), a.ReleasedPointSet.AppendTo([]int{}))
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
	typ := "Read"
	if a.Write {
		typ = "Write"
	}
	return fmt.Sprintf("%s of %s, Acq/rel: %+v %+v cross=%t: %s", typ, a.Addr, a.AcquiredPointSet.AppendTo([]int{}), a.ReleasedPointSet.AppendTo([]int{}), a.CrossThread, fset.Position(a.Instr.Pos()))
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

	if pass.Visitor.testOutput != nil {
		p1 := fset.Position(a1.Instr.Pos())
		p2 := fset.Position(a2.Instr.Pos())
		pass.Visitor.testOutput[p1] = append(pass.Visitor.testOutput[p1], a1.String())
		pass.Visitor.testOutput[p2] = append(pass.Visitor.testOutput[p2], a2.String())
		return
	}

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
