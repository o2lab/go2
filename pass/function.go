package pass

import (
	"github.com/o2lab/go2/go/callgraph"
	"github.com/o2lab/go2/go/ssa"
	"github.com/o2lab/go2/pointer"
	"github.com/o2lab/go2/preprocessor"
	log "github.com/sirupsen/logrus"
	"go/token"
	"go/types"
)

type FnPass struct {
	summary                   preprocessor.FnSummary
	acquiredPointSet          pointer.AccessPointSet
	releasedPointSet          pointer.AccessPointSet
	escapedBorrowedPointSet   pointer.AccessPointSet
	borrowedPointSet          pointer.AccessPointSet
	accessPointSet            pointer.AccessPointSet
	forwardStack              CallStack
	escapedValues             []ssa.Value
	Visitor                   *CFGVisitor
	accessMeta                map[int][]*Access
	seenAccessInstrs          map[ssa.Instruction]bool
	acquireMap                map[ssa.Instruction]*pointer.AccessPointSet // set of acquired points per instruction
	accessInstrs              map[ssa.Instruction]bool
	accessPointSetByCallInstr map[ssa.Instruction]*pointer.AccessPointSet
	releasedPerIns            [][]*pointer.AccessPointSet // filled by backwardsDataflowAnalysis
}

func NewFnPass(visitor *CFGVisitor, summary preprocessor.FnSummary, stack CallStack) *FnPass {
	return &FnPass{
		Visitor:                   visitor,
		summary:                   summary,
		forwardStack:              stack.Copy(),
		seenAccessInstrs:          make(map[ssa.Instruction]bool),
		accessInstrs:              make(map[ssa.Instruction]bool),
		acquireMap:                make(map[ssa.Instruction]*pointer.AccessPointSet),
		accessPointSetByCallInstr: make(map[ssa.Instruction]*pointer.AccessPointSet),
	}
}

