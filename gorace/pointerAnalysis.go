package main

import (
	"fmt"
	"github.com/april1989/origin-go-tools/go/pointer"
	pta0 "github.com/april1989/origin-go-tools/go/pointer_default"
	"github.com/april1989/origin-go-tools/go/ssa"
	"go/types"
	"strconv"
	"strings"
)

//bz: i separate the original function, too long .... this code mainly used for default pta, otherwise direct to pointerNewAnalysis
func (a *analysis) pointerAnalysis(location ssa.Value, goID int, theIns ssa.Instruction) {
	switch locType := location.(type) {
	case *ssa.Parameter:
		if locType.Object().Pkg().Name() == "reflect" { // ignore reflect library
			return
		}
	}
	//indir := false // toggle for indirect query (global variables)
	if useDefaultPTA {
		if pointer.CanPoint(location.Type()) {
			a.mu.Lock()
			a.ptaCfg0.AddQuery(location)
			a.mu.Unlock()
		} else if underType, ok := location.Type().Underlying().(*types.Pointer); ok && pointer.CanPoint(underType.Elem()) {
			//indir = true
			a.ptaCfg0.AddIndirectQuery(location)
		}
	}

	var pta0Set map[ssa.Value]pta0.Pointer
	var PT0Set []*pta0.Label
	if useDefaultPTA {
		a.ptaRes0, _ = pta0.Analyze(a.ptaCfg0)
		pta0Set = a.ptaRes0.Queries
		PT0Set = pta0Set[location].PointsTo().Labels()

		var fnName string
		rightLoc := 0        // initialize index for the right points-to location
		if len(PT0Set) > 1 { // multiple targets returned by pointer analysis
			//log.Trace("***Pointer Analysis revealed ", len(PTSet), " targets for location - ", a.prog.Fset.Position(location.Pos()))
			//var fns []string
			//for ind, eachTarget := range PT0Set { // check each target
			//	if eachTarget.Value().Parent() != nil {
			//		fns = append(fns, eachTarget.Value().Parent().Name())
			//		//log.Trace("*****target No.", ind+1, " - ", eachTarget.Value().Name(), " from function ", eachTarget.Value().Parent().Name())
			//		targetFn := fnCallInfo{eachTarget.Value().Parent(), }
			//		if sliceContainsFn(a.storeFns, ) { // calling function is in current goroutine
			//			rightLoc = ind
			//			break
			//		}
			//	} else {
			//		continue
			//	}
			//}
			//log.Trace("***Executing target No.", rightLoc+1)
		} else if len(PT0Set) == 0 {
			return
		}
		switch theFunc := PT0Set[rightLoc].Value().(type) {
		case *ssa.Function:
			fnName = theFunc.Name()
			a.traverseFn(theFunc, fnName, goID, theIns)
		case *ssa.MakeInterface:
			methodName := theIns.(*ssa.Call).Call.Method.Name()
			if a.prog.MethodSets.MethodSet(pta0Set[location].PointsTo().DynamicTypes().Keys()[0]).Lookup(a.main.Pkg, methodName) == nil { // ignore abstract methods
				break
			}
			check := a.prog.LookupMethod(pta0Set[location].PointsTo().DynamicTypes().Keys()[0], a.main.Pkg, methodName)
			fnName = check.Name()
			a.traverseFn(check, fnName, goID, theIns)
		case *ssa.MakeChan:
			a.chanName = theFunc.Name()
		default:
			break
		}
	} else { // new PTA
		a.pointerNewAnalysis(location, goID, theIns)
	}
}

//bz: use my pta, with offset
func (a *analysis) pointerNewAnalysisOffset(location ssa.Value, f int, goID int, theIns ssa.Instruction) {
	var goInstr *ssa.Go
	if goID == 0 {
		goInstr = nil
	} else {
		goInstr = a.RWIns[goID][0].ins.(*ssa.Go)
	}
	ptr := a.ptaRes.PointsToByGoWithLoopIDOffset(location, f, goInstr, a.loopIDs[goID])
	labels := ptr.PointsTo().Labels()
	if labels == nil {
		//bz: if nil, probably from reflection, which we excluded from analysis;
		// meanwhile, most benchmarks do not use reflection, this is probably infeasible path
		// add a log just in case we need this later
		//log.Debug("Nil Labels: " + location.String() + " @ " + theIns.String())
		return
	}

	a.pointerNewAnalysisHandleFunc(ptr, labels, location, goID, goInstr, theIns)
}

