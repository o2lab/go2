package main

import (
	"fmt"
	"github.tamu.edu/April1989/go_tools/go/pointer"
	"github.tamu.edu/April1989/go_tools/go/ssa"
	"strconv"
	//"go/types"
	//log "github.com/sirupsen/logrus"
	//"go/types"
	//"golang.org/x/tools/go/pointer"
	//"golang.org/x/tools/go/ssa"
	//"strings"
)

func (a *analysis) pointerAnalysis(location ssa.Value, goID int, theIns ssa.Instruction) {
	if a.useNewPTA {
		a.pointerAnalysis_new(location, goID, theIns)
	}else{
		panic("Use default pta: WRONG PATH !!! @ a.pointerAnalysis()")
		//a.pointerAnalysis_original(location, goID, theIns)
	}
}

//bz: find corresponding go instruction -> input of pta
//TODO: bz: needs to match with goID, currently a rough match
func (a *analysis) getGoInstrForGoID(goID int) *ssa.Go {
	if goID == 0 {
		return nil
	}else{
		goInstr := a.goID2info[goID].goIns
		if goInstr == nil {
			panic("Not recorded go instruction in a.goID2info @ goID" + strconv.Itoa(goID))
		}
		return goInstr
	}
}

func (a *analysis) pointerAnalysis_new(location ssa.Value, goID int, theIns ssa.Instruction) {//bz: useNewPTA ...
	switch locType := location.(type) {
	case *ssa.Parameter:
		if locType.Object().Pkg().Name() == "reflect" { // ignore reflect library
			return
		}
	}
	goInstr := a.getGoInstrForGoID(goID)
	//return type: PointerWCtx
	var pts pointer.PointerWCtx
	if a.ptaConfig.DiscardQueries { // bz: we are not using queries now
		pts = a.result.PointsTo2(theIns, goInstr, theIns.Parent())
	}else{ //bz: use queries/indirect/global/extended
		pts = a.result.PointsToByGo(location, goInstr)
	}

	if pts.IsNil() {
		if call, ok := theIns.(*ssa.Call); ok {
			//bz: the callee target is recorded in cg, not pta
			invokeFunc := a.result.GetFunc(location, call, goInstr)
			if invokeFunc == nil {
				if a.useNewPTA && a.ptaConfig.DEBUG {
					fmt.Println(" *** nil invokeFunc: " + location.Name() + " call: " + call.String() + "  goID: " + strconv.Itoa(goID) + " *** ")
				}
				return
			}
			//do the same as case *ssa.Function
			a.traverseFunc(invokeFunc, goID, theIns)
			return
		}
		if a.ptaConfig.DEBUG { //debug use
			fmt.Println(" *** nil pts: " + location.Name() + "  goID: " + strconv.Itoa(goID) + " *** ")
		}
		return
	}
	if a.ptaConfig.DEBUG {
		if goInstr == nil {
			fmt.Println(" *** ssa.Value: " + location.Name() + "  goID: " + strconv.Itoa(goID) + " main *** ")
		}else {
			fmt.Println(" *** ssa.Value: " + location.Name() + "  goID: " + strconv.Itoa(goID) + " " + goInstr.String() + " *** ")
		}
	}
	pts_labels := pts.Labels() // set of labels for locations that the pointer points to
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
	if a.ptaConfig.DiscardQueries { // bz: we are not using queries now
		switch theFunc := pts_labels[rightLoc].Value().(type) {
		case *ssa.Function:
			a.traverseFunc(theFunc, goID, theIns)
		case *ssa.MakeInterface:
			if call, ok := theFunc.X.(*ssa.Call); ok {
				val := call.Call.Value //bz: *ssa.Function
				invokeFunc, _ := val.(*ssa.Function)
				a.traverseFunc(invokeFunc, goID, theIns)
			}
		case *ssa.MakeChan:
			a.chanName = theFunc.Name()
		case *ssa.Alloc:
			if _, ok := theIns.(*ssa.Call); ok {
				invokeFunc := a.result.GetFunc2(pts)
				//do the same as case *ssa.Function
				a.traverseFunc(invokeFunc, goID, theIns)
			}
		default:
			break
		}
	}else{
		switch theFunc := pts_labels[rightLoc].Value().(type) {
		case *ssa.Function:
			a.traverseFunc(theFunc, goID, theIns)
		case *ssa.MakeInterface:
			methodName := theIns.(*ssa.Call).Call.Method.Name()                                                      //ctx ??
			if a.prog.MethodSets.MethodSet(pts.DynamicTypes().Keys()[0]).Lookup(a.mains[0].Pkg, methodName) == nil { // ignore abstract methods
				break
			}
			check := a.prog.LookupMethod(pts.DynamicTypes().Keys()[0], a.mains[0].Pkg, methodName)
			a.traverseFunc(check, goID, theIns)
		case *ssa.MakeChan:
			a.chanName = theFunc.Name()
		case *ssa.Alloc: //bz: missing invoke callee target if func is wrapped as parameter, e.g., Kubernetes.88331
			//tmp solution: find it in call graph
			if call, ok := theIns.(*ssa.Call); ok {
				invokeFunc := a.result.GetFreeVarFunc(theFunc, call, goInstr)
				//do the same as case *ssa.Function
				a.traverseFunc(invokeFunc, goID, theIns)
			}
		default:
			break
		}
	}
}

//bz: abstract out
func (a *analysis) traverseFunc(invokeFunc *ssa.Function, goID int, theIns ssa.Instruction) {
	if !a.exploredFunction(invokeFunc, goID, theIns) {
		fnName := invokeFunc.Name()
		a.updateRecords(fnName, goID, "PUSH ")
		a.RWIns[goID] = append(a.RWIns[goID], theIns)
		a.visitAllInstructions(invokeFunc, goID)
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