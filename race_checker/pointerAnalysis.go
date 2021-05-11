package main

import (
	"fmt"
	"github.com/april1989/origin-go-tools/go/pointer"
	pta0 "github.com/april1989/origin-go-tools/go/pointer_default"
	"github.com/april1989/origin-go-tools/go/ssa"
	log "github.com/sirupsen/logrus"
	"go/types"
)

func (a *analysis) pointerAnalysis(location ssa.Value, goID int, theIns ssa.Instruction) {
	switch locType := location.(type) {
	case *ssa.Parameter:
		if locType.Object().Pkg().Name() == "reflect" { // ignore reflect library
			return
		}
	}
	//indir := false // toggle for indirect query (global variables)
	if pointer.CanPoint(location.Type()) {
		if useDefaultPTA {
			a.mu.Lock()
			a.ptaCfg0.AddQuery(location)
			a.mu.Unlock()
		}
	} else if underType, ok := location.Type().Underlying().(*types.Pointer); ok && pointer.CanPoint(underType.Elem()) {
		//indir = true
		if useDefaultPTA {
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
		rightLoc := 0       // initialize index for the right points-to location
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
			if !a.exploredFunction(theFunc, goID, theIns) {
				a.updateRecords(fnName, goID, "PUSH ", theFunc, theIns)
				a.RWIns[goID] = append(a.RWIns[goID], theIns)
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
				a.updateRecords(fnName, goID, "PUSH ", check, theIns)
				a.RWIns[goID] = append(a.RWIns[goID], theIns)
				a.visitAllInstructions(check, goID)
			}
		case *ssa.MakeChan:
			a.chanName = theFunc.Name()
		default:
			break
		}
	} else { // new PTA
		var ptr pointer.PointerWCtx
		if goID == 0 {
			ptr = a.ptaRes[a.main].PointsToByGoWithLoopID(location, nil, a.loopIDs[0])
		} else {
			ptr = a.ptaRes[a.main].PointsToByGoWithLoopID(location, a.RWIns[goID][0].(*ssa.Go), a.loopIDs[goID])
		}
		labels := ptr.PointsTo().Labels()
		if labels == nil {
			//bz: if nil, probably from reflection, which we excluded from analysis;
			// meanwhile, most benchmarks do not use reflection, this is probably infeasible path
			// add a log just in case we need this later
			//log.Debug("Nil Labels: " + location.String() + " @ " + theIns.String())
			return
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

		var label *pointer.Label
		label = labels[0] // use first target by default
		//TODO: bz: need some heuristics to remove impossible targets, e.g.,
		// when t4(ctx, method, req, reply, cc, t9, opts...) in google.golang.org/grpc.chainUnaryClientInterceptors$1
		// returns 3 targets: TestInterceptorCanAccessCallOptions$2, failOkayRPC and chainUnaryClientInterceptors$1
		// since chainUnaryClientInterceptors is already pushed (or popped) before,
		// the correct target should be nil, since TestInterceptorCanAccessCallOptions$2 is from another test and
		// chainUnaryClientInterceptors$1 already pushed and failOkayRPC is used by TestUnaryClientInterceptor

		if len(labels) > 1 { // pta returns multiple targets
			var stack []fnCallInfo
			var path []int
			ID := goID
			for ID > 0 { // traverse up the call chain
				path = append([]int{ID}, path...) // prepend
				temp := a.goCaller[ID]
				ID = temp
			}
			for _, eachGo := range path { // concatenate call chain from each thread
				stack = append(stack, a.goStack[eachGo][:len(a.goStack[eachGo])-1]...)
			}
			fnCall := fnCallIns{theIns.Parent(), goID}
			stack = append(stack, a.stackMap[fnCall].fnCalls...)

			targetNames := make([]string, len(labels)) // get names of all targets
			for i, eachLabel := range labels {
				switch theFunc := eachLabel.Value().(type) {
				case *ssa.Function:
					if theFunc == testEntry {
						return
					}
					targetNames[i] = theFunc.Name()
				case *ssa.MakeInterface:
					switch theIns.(type) {
					case *ssa.Call:
						methodName := theIns.(*ssa.Call).Call.Method.Name()
						if a.prog.MethodSets.MethodSet(ptr.PointsTo().DynamicTypes().Keys()[0]).Lookup(a.mains[0].Pkg, methodName) == nil { // ignore abstract methods
							break
						}
						check := a.prog.LookupMethod(ptr.PointsTo().DynamicTypes().Keys()[0], a.mains[0].Pkg, methodName)
						targetNames[i] = check.Name()
					case *ssa.Go:
						switch theFunc.X.(type) {
						case *ssa.Parameter:
							targetNames[i] = theFunc.X.Name()
						}
					}
				case *ssa.Alloc:
					if call, ok := theIns.(*ssa.Call); ok {
						var goInstr *ssa.Go
						if goID == 0 {
							goInstr = nil
						} else {
							goInstr = a.RWIns[goID][0].(*ssa.Go)
						}
						invokeFunc := a.ptaRes[a.main].GetFreeVarFunc(theIns.Parent(), call, goInstr)
						if invokeFunc == nil {
							fmt.Println("no pta target")
							break //bz: pta cannot find the target. how?
						}
						targetNames[i] = invokeFunc.Name()
					}
				case *ssa.MakeSlice:
					targetNames[i] = theFunc.Name()
				default:
					break
				}
			}
			//log.Debug("PTA identified multiple targets: ", targetNames)

			//out:
			//for i := len(stack)-1; i >= 0; i-- {
			//	for j, tName := range targetNames {
			//		if stack[i].fnIns.Name() == tName {
			//			label = labels[j]
			//			log.Debug("referencing callstack, select target: ", tName)
			//			break out
			//		}
			//	}
			//}
			if theIns.Parent() != nil && theIns.Parent().Name() == "withRetry" {
				for j, tName := range targetNames {
					if tName == "commitAttemptLocked$bound" || tName == "RecvMsg$1" {
						label = labels[j]
						log.Debug("manually selecting pta target: ", tName)
					}
				}
			}

		}

		var fnName string
		switch theFunc := label.Value().(type) {
		case *ssa.Function:
			if theFunc == testEntry {
				return
			}
			if a.getParam {
				a.paramFunc = theFunc
				a.getParam = !a.getParam
			} else {
				fnName = theFunc.Name()
				if !a.exploredFunction(theFunc, goID, theIns) {
					a.updateRecords(fnName, goID, "PUSH ", theFunc, theIns)
					a.RWIns[goID] = append(a.RWIns[goID], theIns)
					a.visitAllInstructions(theFunc, goID)
				}
			}
		case *ssa.MakeInterface:
			switch theIns.(type) {
			case *ssa.Call:
				methodName := theIns.(*ssa.Call).Call.Method.Name()
				if a.prog.MethodSets.MethodSet(ptr.PointsTo().DynamicTypes().Keys()[0]).Lookup(a.mains[0].Pkg, methodName) == nil { // ignore abstract methods
					break
				}
				check := a.prog.LookupMethod(ptr.PointsTo().DynamicTypes().Keys()[0], a.mains[0].Pkg, methodName)
				fnName = check.Name()
				if !a.exploredFunction(check, goID, theIns) {
					a.updateRecords(fnName, goID, "PUSH ", check, theIns)
					a.RWIns[goID] = append(a.RWIns[goID], theIns)
					a.visitAllInstructions(check, goID)
				}
			case *ssa.Go:
				switch theFunc.X.(type) {
				case *ssa.Parameter:
					a.pointerAnalysis(theFunc.X, goID, theIns)
				}
			}
		case *ssa.MakeChan:
			a.chanName = theFunc.Name()
		case *ssa.Alloc:
			if call, ok := theIns.(*ssa.Call); ok {
				var goInstr *ssa.Go
				if goID == 0 {
					goInstr = nil
				} else {
					goInstr = a.RWIns[goID][0].(*ssa.Go)
				}
				invokeFunc := a.ptaRes[a.main].GetFreeVarFunc(theIns.Parent(), call, goInstr)
				if invokeFunc == nil {
					fmt.Println("no pta target")
					break //bz: pta cannot find the target. how?
				}
				fnName = invokeFunc.Name()
				if !a.exploredFunction(invokeFunc, goID, theIns) {
					a.updateRecords(fnName, goID, "PUSH ", invokeFunc, theIns)
					a.RWIns[goID] = append(a.RWIns[goID], theIns)
					a.visitAllInstructions(invokeFunc, goID)
				}
			}
		default:
			break
		}
	}
}