//bz: use my pta
func (a *analysis) pointerNewAnalysis(location ssa.Value, goID int, theIns ssa.Instruction) {
	var goInstr *ssa.Go
	if goID == 0 {
		goInstr = nil
	} else {
		goInstr = a.RWIns[goID][0].ins.(*ssa.Go)
	}

	ptr := a.ptaRes.PointsToByGoWithLoopID(location, goInstr, a.loopIDs[goID])
	labels := ptr.PointsTo().Labels()
	if labels == nil {
		//bz: if nil, probably from reflection, which we excluded from analysis;
		// meanwhile, most benchmarks do not use reflection, this is probably infeasible path
		// add a log just in case we need this later
		//log.Debug("Nil Labels: " + location.String() + " @ " + theIns.String())
		return
	}

	a.pointerNewAnalysisHandleFunc(ptr, labels, location, goID, goInstr, theIns)
}

//bz: use my pta: comment job for both pointerNewAnalysis and pointerNewAnalysisOffset
func (a *analysis) pointerNewAnalysisHandleFunc(ptr pointer.PointerWCtx, labels []*pointer.Label, location ssa.Value, goID int, goInstr *ssa.Go,
	theIns ssa.Instruction) {
	if len(labels) > 1 { // pta returns multiple targets
		labels = a.filterLabels(labels, ptr, location, goID, goInstr, theIns)
		labels = labels[:0] //bz: tmp solution, will be removed
	}

	//var fns []*ssa.Function //bz: let's record mutual excluded fns
	isInvoke := false
	for _, label := range labels { //bz: labels are reduced -> TODO: bz: here should use mutual exclusion too, len > 1
		var fnName string
		switch theFunc := label.Value().(type) {
		case *ssa.Function:
			if sliceContainsFnCtr(a.otherTests, theFunc) > 0 {
				return
			}
			if a.getParam {
				a.paramFunc = theFunc
				a.getParam = !a.getParam
			} else {
				fnName = theFunc.Name()
				a.traverseFn(theFunc, fnName, goID, theIns)
			}
		case *ssa.MakeInterface:
			switch theIns.(type) {
			case *ssa.Call:
				methodName := theIns.(*ssa.Call).Call.Method.Name()
				if a.prog.MethodSets.MethodSet(ptr.PointsTo().DynamicTypes().Keys()[0]).Lookup(a.main.Pkg, methodName) == nil { // ignore abstract methods
					break
				}
				check := a.prog.LookupMethod(ptr.PointsTo().DynamicTypes().Keys()[0], a.main.Pkg, methodName)
				fnName = check.Name()
				a.traverseFn(check, fnName, goID, theIns)
			case *ssa.Go:
				switch theFunc.X.(type) {
				case *ssa.Parameter:
					a.pointerAnalysis(theFunc.X, goID, theIns) //TODO: bz: how to exclude this ?
				}
			}
		case *ssa.MakeChan:
			a.chanName = theFunc.Name() //TODO: bz: how to deal with this ?
		case *ssa.Alloc:
			if call, ok := theIns.(*ssa.Call); ok {
				invokeFunc := a.ptaRes.GetFreeVarFunc(theIns.Parent(), call, goInstr)
				if invokeFunc == nil {
					if DEBUG {
						fmt.Println("no pta target@", theIns)
					}
					break //bz: pta cannot find the target. how?
				}
				fnName = invokeFunc.Name()
				a.traverseFn(invokeFunc, fnName, goID, theIns)
			}
		default: //bz: this label is a pointer/named/interface, why not consider ...
			isInvoke = true
		}
		if isInvoke {
			break //bz: no need to continue, all are pointer-similar type, skip for loop
		}
	}

	if isInvoke { //bz: handle invoke call
		if call, ok := theIns.(*ssa.Call); ok {
			targets := a.ptaRes.GetInvokeFuncs(call, ptr, goInstr)
			for _, target := range targets {
				a.traverseFn(target, target.Name(), goID, theIns)
			}
		} else {
			//bz: here will have weired behavior if running in debug mode
			// -> make interface, skip; e.g. make ServerOption <- *funcServerOption (t2)
			return
		}
	}

	//if len(fns) > 1 {
	//	//bz: let's mutual exclude them
	//	mFns := a.mutualTargets[goID]
	//	if mFns == nil {
	//		mFns = &mutualFns{}
	//		mFns.fns = make(map[*ssa.Function]*mutualGroup)
	//	}
	//	group := make(map[*ssa.Function]*ssa.Function)
	//	for _, fn := range fns {
	//		group[fn] = fn
	//	}
	//	mGroup := &mutualGroup{
	//		group: group,
	//	}
	//	for _, fn := range fns {
	//		mFns.fns[fn] = mGroup
	//	}
	//}
}

