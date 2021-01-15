package pass

import (
	"fmt"
	"github.com/o2lab/go2/pointer"
	"github.com/o2lab/go2/preprocessor"
	log "github.com/sirupsen/logrus"
	"go/token"
	"go/types"
	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/ssa"
)

type FnPass struct {
	//BlockStates               map[int]*BlockState
	thread                    ThreadDomain
	summary                   preprocessor.FnSummary
	acquiredPointSet          pointer.AccessPointSet
	escapedBorrowedPointSet   pointer.AccessPointSet
	borrowedPointSet          pointer.AccessPointSet
	accessPointSet            pointer.AccessPointSet
	fowardStack               CallStack
	escapedValues             []ssa.Value
	Visitor                   *CFGVisitor
	accessMeta                map[int][]*Access
	seenAccessInstrs          map[ssa.Instruction]bool
	acquireMap                map[ssa.Instruction]*pointer.AccessPointSet // set of acquired points per instruction
	accessInstrs              map[ssa.Instruction]bool
	accessPointSetByCallInstr map[ssa.Instruction]*pointer.AccessPointSet
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
	Thread         ThreadDomain
	Stack          CallStack
	CrossThread    bool
}

type RefState int

const (
	Owned RefState = iota
	Shared
	Inherent
)

type ThreadDomain struct {
	ID        int
	Reflexive bool
}

func (d ThreadDomain) String() string {
	if d.Reflexive {
		return fmt.Sprintf("%d#", d.ID)
	}
	return fmt.Sprintf("%d", d.ID)
}

func NewFnPass(visitor *CFGVisitor, domain ThreadDomain, summary preprocessor.FnSummary, stack CallStack) *FnPass {
	return &FnPass{
		//BlockStates:               make(map[int]*BlockState),
		Visitor:                   visitor,
		thread:                    domain,
		summary:                   summary,
		fowardStack:               stack.Copy(),
		seenAccessInstrs:          make(map[ssa.Instruction]bool),
		accessInstrs:              make(map[ssa.Instruction]bool),
		acquireMap:                make(map[ssa.Instruction]*pointer.AccessPointSet),
		accessPointSetByCallInstr: make(map[ssa.Instruction]*pointer.AccessPointSet),
	}
}

func (pass *FnPass) GetPointer(value ssa.Value) pointer.Pointer {
	return pass.Visitor.ptaResult.Queries[value]
}

func (pass *FnPass) ThreadDomain() ThreadDomain {
	return pass.thread
}

func (pass *FnPass) Position(pos token.Pos) token.Position {
	return pass.Visitor.program.Fset.Position(pos)
}

func (pass *FnPass) valueToPointSlice(v ssa.Value) []pointer.AccessPointId {
	if ps := pass.valueToPointSet(v); ps != nil {
		return ps.ToSlice()
	}
	return nil
}

func (pass *FnPass) valueToPointSet(v ssa.Value) *pointer.AccessPointSet {
	p := pass.Visitor.ptaResult.Queries[v]
	return p.AccessPointSet()
}

func (pass *FnPass) dataflowAnalysis(function *ssa.Function) {
	size := len(function.Blocks)
	acquiredOuts := make([]*pointer.AccessPointSet, size)
	escapedOuts := make([]*pointer.AccessPointSet, size)
	accessMap := make(map[int][]*Access)

	worklist := make([]int, size)
	for i := 0; i < size; i++ {
		worklist[i] = i
		acquiredOuts[i] = &pointer.AccessPointSet{}
		escapedOuts[i] = &pointer.AccessPointSet{}
	}

	for len(worklist) > 0 {
		targetIdx := worklist[0]
		worklist = worklist[1:]
		block := function.Blocks[targetIdx]

		// acqIn is the intersection of outs for each predecessor.
		var acqIn pointer.AccessPointSet
		if len(block.Preds) > 0 {
			acqIn.Copy(&acquiredOuts[block.Preds[0].Index].Sparse)
		}
		for idx := 1; idx < len(block.Preds); idx++ {
			predIdx := block.Preds[idx].Index
			acqIn.IntersectionWith(&acquiredOuts[predIdx].Sparse)
		}

		// escIn is the union of outs for each predecessor.
		var escIn pointer.AccessPointSet
		for _, pred := range block.Preds {
			escIn.UnionWith(&escapedOuts[pred.Index].Sparse)
		}

		if pass.updateBlockState(block, &acqIn, &escIn, acquiredOuts[targetIdx], escapedOuts[targetIdx], accessMap, &pass.accessPointSet) {
			for _, succ := range block.Succs {
				// Do not add the same index twice if it is already in the worklist.
				idx := 0
				for ; idx < len(worklist); idx++ {
					if worklist[idx] == succ.Index {
						break
					}
				}
				if idx == len(worklist) {
					worklist = append(worklist, succ.Index)
				}
			}
		}
	}

	pass.accessMeta = accessMap
}

