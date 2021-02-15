package main

import (
	"github.tamu.edu/April1989/go_tools/go/pointer"
	"github.tamu.edu/April1989/go_tools/go/ssa"
	"go/types"
	"strings"

	//"go/types"
	//"golang.org/x/tools/go/pointer"
	//"golang.org/x/tools/go/ssa"
	//"strings"
)

func (a *analysis) pointerAnalysis(location ssa.Value, goID int, theIns ssa.Instruction) {
	switch locType := location.(type) {
	case *ssa.Parameter:
		if locType.Object().Pkg().Name() == "reflect" { // ignore reflect library
			return
		}
	}
	indir := false // toggle for indirect query (global variables)
	if pointer.CanPoint(location.Type()) {
		a.ptaConfig.AddQuery(location)
	} else if underType, ok := location.Type().Underlying().(*types.Pointer); ok && pointer.CanPoint(underType.Elem()) {
		indir = true
		a.ptaConfig.AddIndirectQuery(location)
	}
	var result *pointer.Result
	var err error
	// TODO: need to differentiate imported packages for PTA
	if useDefaultPTA {
		result, err = pointer.Analyze(a.ptaConfig) // conduct pointer analysis
	} else if useNewPTA {
		result, err = pointer.Analyze(a.ptaConfig) // conduct pointer analysis
	}

	if err != nil && strings.HasPrefix(err.Error(), "internal error") {
		return
	}
	a.result = result
	ptrSet := a.result.Queries[location]          // set of pointers (with context) from result of pointer analysis
	if indir {
		ptrSetIndir := a.result.IndirectQueries[location]
		_ = ptrSetIndir// TODO: check these labels
	}
	var fnName string
	rightLoc := 0       // initialize index for the right points-to location
	rightCtx := 0 // initialize index for the pointer with right context
	if len(ptrSet) > 1 { // multiple targets returned by pointer analysis
		//log.Trace("***Pointer Analysis revealed ", len(PTSet), " targets for location - ", a.prog.Fset.Position(location.Pos()))
		var fns []string
		for ind, eachPtr := range ptrSet { // check each pointer (with context)
			//log.Debug(eachPtr.GetMyContext())
			ptsLabels := eachPtr.PointsTo().Labels() // set of labels for locations that the pointer (with context) points to
			for _, eachLabel := range ptsLabels {
				if eachLabel.Value().Parent() != nil {
					fns = append(fns, eachLabel.Value().Parent().Name())
					//log.Trace("*****target No.", ind+1, " - ", eachPtr.Value().Name(), " from function ", eachPtr.Value().Parent().Name())
					if sliceContainsStr(a.storeIns, eachLabel.Value().Parent().Name()) { // calling function is in current goroutine
						rightLoc = ind
						break
					}
				} else {
					continue
				}
			}
		}
		//log.Trace("***Executing target No.", rightLoc+1)
	} else if len(ptrSet) == 0 {
		//log.Debug("Points-to set is empty: " + location.String() + " @ " + theIns.String())
		return
	}
	labels := ptrSet[rightLoc].PointsTo().Labels()
	if labels == nil {
		//bz: if nil, probably from reflection, which we excluded from analysis;
		// meanwhile, most benchmarks do not use reflection, this is probably infeasible path
		// add a log just in case we need this later
		//log.Debug("Nil Labels: " + location.String() + " @ " + theIns.String())
		return
	}

	switch theFunc := labels[rightCtx].Value().(type) {
	case *ssa.Function:
		fnName = theFunc.Name()
		if !a.exploredFunction(theFunc, goID, theIns) {
			a.updateRecords(fnName, goID, "PUSH ")
			a.RWIns[goID] = append(a.RWIns[goID], theIns)
			a.visitAllInstructions(theFunc, goID)
		}
	case *ssa.MakeInterface:
		methodName := theIns.(*ssa.Call).Call.Method.Name()
		if a.prog.MethodSets.MethodSet(ptrSet[rightLoc].PointsTo().DynamicTypes().Keys()[0]).Lookup(a.mains[0].Pkg, methodName) == nil { // ignore abstract methods
			break
		}
		check := a.prog.LookupMethod(ptrSet[rightLoc].PointsTo().DynamicTypes().Keys()[0], a.mains[0].Pkg, methodName)
		fnName = check.Name()
		if !a.exploredFunction(check, goID, theIns) {
			a.updateRecords(fnName, goID, "PUSH ")
			a.RWIns[goID] = append(a.RWIns[goID], theIns)
			a.visitAllInstructions(check, goID)
		}
	case *ssa.MakeChan:
		a.chanName = theFunc.Name()
	default:
		break
	}
}