func (pass *FnPass) GetPointer(value ssa.Value) pointer.Pointer {
	return pass.Visitor.ptaResult.Queries[value]
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

func (pass *FnPass) extendPointSetIfStruct(ps *pointer.AccessPointSet, v ssa.Value) []int {
	var space [4]int
	res := ps.AppendTo(space[:0])
	if named, ok := v.Type().Underlying().(*types.Pointer).Elem().(*types.Named); ok {
		if t, ok := named.Underlying().(*types.Struct); ok {
			n := t.NumFields()
			for _, p := range res {
				for i := 0; i < n; i++ {
					res = append(res, p+i+1)
				}
			}
		}
	}
	return res
}

func (pass *FnPass) backwardsDataflowAnalysis(function *ssa.Function) {
	size := len(function.Blocks)
	releasedPerIns := make([][]*pointer.AccessPointSet, size)

	worklist := make([]int, size)
	for i := 0; i < size; i++ {
		worklist[i] = size - i - 1 // reverse order
		releasedPerIns[i] = make([]*pointer.AccessPointSet, len(function.Blocks[i].Instrs))
	}

	for len(worklist) > 0 {
		blockIdx := worklist[0]
		worklist = worklist[1:]
		block := function.Blocks[blockIdx]

		if block.Index != blockIdx {
			log.Fatalln("assert fail")
		}

		var releasedOut pointer.AccessPointSet
		succs := block.Succs
		if len(succs) > 0 {
			if in := releasedPerIns[succs[0].Index][0]; in != nil {
				releasedOut.Copy(&in.Sparse)
			}
		}
		for i := 1; i < len(succs); i++ {
			if in := releasedPerIns[succs[i].Index][0]; in != nil {
				releasedOut.Intersects(&in.Sparse)
			}
		}

		if pass.updateBlockStateOnRelease(block, releasedPerIns[block.Index], &releasedOut) {
			for _, pred := range block.Preds {
				worklist = appendIfNotPresent(worklist, pred.Index)
			}
		}
	}
	pass.releasedPerIns = releasedPerIns
}

func appendIfNotPresent(worklist []int, x int) []int {
	idx := 0
	for ; idx < len(worklist); idx++ {
		if worklist[idx] == x {
			break
		}
	}
	if idx == len(worklist) {
		worklist = append(worklist, x)
	}
	return worklist
}

// release ops beside lock: chan send/recv, waitgroup wait/done/add
func (pass *FnPass) updateBlockStateOnRelease(block *ssa.BasicBlock, relMap []*pointer.AccessPointSet, releasedOut *pointer.AccessPointSet) bool {
	prevAcq := &pointer.AccessPointSet{}
	var oldRelIn pointer.AccessPointSet
	if relMap[0] != nil {
		oldRelIn.Copy(&relMap[0].Sparse)
	}

	handleCall := func(i int, instr ssa.CallInstruction) {
		//	TODO: waitgroup wait/done/add
		if mutex := preprocessor.GetLockedMutex(instr.Common()); mutex != nil {
			if ps := pass.valueToPointSet(mutex); ps != nil {
				releasedOut.DifferenceWith(&ps.Sparse)
				prevAcq = &pointer.AccessPointSet{}
				prevAcq.Copy(&releasedOut.Sparse)
			}
		} else if mutex := preprocessor.GetUnlockedMutex(instr.Common()); mutex != nil {
			if ps := pass.valueToPointSet(mutex); ps != nil {
				releasedOut.UnionWith(&ps.Sparse)
				prevAcq = &pointer.AccessPointSet{}
				prevAcq.Copy(&releasedOut.Sparse)
			}
		} else if ch := preprocessor.GetClosedChan(instr.Common()); ch != nil {
			if ps := pass.valueToPointSet(ch); ps != nil {
				releasedOut.UnionWith(&ps.Sparse)
				prevAcq = &pointer.AccessPointSet{}
				prevAcq.Copy(&releasedOut.Sparse)
			}
		} else {
			for _, edge := range pass.Visitor.instrSiteMap[instr] {
				if calleeSummary := pass.Visitor.passes[edge.Callee.Func]; calleeSummary != nil {
					if !calleeSummary.releasedPointSet.IsEmpty() {
						releasedOut.UnionWith(&calleeSummary.releasedPointSet.Sparse)
						prevAcq = &pointer.AccessPointSet{}
						prevAcq.Copy(&releasedOut.Sparse)
					}
				}
			}
		}
	}

	for i := len(block.Instrs) - 1; i >= 0; i-- { // reverse
		switch instr := block.Instrs[i].(type) {
		case preprocessor.SyntheticDeferred:
			handleCall(i, instr)
		case *ssa.Call:
			handleCall(i, instr)
		case *ssa.Send:
			if ps := pass.valueToPointSet(instr.Chan); ps != nil {
				releasedOut.UnionWith(&ps.Sparse)
				prevAcq = &pointer.AccessPointSet{}
				prevAcq.Copy(&releasedOut.Sparse)
			}
		case *ssa.UnOp:
			if instr.Op == token.ARROW { // channel receive
				if ps := pass.valueToPointSet(instr.X); ps != nil {
					releasedOut.UnionWith(&ps.Sparse)
					prevAcq = &pointer.AccessPointSet{}
					prevAcq.Copy(&releasedOut.Sparse)
				}
			}
		}
		relMap[i] = prevAcq
	}

	return !oldRelIn.Equals(&releasedOut.Sparse)
}

func (pass *FnPass) forwardDataflowAnalysis(function *ssa.Function) {
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
		blockIdx := worklist[0]
		worklist = worklist[1:]
		block := function.Blocks[blockIdx]

		if block.Index != blockIdx {
			log.Fatalln("assert fail")
		}

		// acqIn is the intersection of outs for each predecessor.
		var acqIn pointer.AccessPointSet
		if len(block.Preds) > 0 {
			acqIn.Copy(&acquiredOuts[block.Preds[0].Index].Sparse)
		}
		for idx := 1; idx < len(block.Preds); idx++ {
			predIdx := block.Preds[idx].Index
			acqIn.IntersectionWith(&acquiredOuts[predIdx].Sparse)
		}

		// escIn is the union of escapedOuts for each predecessor.
		var escIn pointer.AccessPointSet
		for _, pred := range block.Preds {
			escIn.UnionWith(&escapedOuts[pred.Index].Sparse)
		}

		if pass.updateBlockState(block, &acqIn, &escIn, acquiredOuts[blockIdx], pass.releasedPerIns[blockIdx], escapedOuts[blockIdx], accessMap, &pass.accessPointSet) {
			for _, succ := range block.Succs {
				worklist = appendIfNotPresent(worklist, succ.Index)
			}
		}
	}

	pass.accessMeta = accessMap
}