func (pass *FnPass) updateBlockState(block *ssa.BasicBlock, acqIn *pointer.AccessPointSet, escIn *pointer.AccessPointSet,
	acqOut *pointer.AccessPointSet, escOut *pointer.AccessPointSet, accessMap map[int][]*Access, accessPointSet *pointer.AccessPointSet) bool {
	var acqInOld, escInOld pointer.AccessPointSet
	acqInOld.Copy(&acqIn.Sparse)
	escInOld.Copy(&escIn.Sparse)

	for _, instruction := range block.Instrs {
		log.Debugln(instruction)
		switch instr := instruction.(type) {
		case *ssa.Call:
			if mutex := preprocessor.GetLockedMutex(instr.Common()); mutex != nil {
				log.Debugf("Lock on %s", mutex)
				if ps := pass.valueToPointSet(mutex); ps != nil {
					acqIn.UnionWith(&ps.Sparse)
				}
			} else if mutex := preprocessor.GetUnlockedMutex(instr.Common()); mutex != nil {
				log.Debugf("Unlock on %s", mutex)
				if ps := pass.valueToPointSet(mutex); ps != nil {
					if !acqIn.Intersects(&ps.Sparse) {
						log.Warnf("Unlock of an unlocked mutex %s at %s", mutex, pass.Position(mutex.Pos()))
					}
					acqIn.DifferenceWith(&ps.Sparse)
				}
			} else {
				// Apply the callee's summary.
				for _, edge := range pass.Visitor.instrSiteMap[instr] {
					fun := edge.Callee.Func
					if calleePass, ok := pass.Visitor.passes[fun]; ok && fun != nil {
						escIn.UnionWith(&calleePass.escapedBorrowedPointSet.Sparse)
						pass.applyCalleeSummary(calleePass, instr, false, acqIn, escIn, accessMap, edge)

						acqIn.UnionWith(&calleePass.acquiredPointSet.Sparse)
					}
				}
			}
		case *ssa.Go:
			for _, edge := range pass.Visitor.instrSiteMap[instr] {
				fun := edge.Callee.Func
				for _, v := range fun.FreeVars {
					if ps := pass.valueToPointSet(v); ps != nil {
						escIn.UnionWith(&ps.Sparse)
					}
				}
				for _, v := range fun.Params {
					if ps := pass.valueToPointSet(v); ps != nil {
						escIn.UnionWith(&ps.Sparse)
					}
				}

				// Apply the callee's summary.
				if calleePass, ok := pass.Visitor.passes[fun]; ok {
					pass.applyCalleeSummary(calleePass, instr, true, acqIn, escIn, accessMap, edge)
				}
			}

		case *ssa.UnOp:
			if instr.Op == token.MUL {
				pass.updateStateViaIndirection(instr, instr.X, escIn)
				pass.makeAccess(instr, instr.X, false, acqIn, escIn, accessMap, accessPointSet)
			}
		case *ssa.Store:
			pass.makeAccess(instr, instr.Addr, true, acqIn, escIn, accessMap, accessPointSet)
		case *ssa.IndexAddr:
			pass.updateStateViaIndirection(instr, instr.X, escIn)
		case *ssa.FieldAddr:
			pass.updateStateViaIndirection(instr, instr.X, escIn)
		default:
			// Let instr acquire all points in acqIn.
			acqAP := pass.acquireMap[instr]
			if acqAP == nil {
				acqAP = &pointer.AccessPointSet{}
				pass.acquireMap[instr] = acqAP
			}
			acqAP.UnionWith(&acqIn.Sparse)
		}
	}
	acqFixed := acqOut.Equals(&acqInOld.Sparse)
	escFixed := escOut.Equals(&escInOld.Sparse)
	if acqFixed && escFixed {
		return false
	}
	if !acqFixed {
		acqOut.Copy(&acqIn.Sparse)
	}
	if !escFixed {
		escOut.Copy(&escIn.Sparse)
	}
	return true
}

