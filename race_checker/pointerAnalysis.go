package main

import (
	"github.tamu.edu/April1989/go_tools/go/pointer"
	pta0 "github.tamu.edu/April1989/go_tools/go/pointer_default"
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
		if useDaultPTA {
			a.pta0Cfg.AddQuery(location)
		}
	} else if underType, ok := location.Type().Underlying().(*types.Pointer); ok && pointer.CanPoint(underType.Elem()) {
		indir = true
		if useDefaultPTA {
			a.pta0Cfg.AddIndirectQuery(location)
		}
	}
	var result map[*ssa.Package]*pointer.Result
	var ptaResult *pta0.Result
	var err error
	if useDefaultPTA {
		ptaResult, err = pta0.Analyze(a.pta0Cfg)
		a.pta0Result = ptaResult
	} else {
		result, err = pointer.AnalyzeMultiMains(a.ptaConfig)
		if err != nil && strings.HasPrefix(err.Error(), "internal error") {
			return
		}
		a.result = result
	}
	var ptrSet []pointer.PointerWCtx
	var pta0Set map[ssa.Value]pta0.Pointer
	var PT0Set []*pta0.Label
	if useDefaultPTA {
		pta0Set = a.pta0Result.Queries
		PT0Set = pta0Set[location].PointsTo().Labels()

		var fnName string
		rightLoc := 0       // initialize index for the right points-to location
		if len(PT0Set) > 1 { // multiple targets returned by pointer analysis
			//log.Trace("***Pointer Analysis revealed ", len(PTSet), " targets for location - ", a.prog.Fset.Position(location.Pos()))
			var fns []string
			for ind, eachTarget := range PT0Set { // check each target
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
		} else if len(PT0Set) == 0 {
			return
		}
		switch theFunc := PT0Set[rightLoc].Value().(type) {
		case *ssa.Function:
			fnName = theFunc.Name()
			if !a.exploredFunction(theFunc, goID, theIns) {
				a.updateRecords(fnName, goID, "PUSH ")
				a.RWIns[a.fromPath][goID] = append(a.RWIns[a.fromPath][goID], theIns)
				a.visitAllInstructions(theFunc, goID)
			}
		case *ssa.MakeInterface:
			methodName := theIns.(*ssa.Call).Call.Method.Name()
			if a.prog.MethodSets.MethodSet(pta0Set[location].PointsTo().DynamicTypes().Keys()[0]).Lookup(a.mains[0].Pkg, methodName) == nil { // ignore abstract methods
				break
			}
			check := a.prog.LookupMethod(pta0Set[location].PointsTo().DynamicTypes().Keys()[0], a.mains[0].Pkg, methodName)
			fnName = check.Name()
			if !a.exploredFunction(check, goID, theIns) {
				a.updateRecords(fnName, goID, "PUSH ")
				a.RWIns[a.fromPath][goID] = append(a.RWIns[a.fromPath][goID], theIns)
				a.visitAllInstructions(check, goID)
			}
		case *ssa.MakeChan:
			a.chanName = theFunc.Name()
		default:
			break
		}
	} else { // new PTA
		ptrSet = a.result[a.main].Queries[location]          // set of pointers (with context) from result of pointer analysis

		if indir {
			ptrSetIndir := a.result[a.main].IndirectQueries[location]
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
				a.RWIns[a.fromPath][goID] = append(a.RWIns[a.fromPath][goID], theIns)
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
				a.RWIns[a.fromPath][goID] = append(a.RWIns[a.fromPath][goID], theIns)
				a.visitAllInstructions(check, goID)
			}
		case *ssa.MakeChan:
			a.chanName = theFunc.Name()
		default:
			break
		}
	}
}
