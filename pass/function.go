package pass

import (
	"fmt"
	"github.com/o2lab/go2/pointer"
	"github.com/o2lab/go2/preprocessor"
	log "github.com/sirupsen/logrus"
	"go/token"
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
	stack                     CallStack
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
	PredSite         ssa.Instruction
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
		stack:                     stack.Copy(),
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
				fun := GetFunctionFromCallCommon(instr.Common())
				if calleePass, ok := pass.Visitor.passes[fun]; ok && fun != nil {
					pass.applyCalleeSummary(calleePass, instr, false, acqIn, escIn, accessMap)

					acqIn.UnionWith(&calleePass.acquiredPointSet.Sparse)
					escIn.UnionWith(&calleePass.escapedBorrowedPointSet.Sparse)
				}
			}
		case *ssa.Go:
			if fun := GetFunctionFromCallCommon(instr.Common()); fun != nil {
				// Capture escaped values.
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
					pass.applyCalleeSummary(calleePass, instr, true, acqIn, escIn, accessMap)
				}
			}
		case *ssa.UnOp:
			if instr.Op == token.MUL {
				pass.makeAccess(instr, instr.X, false, acqIn, escIn, accessMap, accessPointSet)
			}
		case *ssa.Store:
			pass.makeAccess(instr, instr.Addr, true, acqIn, escIn, accessMap, accessPointSet)
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


func (pass *FnPass) applyCalleeSummary(calleePass *FnPass, site ssa.Instruction, crossThread bool, acqIn *pointer.AccessPointSet,
	escIn *pointer.AccessPointSet, accessMap map[int][]*Access) {
	if escIn.Intersects(&calleePass.accessPointSet.Sparse) {
		for p, accesses := range calleePass.accessMeta {
			for _, acc := range accesses {
				accNew := &Access{
					Instr:            acc.Instr,
					Write:            acc.Write,
					Addr:             acc.Addr,
					Pred:             acc,
					PredSite:         site,
					CrossThread:      crossThread || acc.CrossThread,
				}

				// Merge the acquired point sets if the access is bound to the current thread.
				if !accNew.CrossThread {
					accNew.AcquiredPointSet.Union(&acc.AcquiredPointSet.Sparse, &acqIn.Sparse)
				}

				for _, accCur := range accessMap[p] {
					if accCur.RacesWith(accNew) {
						pass.ReportRace(accCur, accNew)
					}
				}
			}
		}
	}
}

//
//func (pass *FnPass) computeEscapedAccessPoints(function *ssa.Function) []map[pointer.AccessPointId]int {
//	escapedPerBlock := make([]map[pointer.AccessPointId]int, len(function.Blocks))
//	for i := 0; i < len(function.Blocks); i++ {
//		escapedPerBlock[i] = make(map[pointer.AccessPointId]int) // maps point to the index of the call/go instruction where the point escapes
//	}
//
//	for blockIdx, block := range function.Blocks {
//		for instrIdx, instruction := range block.Instrs {
//			switch instr := instruction.(type) {
//			case *ssa.Defer:
//			//	TODO
//			case *ssa.Call:
//				if fun := GetFunctionFromCallCommon(instr.Common()); fun != nil {
//					if calleePass, ok := pass.Visitor.passes[fun]; ok {
//						for pts, _ := range calleePass.escapedBorrowedPointSet {
//							escapedPerBlock[blockIdx][pts] = instrIdx
//						}
//					}
//				}
//			case *ssa.Go:
//				if fun := GetFunctionFromCallCommon(instr.Common()); fun != nil {
//					for _, v := range fun.Params {
//						for _, p := range pass.valueToPointSlice(v) {
//							escapedPerBlock[blockIdx][p] = instrIdx
//						}
//					}
//					for _, v := range fun.FreeVars {
//						for _, p := range pass.valueToPointSlice(v) {
//							escapedPerBlock[blockIdx][p] = instrIdx
//						}
//					}
//				}
//			}
//		}
//	}
//
//	// Compute transitive closure for escaped points of each block.
//	// Exclude the entry block initially, whose Preds is nil.
//	worklist := make([]int, len(function.Blocks)-1)
//	for i := 1; i < len(function.Blocks); i++ {
//		worklist[i-1] = i
//	}
//	for len(worklist) > 0 {
//		idx := worklist[0]
//		worklist = worklist[1:]
//		block := function.Blocks[idx]
//		changed := false
//		for _, pred := range block.Preds {
//			for pt, _ := range escapedPerBlock[pred.Index] {
//				if _, ok := escapedPerBlock[idx][pt]; !ok {
//					changed = true
//				}
//				// If pt is already seen by block idx, overwrite its index to 0.
//				escapedPerBlock[idx][pt] = 0
//			}
//		}
//		if changed {
//			for _, succ := range block.Succs {
//				worklist = append(worklist, succ.Index)
//			}
//		}
//	}
//	return escapedPerBlock
////}
//
//func (pass *FnPass) setEscapedPointsByValue(v ssa.Value, escapedToCallee map[pointer.AccessPointId]bool, instrIdx int, target map[pointer.AccessPointId]int) {
//	for _, p := range pass.Visitor.ptaResult.Queries[v].AccessPointSet().ToSlice() {
//		if escapedToCallee[p] {
//			target[p] = instrIdx
//		}
//	}
//}

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

		// Check races on any escaped values.
		var intersection pointer.AccessPointSet
		var space [8]int
		intersection.Intersection(&escIn.Sparse, &ps.Sparse)
		if !intersection.IsEmpty() {
			for _, p := range intersection.Sparse.AppendTo(space[:0]) {
				for _, acc1 := range accessMap[p] {
					if acc.RacesWith(acc1) {
						pass.ReportRace(acc, acc1)
					}
				}
			}
		}

		// Store access metadata.
		for _, p := range ps.Sparse.AppendTo(space[:0]) {
			accessMap[p] = append(accessMap[p], acc)
		}
		accessPointSet.UnionWith(&ps.Sparse)
	}
}

func (a *Access) RacesWith(b *Access) bool {
	if !a.Write && !b.Write {
		return false
	}
	if a.AcquiredPointSet.Intersects(&b.AcquiredPointSet.Sparse) {
		return false
	}
	if !b.CrossThread {
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
	PrintStack(pass.stack)
	log.Printf("  %s", a2.StringWithPos(fset))
	log.Println("  Call stack:")
	PrintStack(a2.Stack)
	log.Println("===============================")
}