func (pass *FnPass) updateStateViaIndirection(target ssa.Value, base ssa.Value, escIn *pointer.AccessPointSet) {
	targetPS := pass.valueToPointSet(target)
	basePS := pass.valueToPointSet(base)
	if targetPS != nil && basePS != nil {
		if escIn.Intersects(&basePS.Sparse) {
			escIn.UnionWith(&targetPS.Sparse)
		}
		if pass.borrowedPointSet.Intersects(&basePS.Sparse) {
			pass.borrowedPointSet.UnionWith(&targetPS.Sparse)
		}
	}
	log.Debug("borrowed: ", pass.borrowedPointSet.AppendTo([]int{}), "escaped: ", escIn.AppendTo([]int{}))
}

func (pass *FnPass) applyCalleeSummary(calleePass *FnPass, site ssa.Instruction, crossThread bool, acqIn *pointer.AccessPointSet,
	escIn *pointer.AccessPointSet, accessMap map[int][]*Access, edge *callgraph.Edge) {
	for p, accesses := range calleePass.accessMeta {
		for _, acc := range accesses {
			accNew := &Access{
				Instr:            acc.Instr,
				Write:            acc.Write,
				Addr:             acc.Addr,
				Pred:             acc,
				PredSite:         edge,
				CrossThread:      crossThread || acc.CrossThread,
			}

			if !accNew.CrossThread {
				accNew.AcquiredPointSet.Union(&acc.AcquiredPointSet.Sparse, &acqIn.Sparse)
			}

			escaped := pass.isAddrEscaped(acc.Addr, pass.valueToPointSet(acc.Addr), escIn)
			// Check races if the access is bound to the current thread.
			if !accNew.CrossThread && escaped {
				for _, accCur := range accessMap[p] {
					if accCur.RacesWith(accNew) {
						pass.ReportRace(accCur, accNew)
					}
				}
			}

			// Merge the access into caller's pass.
			if escaped || pass.isAddrNonLocal1(acc.Addr) || accNew.CrossThread {
				pass.accessPointSet.Insert(p)
				accessMap[p] = append(accessMap[p], accNew)
			}
		}
	}
}

func (pass *FnPass) makeAccess(instr ssa.Instruction, addr ssa.Value, write bool, acqIn *pointer.AccessPointSet,
	escIn *pointer.AccessPointSet, accessMap map[int][]*Access, accessPointSet *pointer.AccessPointSet) {
	if ps := pass.valueToPointSet(addr); ps != nil {
		acc := &Access{
			Instr:       instr,
			Write:       write,
			Addr:        addr,
			CrossThread: false,
			Pred:        nil,
			PredSite:    nil,
		}
		acc.AcquiredPointSet.Copy(&acqIn.Sparse)

		// Check races on the subset of escaped values.
		var intersection pointer.AccessPointSet
		var space [8]int
		escaped := pass.isAddrEscaped(addr, ps, escIn)
		if escaped {
			for _, p := range intersection.Sparse.AppendTo(space[:0]) {
				for _, acc1 := range accessMap[p] {
					if acc.RacesWith(acc1) {
						pass.ReportRace(acc, acc1)
					}
				}
			}
		}

		// Store access metadata if its address is non-local or escaped.
		nonlocal := pass.isAddrNonLocal(addr, ps)
		if escaped || nonlocal {
			for _, p := range ps.Sparse.AppendTo(space[:0]) {
				accessMap[p] = append(accessMap[p], acc)
			}
			accessPointSet.UnionWith(&ps.Sparse)
		}
		if nonlocal {
			pass.escapedBorrowedPointSet.UnionWith(&ps.Sparse)
		}
	}
}

