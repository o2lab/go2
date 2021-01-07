package main

import (
	"fmt"
	"strconv"
	//log "github.com/sirupsen/logrus"
	//"github.tamu.edu/April1989/go_tools/go/pointer"
	"github.tamu.edu/April1989/go_tools/go/ssa"
	//"go/types"
)

func (a *analysis) pointerAnalysis(location ssa.Value, goID int, theIns ssa.Instruction) {
	if a.useNewPTA {
		a.pointerAnalysis_new(location, goID, theIns)
	}else{
		panic("WRONG PATH !!! @ a.pointerAnalysis()")
		//a.pointerAnalysis_original(location, goID, theIns)
	}
}

//// pointerAnalysis conducts pointer analysis using various built in pointer tools
//func (a *analysis) pointerAnalysis_original(location ssa.Value, goID int, theIns ssa.Instruction) {
//	switch locType := location.(type) {
//	case *ssa.Parameter:
//		if locType.Object().Pkg().Name() == "reflect" { // ignore reflect library
//			return
//		}
//	}
//	indir := false // toggle for indirect query (global variables)
//	if pointer.CanPoint(location.Type()) {
//		a.ptaConfig.AddQuery(location)
//	} else if underType, ok := location.Type().Underlying().(*types.Pointer); ok && pointer.CanPoint(underType.Elem()) {
//		indir = true
//		a.ptaConfig.AddIndirectQuery(location)
//	}
//	result, err := pointer.Analyze(a.ptaConfig) // conduct pointer analysis
//	if err != nil {
//		log.Fatal(err)
//	}
//	Analysis.result = result
//	ptrSet := a.result.Queries                    // set of pointers from result of pointer analysis
//	PTSet := ptrSet[location].PointsTo().Labels() // set of labels for locations that the pointer points to
//	if indir {
//		log.Debug("********************indirect queries need to be analyzed********************")
//		//ptrSetIndir := a.result.IndirectQueries
//		//PTSetIndir := ptrSetIndir[location].PointsTo().Labels()
//	}
//	var fnName string
//	rightLoc := 0       // initialize index for the right points-to location
//	if len(PTSet) > 1 { // multiple targets returned by pointer analysis
//		//log.Trace("***Pointer Analysis revealed ", len(PTSet), " targets for location - ", a.prog.Fset.Position(location.Pos()))
//		var fns []string
//		for ind, eachTarget := range PTSet { // check each target
//			if eachTarget.Value().Parent() != nil {
//				fns = append(fns, eachTarget.Value().Parent().Name())
//				//log.Trace("*****target No.", ind+1, " - ", eachTarget.Value().Name(), " from function ", eachTarget.Value().Parent().Name())
//				if sliceContainsStr(a.storeIns, eachTarget.Value().Parent().Name()) { // calling function is in current goroutine
//					rightLoc = ind
//					break
//				}
//			} else {
//				continue
//			}
//		}
//		//log.Trace("***Executing target No.", rightLoc+1)
//	} else if len(PTSet) == 0 {
//		return
//	}
//	switch theFunc := PTSet[rightLoc].Value().(type) {
//	case *ssa.Function:
//		fnName = theFunc.Name()
//		if !a.exploredFunction(theFunc, goID, theIns) {
//			a.updateRecords(fnName, goID, "PUSH ")
//			a.RWIns[goID] = append(a.RWIns[goID], theIns)
//			a.visitAllInstructions(theFunc, goID)
//		}
//	case *ssa.MakeInterface:
//		methodName := theIns.(*ssa.Call).Call.Method.Name()
//		if a.prog.MethodSets.MethodSet(ptrSet[location].PointsTo().DynamicTypes().Keys()[0]).Lookup(a.mains[0].Pkg, methodName) == nil { // ignore abstract methods
//			break
//		}
//		check := a.prog.LookupMethod(ptrSet[location].PointsTo().DynamicTypes().Keys()[0], a.mains[0].Pkg, methodName)
//		fnName = check.Name()
//		if !a.exploredFunction(check, goID, theIns) {
//			a.updateRecords(fnName, goID, "PUSH ")
//			a.RWIns[goID] = append(a.RWIns[goID], theIns)
//			a.visitAllInstructions(check, goID)
//		}
//	case *ssa.MakeChan:
//		a.chanName = theFunc.Name()
//	default:
//		break
//	}
//}