//bz: reduce targets
func (a *analysis) filterLabels(labels []*pointer.Label, ptr pointer.PointerWCtx, location ssa.Value, goID int, goInstr *ssa.Go,
	theIns ssa.Instruction) []*pointer.Label {
	switch v := location.(type) {
	case *ssa.Parameter:
		targets := a.filterParam(v, labels, ptr, goID, theIns)
		if targets != nil {
			return targets
		}
	case *ssa.Call:
		targets := a.filterInvoke(v, labels, ptr, goID, theIns)
		if targets != nil {
			return targets
		}
	default:
		break
	}
	//a.conservative()
	//if theIns.Parent() != nil && theIns.Parent().Name() == "withRetry" {
	//	for j, tName := range targetNames {
	//		if tName == "commitAttemptLocked$bound" || tName == "RecvMsg$1" {
	//			label := labels[j]
	//			log.Debug("manually selecting pta target: ", tName, label)
	//		}
	//	}
	//}
	return nil
}

//bz: a simple heuristics now:
// the receiver of this invoke should be already allocated, so let;s check a.RWIns[goID] for *ssa.Alloc -> too many, cannot use as reference
// totally no idea how to filter ...  return all
func (a *analysis) filterInvoke(v *ssa.Call, labels []*pointer.Label, ptr pointer.PointerWCtx, goID int, ins ssa.Instruction) []*pointer.Label {
	//var existAllocs []types.Type //store type now
	//for _, instr := range a.RWIns[goID] {
	//	if alloc, ok := instr.(*ssa.Alloc); ok {
	//		typ := alloc.Type()
	//		existAllocs = append(existAllocs, typ)
	//	}
	//}
	//
	//for _, s := range a.storeFns {
	//	fmt.Println(s.fnIns.String())
	//}
	//fmt.Println()
	//
	//var result []*pointer.Label
	//for _, label := range labels {
	//	fmt.Println(label.String())
	//}
	//return result
	return labels
}

//bz: a simple heuristics now:
//  if ptr is a parameter, use the one level smaller parent (the push, not popped yet) as the standard to filter the target;
//     if we cannot find such a target, then continue reduce one level until no parent exist; TODO: what is we still cannot find?
//     e.g., withRetry() in google.golang.org/grpc/test.(*s).TestMetadataStreamingRPC()
func (a *analysis) filterParam(v *ssa.Parameter, labels []*pointer.Label, ptr pointer.PointerWCtx, goID int, ins ssa.Instruction) []*pointer.Label {
	var result []*pointer.Label
	i := len(a.storeFns) - 1
	parent := a.storeFns[i] //all push here
	for parent.fnIns != nil {
		pStr := parent.fnIns.String()
		for _, label := range labels {
			if strings.HasPrefix(label.String(), pStr) {
				//mostly end with xx.xxx$xxx, which means fn is wrapped in this parent as makeclosure
				result = append(result, label)
			}
		}
		//if cannot find, try next parent
		if len(result) == 0{
			i--
			if i < 0 { //reach main ...
				break
			}
			parent = a.storeFns[i]
		}else{
			return result
		}
	}

	//if exist any label with xx.xxx$xxx but we cannot locate its parent in current stack (if located, it will be stored in result)
	//  -> remove it because it cannot be the correct target
	for _, label := range labels {
		lStr := label.String()
		if strings.Contains(lStr, "$") {
			afterDollarSign := strings.Split(lStr, "$")[1]
			if _, err := strconv.Atoi(afterDollarSign); err != nil { //this is not a number, probably "xxx.xx$bound"
				result = append(result, label)
			}
		}else{ //label is "makeinterface:XXX" and ins is "xx.invoke(...)"
			result = append(result, label)
		}
	}

	return result
}


//bz: an example use of new api
//allocSites := a.ptaRes[a.main].GetAllocations(ptr)
//for _, site := range allocSites {
//	fn := site.Fn //which fn allocates the obj
//	ctx := site.Ctx //context of this obj, context is an array of *callsite; but you cannot directly access them
//	for _, c := range ctx {
//loopID := c.GetLoopID() //this is the loop id you want
//str := c.String()
//	}
//}
//bz: end