func (pass *FnPass) isAddrNonLocal1(value ssa.Value) bool {
	if ps := pass.valueToPointSet(value); ps != nil {
		if pass.borrowedPointSet.Intersects(&ps.Sparse) {
			return true
		}
		if fieldAddrInstr, ok := value.(*ssa.FieldAddr); ok {
			return pass.isAddrNonLocal1(fieldAddrInstr.X)
		} else if ptr, ok := value.Type().Underlying().(*types.Pointer); ok {
			log.Debugln("ptr", ptr)
		}
	}
	return false
}

func (pass *FnPass) isAddrEscaped(value ssa.Value, ps *pointer.AccessPointSet, escIn *pointer.AccessPointSet) bool {
	if escIn.Intersects(&ps.Sparse) {
		return true
	}
	if fieldAddrInstr, ok := value.(*ssa.FieldAddr); ok {
		ps = pass.valueToPointSet(fieldAddrInstr.X)
		if ps != nil {
			return pass.isAddrEscaped(fieldAddrInstr.X, ps, escIn)
		}
	}
	return false
}

func (pass *FnPass) isAddrNonLocal(value ssa.Value, ps *pointer.AccessPointSet) bool {
	if pass.borrowedPointSet.Intersects(&ps.Sparse) {
		return true
	}
	if fieldAddrInstr, ok := value.(*ssa.FieldAddr); ok {
		ps = pass.valueToPointSet(fieldAddrInstr.X)
		if ps != nil {
			return pass.isAddrNonLocal(fieldAddrInstr.X, ps)
		}
	}
	return false
}

func (pass *FnPass) GetSSAValueByPointID(p int) ssa.Value {
	return pass.Visitor.aPointer.GetSSAValue(p)
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

func (a *Access) WriteAndThreadConflictsWith(b *Access) bool {
	return (a.Write || b.Write) && (a.Thread.ID != b.Thread.ID)
}

func (a *Access) MayAlias(b *Access, q map[ssa.Value]pointer.Pointer) bool {
	return q[a.Addr].MayAlias(q[b.Addr])
}

func (a *Access) String() string {
	if a.Write {
		return fmt.Sprintf("Write of %s from %s @T%s", a.Addr, a.Instr, a.Thread)
	}
	return fmt.Sprintf("Read of %s from %s @T%s", a.Addr, a.Instr, a.Thread)
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
		return fmt.Sprintf("Write of %s by T%s, Acquired: %+q, %s", a.Addr, a.Thread, a.AcquiredValues, fset.Position(a.Instr.Pos()))
	}
	return fmt.Sprintf("Read of %s by T%s, Acquired: %+q, %s", a.Addr, a.Thread, a.AcquiredValues, fset.Position(a.Instr.Pos()))
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
		log.Infof("    %-14s %v", signature, f.Prog.Fset.Position(pos))
	}
}

func (pass *FnPass) ReportRace(a1, a2 *Access) {
	fset := pass.Visitor.program.Fset
	log.Println("========== DATA RACE ==========")
	log.Printf("  %s", a1.StringWithPos(fset))
	log.Println("  Call stack:")
	PrintStack(a1.UnrollStack())
	PrintStack(pass.fowardStack)
	log.Printf("  %s", a2.StringWithPos(fset))
	log.Println("  Call stack:")
	PrintStack(a2.UnrollStack())
	PrintStack(pass.fowardStack)
	log.Println("===============================")
}