func (a *analysis) pointerAnalysis_new(location ssa.Value, goID int, theIns ssa.Instruction) {
	switch locType := location.(type) {
	case *ssa.Parameter:
		if locType.Object().Pkg().Name() == "reflect" { // ignore reflect library
			return
		}
	}

	//TODO: bz: needs to match with goID, currently a rough match
	var goInstr *ssa.Go
	if goID == 0 {
		goInstr = nil
	}else{
		goInstr = a.goID2info[goID].goIns
		if goInstr == nil {
			panic("Not recorded go instruction in a.goID2info @ goID" + strconv.Itoa(goID))
		}
	}
	pts := a.result.PointsToByGo(location, goInstr) //return type: PointerWCtx
	if pts.IsNil() {
		fmt.Println(" *** nil pts: " + location.Name() + "  goID: " + strconv.Itoa(goID) + " *** ")  //bz: useNewPTA ...
		//bz: the callee target is recorded in cg, not pta
		if call, ok := theIns.(*ssa.Call); ok {
			invokeFunc := a.result.GetFunc(location, call, goInstr)
			//do the same as case *ssa.Function
			fnName := invokeFunc.Name()
			if !a.exploredFunction(invokeFunc, goID, theIns) {
				a.updateRecords(fnName, goID, "PUSH ")
				a.RWIns[goID] = append(a.RWIns[goID], theIns)
				a.visitAllInstructions(invokeFunc, goID)
			}
		}
		return
	}

	fmt.Println(" *** ssa.Value: " + location.Name() + "  goID: " + strconv.Itoa(goID) + " " + goInstr.String() + " *** ")  //bz: useNewPTA ...
	pts_labels := pts.Labels() // set of labels for locations that the pointer points to
	var fnName string
	rightLoc := 0            // initialize index for the right points-to location
	if len(pts_labels) > 1 { // multiple targets returned by pointer analysis
		//log.Trace("***Pointer Analysis revealed ", len(pts_labels), " targets for location - ", a.prog.Fset.Position(location.Pos()))
		var fns []string
		for ind, eachTarget := range pts_labels { // check each target
			if eachTarget.Value().Parent() != nil {
				fns = append(fns, eachTarget.Value().Parent().Name())
				//log.Trace("*****target No.", ind+1, " - ", eachTarget.Value().Name(), " from function ", eachTarget.Value().Parent().Name())
				if sliceContainsStr(a.storeIns, eachTarget.Value().Parent().Name()) { // calling function is in current goroutine
					rightLoc = ind
					break
				}
			} else {
				continue
			}
		}
		//log.Trace("***Executing target No.", rightLoc+1)
	} else if len(pts_labels) == 0 {
		return
	}
	switch theFunc := pts_labels[rightLoc].Value().(type) {
	case *ssa.Function:
		fnName = theFunc.Name()
		if !a.exploredFunction(theFunc, goID, theIns) {
			a.updateRecords(fnName, goID, "PUSH ")
			a.RWIns[goID] = append(a.RWIns[goID], theIns)
			a.visitAllInstructions(theFunc, goID)
		}
	case *ssa.MakeInterface:
		methodName := theIns.(*ssa.Call).Call.Method.Name()                                                      //ctx ??
		if a.prog.MethodSets.MethodSet(pts.DynamicTypes().Keys()[0]).Lookup(a.mains[0].Pkg, methodName) == nil { // ignore abstract methods
			break
		}
		check := a.prog.LookupMethod(pts.DynamicTypes().Keys()[0], a.mains[0].Pkg, methodName)
		fnName = check.Name()
		if !a.exploredFunction(check, goID, theIns) {
			a.updateRecords(fnName, goID, "PUSH ")
			a.RWIns[goID] = append(a.RWIns[goID], theIns)
			a.visitAllInstructions(check, goID)
		}
	case *ssa.MakeChan:
		a.chanName = theFunc.Name()
	case *ssa.Alloc: //bz: missing invoke callee target if func is wrapped as parameter, e.g., Kubernetes.88331
	    //tmp solution: find it in call graph
		if call, ok := theIns.(*ssa.Call); ok {
			invokeFunc := a.result.GetFreeVarFunc(theFunc, call, goInstr)
			//do the same as case *ssa.Function
			fnName = invokeFunc.Name()
			if !a.exploredFunction(invokeFunc, goID, theIns) {
				a.updateRecords(fnName, goID, "PUSH ")
				a.RWIns[goID] = append(a.RWIns[goID], theIns)
				a.visitAllInstructions(invokeFunc, goID)
			}
		}
	default:
		break
	}
}