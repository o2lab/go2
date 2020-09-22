package main

import (
	log "github.com/sirupsen/logrus"
	"go/types"
	"golang.org/x/tools/go/pointer"
	"golang.org/x/tools/go/ssa"
)

func (a *analysis) pointerAnalysis(location ssa.Value, goID int, theIns ssa.Instruction) {
	log.Debugf("Solving PTA with %d/%d queries at %s", len(a.ptaConfig.Queries), len(a.ptaConfig.IndirectQueries),
		a.prog.Fset.Position(location.Pos()))
	defer log.Debugf("Solving PTA done")
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
	result, err := pointer.Analyze(a.ptaConfig) // conduct pointer analysis
	if err != nil {
		log.Fatal(err)
	}
	Analysis.result = result
	ptrSet := a.result.Queries                    // set of pointers from result of pointer analysis
	PTSet := ptrSet[location].PointsTo().Labels() // set of labels for locations that the pointer points to
	if indir {
		log.Debug("********************indirect queries need to be analyzed********************")
		//ptrSetIndir := a.result.IndirectQueries
		//PTSetIndir := ptrSetIndir[location].PointsTo().Labels()
	}
	var fnName string
	rightLoc := 0       // initialize index for the right points-to location
	if len(PTSet) > 1 { // multiple targets returned by pointer analysis
		log.Trace("***Pointer Analysis revealed ", len(PTSet), " targets for location - ", a.prog.Fset.Position(location.Pos()))
		var fns []string
		for ind, eachTarget := range PTSet { // check each target
			if eachTarget.Value().Parent() != nil {
				fns = append(fns, eachTarget.Value().Parent().Name())
				log.Trace("*****target No.", ind+1, " - ", eachTarget.Value().Name(), " from function ", eachTarget.Value().Parent().Name())
				if sliceContainsStr(storeIns, eachTarget.Value().Parent().Name()) { // calling function is in current goroutine
					rightLoc = ind
				} else if goID > 0 {
					i := goID
					allStack := goStack[i] // first get stack from immediate caller
					for i > 0 {
						allStack = append(goStack[goCaller[i]], allStack...) // then get from all preceding caller goroutines
						i = goCaller[i]
					}
					if sliceContainsStr(allStack, eachTarget.Value().Parent().Name()) { // check callstack of current goroutine
						rightLoc = ind
					}
				}
			} else {
				log.Debug("target No.", ind+1, " - ", eachTarget.Value().Name(), " with no parent function*********")
			}
		}
		if sliceContainsDup(fns) { // multiple targets reside in same function
			//for i, t := range PTSet {
			//	if a.reachable(t.Value().Parent().Referrers(1), )
			//}
		}
		log.Trace("***Executing target No.", rightLoc+1)
	} else if len(PTSet) == 0 {
		return
	}
	switch theFunc := PTSet[rightLoc].Value().(type) {
	case *ssa.Function:
		fnName = theFunc.Name()
		if !a.exploredFunction(theFunc, goID, theIns) {
			updateRecords(fnName, goID, "PUSH ")
			RWIns[goID] = append(RWIns[goID], theIns)
			a.visitAllInstructions(theFunc, goID)
		}
	case *ssa.MakeInterface:
		methodName := theIns.(*ssa.Call).Call.Method.Name()
		if a.prog.MethodSets.MethodSet(ptrSet[location].PointsTo().DynamicTypes().Keys()[0]).Lookup(a.mains[0].Pkg, methodName) == nil { // ignore abstract methods
			return
		}
		check := a.prog.LookupMethod(ptrSet[location].PointsTo().DynamicTypes().Keys()[0], a.mains[0].Pkg, methodName)
		fnName = check.Name()
		if !a.exploredFunction(check, goID, theIns) {
			updateRecords(fnName, goID, "PUSH ")
			RWIns[goID] = append(RWIns[goID], theIns)
			a.visitAllInstructions(check, goID)
		}
	case *ssa.MakeChan:
		chanName = theFunc.Name()
	default:
		return
	}
}