func (pass *FnPass) updateBlockState(block *ssa.BasicBlock, acqIn *pointer.AccessPointSet, escIn *pointer.AccessPointSet,
	acqOut *pointer.AccessPointSet, relMap []*pointer.AccessPointSet, escOut *pointer.AccessPointSet,
	accessMap map[int][]*Access, accessPointSet *pointer.AccessPointSet) bool {
	log.Debugf("Block %d: %s", block.Index, block.Comment)

	handleCall := func(i int, instr ssa.CallInstruction) {
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
					pass.applyCalleeSummary(calleePass, false, acqIn, relMap[i], escIn, accessMap, edge)

					acqIn.UnionWith(&calleePass.acquiredPointSet.Sparse)
				}
			}
		}
	}

	for i, instruction := range block.Instrs {
		log.Debugf("  %s pos=%v", instruction, instruction.Pos())
		switch instr := instruction.(type) {
		case preprocessor.SyntheticDeferred:
			handleCall(i, instr)
		case *ssa.Call:
			handleCall(i, instr)
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
					pass.applyCalleeSummary(calleePass, true, acqIn, relMap[i], escIn, accessMap, edge)
				}
			}

		case *ssa.UnOp:
			if instr.Op == token.MUL {
				pass.updateStateViaIndirection(instr, instr.X, escIn)
				pass.makeAccess(instr, instr.X, false, acqIn, relMap[i], escIn, accessMap, accessPointSet)
			} else if instr.Op == token.ARROW {
				if ps := pass.valueToPointSet(instr.X); ps != nil {
					acqIn.UnionWith(&ps.Sparse)
				}
			}
		case *ssa.Send:
			// TODO: only unbuffered sends should acquire. Here we temporarily do acquire for all sends.
			if ps := pass.valueToPointSet(instr.Chan); ps != nil {
				acqIn.UnionWith(&ps.Sparse)
			}
		case *ssa.Store:
			pass.makeAccess(instr, instr.Addr, true, acqIn, relMap[i], escIn, accessMap, accessPointSet)
		case *ssa.IndexAddr:
			pass.updateStateViaIndirection(instr, instr.X, escIn)
		case *ssa.FieldAddr:
			pass.updateStateViaIndirection(instr, instr.X, escIn)
		case *ssa.Select:
			t := instr.Type()
			_ = t
			for _, state := range instr.States {
				log.Infoln(state.DebugNode)
			}
		}
	}
	acqFixed := acqIn.Equals(&acqOut.Sparse)
	escFixed := escIn.Equals(&escOut.Sparse)
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
	log.Debug("   => borrowed: ", pass.borrowedPointSet.AppendTo([]int{}), " escaped: ", escIn.AppendTo([]int{}))
}

func (pass *FnPass) applyCalleeSummary(calleePass *FnPass, crossThread bool, acqIn *pointer.AccessPointSet, rel *pointer.AccessPointSet,
	escIn *pointer.AccessPointSet, accessMap map[int][]*Access, edge *callgraph.Edge) {
	for p, accesses := range calleePass.accessMeta {
		for _, acc := range accesses {
			accNew := &Access{
				Instr:       acc.Instr,
				Write:       acc.Write,
				Addr:        acc.Addr,
				Pred:        acc,
				PredSite:    edge,
				CrossThread: crossThread || acc.CrossThread,
			}

			if !accNew.CrossThread {
				accNew.AcquiredPointSet.Union(&acc.AcquiredPointSet.Sparse, &acqIn.Sparse)
				accNew.ReleasedPointSet.Union(&acc.ReleasedPointSet.Sparse, &rel.Sparse)
			} else {
				accNew.AcquiredPointSet.Copy(&acc.AcquiredPointSet.Sparse)
				accNew.ReleasedPointSet.Copy(&acc.ReleasedPointSet.Sparse)
			}

			escaped := pass.isAddrEscaped(acc.Addr, pass.valueToPointSet(acc.Addr), escIn)
			nonlocal := accNew.CrossThread || pass.isAddrNonLocal1(acc.Addr)

			log.Debugf("   => ACCESS callee esc=%t nonlocal=%t %s", escaped, nonlocal, accNew)

			// Check races if the access is bound to the current thread.
			stored := false // store once
			for i, accCur := range accessMap[p] {
				if (!accNew.CrossThread || accCur.CrossThread) && escaped && accCur.RacesWith(accNew) {
					pass.ReportRace(accCur, accNew)
				}
				if !stored && accNew.Subsumes(accCur) {
					accessMap[p][i] = accNew
					stored = true
				}
			}

			// Merge the access into caller's pass.
			if !stored && (escaped || nonlocal) {
				pass.accessPointSet.Insert(p)
				accessMap[p] = append(accessMap[p], accNew)
			}
		}
	}
}

func (pass *FnPass) makeAccess(instr ssa.Instruction, addr ssa.Value, write bool, acqIn *pointer.AccessPointSet,
	rel *pointer.AccessPointSet, escIn *pointer.AccessPointSet, accessMap map[int][]*Access, accessPointSet *pointer.AccessPointSet) {
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
		acc.ReleasedPointSet.Copy(&rel.Sparse)

		// Check races on the subset of escaped values.
		points := pass.extendPointSetIfStruct(ps, addr)
		escaped := pass.isAddrEscaped(addr, ps, escIn)
		nonlocal := pass.isAddrNonLocal(addr, ps)
		log.Debugf("   => ACCESS esc=%t nonlocal=%t %s", escaped, nonlocal, acc)

		for _, p := range points {
			stored := false
			for accIdx, acc1 := range accessMap[p] {
				if escaped && acc.RacesWith(acc1) {
					pass.ReportRace(acc, acc1)
				}
				if !stored && acc.Subsumes(acc1) {
					accessMap[p][accIdx] = acc
					stored = true
				}
			}
			// Store access metadata if its address is non-local or escaped.
			if !stored && (escaped || nonlocal) {
				accessMap[p] = append(accessMap[p], acc)
			}
		}

		if escaped && nonlocal {
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
