package main

import (
	"fmt"
	"github.com/april1989/origin-go-tools/go/pointer"
	"github.com/april1989/origin-go-tools/go/ssa"
	"github.com/logrusorgru/aurora"
	log "github.com/sirupsen/logrus"
	"github.com/twmb/algoimpl/go/graph"
	"go/token"
	"regexp"
	"strings"
)

func isLocal(ins ssa.Instruction) bool {
	switch rw := ins.(type) {
	case *ssa.UnOp:
		if faddr, ok := rw.X.(*ssa.FieldAddr); ok {
			if _, local := faddr.X.(*ssa.Alloc); local {
				return true
			}
		}
	case *ssa.Store:
		if faddr, ok := rw.Addr.(*ssa.FieldAddr); ok {
			if _, local := faddr.X.(*ssa.Alloc); local {
				return true
			}
		}
	}
	return false
}

// checkRacyPairs checks accesses among two concurrent goroutines
func (a *analysis) checkRacyPairs() []*raceInfo {
	var races []*raceInfo
	for i := 0; i < len(a.RWIns); i++ {
		for j := i + 1; j < len(a.RWIns); j++ { // must be in different goroutines, j always greater than i
			for ii, goI := range a.RWIns[i] {
				if (i == 0 && ii < a.insDRA) || (channelComm && sliceContainsBloc(a.omitComm, goI.node.Block())) {
					continue
				}
				for jj, goJ := range a.RWIns[j] {
					if channelComm && sliceContainsBloc(a.omitComm, goJ.node.Block()) {
						continue
					}
					////!!!! bz: for my debug, please comment off, do not delete
					//fmt.Println("goI: ", goI.String(), " (i: ", i, ")  goJ: ", goJ.String(), " (j: ", j, ")")
					//if strings.Contains(goI.String(), "&t6[t9]") && strings.Contains(goJ.String(), "*checkName") {
					//	fmt.Println() //goI:  &t6[t9]  (i:  1 )  goJ:  *checkName  (j:  3 )
					//}

					goINode := goI.node //bz: read/write of goI
					goJNode := goJ.node //bz: read/write of goJ
					if (isWriteIns(goINode) && isWriteIns(goJNode)) || (isWriteIns(goINode) && a.isReadIns(goJNode)) || (a.isReadIns(goINode) && isWriteIns(goJNode)) { // only read and write instructions
						if isLocal(goINode) && isLocal(goJNode) { // both are locally declared
							continue
						}
						insSlice := []ssa.Instruction{goINode, goJNode}
						addressPair := a.insAddress(insSlice) // one instruction from each goroutine
						if addressPair[0] == nil || addressPair[1] == nil {
							continue
						}
						//////!!!! bz: for my debug, please comment off, do not delete
						//var goIinstr string
						//var goJinstr string
						//if i == 0 {
						//	goIinstr = "main"
						//} else {
						//	goIinstr = a.RWIns[i][0].node.String()
						//}
						//if j == 0 {
						//	goJinstr = "main"
						//} else {
						//	goJinstr = a.RWIns[j][0].node.String()
						//}
						//fmt.Println(addressPair[0], " Go: ", goIinstr, " loopid: ", a.loopIDs[i], ";  ", addressPair[1], " Go: ", goJinstr, " loopid: ", a.loopIDs[j])
						////&t151[t148]  Go:  go t144(t142)  loopid:  1 ;   &t12[goID]  Go:  go t144(t142)  loopid:  2
						//if strings.Contains(addressPair[0].String(), "&t151[t148]") &&
						//	strings.Contains(addressPair[1].String(), "&t12[goID]") {
						//	fmt.Println()
						//}

						if a.sameAddress(addressPair[0], addressPair[1], i, j) &&
							!sliceContains(a.reportedAddr, addressPair) &&
							!a.reachable(goINode, i, goJNode, j, false) &&
							!a.reachable(goJNode, j, goINode, i, false) &&
							!a.bothAtomic(insSlice[0], insSlice[1]) &&
							!a.lockSetsIntersect(goINode, goJNode, i, j) &&
							!a.selectMutEx(insSlice[0], insSlice[1]) {
							a.reportedAddr = append(a.reportedAddr, addressPair)

							////bz: for my debug, please comment off, do not delete
							//if!a.reachable(goINode, i, goJNode, j, true) &&
							//	!a.reachable(goJNode, j, goINode, i, true) {
							//	fmt.Println()
							//}

							stacks := make([][]*stackInfo, 2)
							stacks[0] = goI.stack
							stacks[1] = goJ.stack
							ri := &raceInfo{
								insPair:  insSlice,
								addrPair: addressPair,
								goIDs:    []int{i, j},
								insInd:   []int{ii, jj},
								stacks:   stacks,
							}

							if !allEntries {
								a.printRace(len(a.reportedAddr), ri)
							}
							races = append(races, ri)
						}
					}
				}
			}
		}
	}
	return races
}

// insAddress takes a slice of ssa instructions and returns a slice of their corresponding addresses
func (a *analysis) insAddress(insSlice []ssa.Instruction) [2]ssa.Value { // obtain addresses of instructions
	theAddrs := [2]ssa.Value{}
	for i, anIns := range insSlice {
		switch theIns := anIns.(type) {
		case *ssa.Store: // write
			theAddrs[i] = theIns.Addr
		case *ssa.Call:
			if theIns.Call.Value.Name() == "delete" { // write
				theAddrs[i] = theIns.Call.Args[0].(*ssa.UnOp).X
			} else if strings.HasPrefix(theIns.Call.Value.Name(), "Add") && theIns.Call.StaticCallee().Pkg.Pkg.Name() == "atomic" { // write
				theAddrs[i] = theIns.Call.Args[0].(*ssa.FieldAddr).X
			} else if len(theIns.Call.Args) > 0 { // read
				for _, anArg := range theIns.Call.Args {
					if readAcc, ok := anArg.(*ssa.FieldAddr); ok {
						theAddrs[i] = readAcc.X
					}
				}
			}
		case *ssa.UnOp: // read
			theAddrs[i] = theIns.X
		case *ssa.Lookup: // read
			theAddrs[i] = theIns.X
		case *ssa.FieldAddr: // read
			theAddrs[i] = theIns.X
		case *ssa.MapUpdate: // write
			switch accType := theIns.Map.(type) {
			case *ssa.UnOp:
				theAddrs[i] = accType.X
			case *ssa.MakeMap:
			}
		}
	}
	return theAddrs
}

// sameAddress determines if two addresses have the same global address(for package-level variables only)
func (a *analysis) sameAddress(addr1 ssa.Value, addr2 ssa.Value, go1 int, go2 int) bool {
	if global1, ok1 := addr1.(*ssa.Global); ok1 {
		if global2, ok2 := addr2.(*ssa.Global); ok2 {
			if global1.Pos() == global2.Pos() { // compare position of identifiers
				return true
			}
		}
	} else if freevar1, ok := addr1.(*ssa.FreeVar); ok {
		if freevar2, ok2 := addr2.(*ssa.FreeVar); ok2 {
			if freevar1.Pos() == freevar2.Pos() { // compare position of identifiers
				//bz: check the one with non-0 goID
				var nonZeroGoID int
				if go1 == 0 {
					nonZeroGoID = go2
				} else {
					nonZeroGoID = go1
				}
				if !sliceContainsFreeVar(a.bindingFV[a.RWIns[nonZeroGoID][0].node.(*ssa.Go)], freevar1) {
					return true
				} else {
					return false
				}
			}
		}
	}
	// check points-to set to see if they can point to the same object
	if useDefaultPTA {
		ptsets := a.ptaRes0.Queries
		return ptsets[addr1].PointsTo().Intersects(ptsets[addr2].PointsTo())
	}
	// new PTA
	if go1 == 0 && go2 == 0 { //bz: ??
		ptset1 := a.ptaRes.Queries[addr1]
		ptset2 := a.ptaRes.Queries[addr2]
		for _, ptrCtx1 := range ptset1 {
			for _, ptrCtx2 := range ptset2 {
				if ptrCtx1.MayAlias(ptrCtx2) {
					return true
				}
			}
		}
		return false
	}
	var pt1 pointer.PointerWCtx
	var pt2 pointer.PointerWCtx
	var goIns1 *ssa.Go
	var goIns2 *ssa.Go
	loopID1 := a.loopIDs[go1]
	loopID2 := a.loopIDs[go2]
	if go1 == 0 {
		goIns1 = nil
	} else {
		goIns1 = a.RWIns[go1][0].node.(*ssa.Go)
	}
	pt1 = a.ptaRes.PointsToByGoWithLoopID(addr1, goIns1, loopID1)
	if go2 == 0 {
		goIns2 = nil
	} else {
		goIns2 = a.RWIns[go2][0].node.(*ssa.Go)
	}
	pt2 = a.ptaRes.PointsToByGoWithLoopID(addr2, goIns2, loopID2)
	alias := pt1.MayAlias(pt2)

	if alias {
		alias = a.heuristics(addr1, goIns1, loopID1, addr2, goIns2, loopID2)
	}
	return alias
}

//bz: these are heuristics that cannot always be true -> we may miss races here
func (a *analysis) heuristics(addr1 ssa.Value, ins1 *ssa.Go, id1 int, addr2 ssa.Value, ins2 *ssa.Go, id2 int) bool {
	////array heuristics: if the base vars of these ssa.IndexAddr are declared in a loop with goroutines, assume they cannot be alias
	//idxAccess1, ok1 := addr1.(*ssa.IndexAddr)
	//idxAccess2, ok2 := addr2.(*ssa.IndexAddr)
	//if ok1 && ok2 {
	//	base1 := idxAccess1.X
	//	base2 := idxAccess2.X
	//	baseFn1 := a.getBase(base1)
	//	baseFn2 := a.getBase(base2)
	//
	//}

	return true //same result as before do heuristics
}

//bz: get the fn that declares the base var of val
func (a *analysis) getBase(val ssa.Value) *ssa.Function {
	for (true) {
		switch v := val.(type) {
		case *ssa.IndexAddr:
			val = v.X
		case *ssa.UnOp:
			val = v.X
		case *ssa.FieldAddr:
			val = v.X
		case *ssa.Alloc:
			return v.Parent()
		case *ssa.Parameter:
			return v.Parent()
		default:
			return nil
		}
	}
	return nil
}

// reachable determines if 2 input instructions are connected in the Happens-Before Graph
func (a *analysis) reachable(fromIns ssa.Instruction, fromGo int, toIns ssa.Instruction, toGo int, debug bool) bool {
	////TODO: bz: remove debug if open source
	////TODO: bz: this logic is too ad-hoc, need a new one -> tmp solution
	//fromBlock := fromIns.Block().Index
	//if strings.HasPrefix(fromIns.Block().Comment, "rangeindex") && toIns.Parent() != nil && toIns.Parent().Parent() != nil { // checking both instructions belong to same forloop
	//	if len(toIns.Parent().Parent().Blocks) > fromBlock {
	//		if fromIns.Block().Comment == toIns.Parent().Parent().Blocks[fromBlock].Comment {
	//			return false
	//		}
	//	}
	//}
	fromInsKey := goIns{ins: fromIns, goID: fromGo}
	toInsKey := goIns{ins: toIns, goID: toGo}
	fromNode := a.RWinsMap[fromInsKey] // starting node
	toNode := a.RWinsMap[toInsKey]     // target node

	if debug {
		fmt.Println("Start HBNode: ", fromInsKey.String(), "\nEnd HBNode: ", toInsKey.String())
		if strings.Contains(fromInsKey.String(), "*doStack = false:bool@GoID: 0") {
			fmt.Println()
		}
	}

	//use breadth-first-search to traverse the Happens-Before Graph
	var visited []graph.Node
	q := &queue{}
	q.enQueue(fromNode)
	for !q.isEmpty() {
		for size := q.size(); size > 0; size-- {
			node := q.deQueue()
			if node == toNode {
				if debug {
					tmp := (*node.Value).(goIns)
					fmt.Println("  reach* -> ", (&tmp).String())
				}
				return true
			}
			for _, neighbor := range a.HBgraph.Neighbors(node) {
				if debug {
					tmp := (*neighbor.Value).(goIns)
					fmt.Println("  reach -> ", (&tmp).String())
				}
				if sliceContainsNode(visited, neighbor) {
					continue
				}
				visited = append(visited, neighbor)
				q.enQueue(neighbor)
			}
		}
	}
	return false
}

func sliceContainsNode(slice []graph.Node, node graph.Node) bool {
	for _, n := range slice {
		if n.Value == node.Value {
			return true
		}
	}
	return false
}

// lockSetsIntersect determines if two input instructions are trying to access a variable that is protected by the same set of locks
func (a *analysis) lockSetsIntersect(insA ssa.Instruction, insB ssa.Instruction, goA int, goB int) bool {
	setA := a.lockMap[insA] // lockset of instruction-A
	if a.isReadIns(insA) {
		setA = append(setA, a.RlockMap[insA]...)
	}
	setB := a.lockMap[insB] // lockset of instruction-B
	if a.isReadIns(insB) {
		setB = append(setB, a.RlockMap[insB]...)
	}
	for _, addrA := range setA {
		for _, addrB := range setB {
			if a.sameAddress(addrA, addrB, goA, goB) {
				return true
			} else {
				posA := getSrcPos(addrA)
				posB := getSrcPos(addrB)
				if posA == posB {
					return true
				}
			}
		}
	}
	return false
}

func (a *analysis) bothAtomic(insA ssa.Instruction, insB ssa.Instruction) bool {
	if aCall, ok := insA.(*ssa.Call); ok && aCall.Call.StaticCallee() != nil {
		if bCall, ok0 := insB.(*ssa.Call); ok0 && bCall.Call.StaticCallee() != nil {
			if aCall.Call.StaticCallee().Pkg.Pkg.Name() == "atomic" && bCall.Call.StaticCallee().Pkg.Pkg.Name() == "atomic" {
				return true
			}
		}
	}
	return false
}

func (a *analysis) selectMutEx(insA ssa.Instruction, insB ssa.Instruction) bool {
	if selA, ok1 := a.selectCaseBody[insA]; ok1 {
		if selB, ok2 := a.selectCaseBody[insB]; ok2 {
			return selA == selB
		}
	}
	return false
}

func getSrcPos(address ssa.Value) token.Pos {
	var position token.Pos
	switch param := address.(type) {
	case *ssa.Parameter:
		position = param.Pos()
	case *ssa.FieldAddr:
		position = param.Pos()
	case *ssa.Alloc:
		position = param.Pos()
	case *ssa.FreeVar:
		position = param.Pos()
	}
	return position
}

//func (a *analysis) reportRace

// printRace will print the details of a data race such as the write/read of a variable and other helpful information
func (a *analysis) printRace(counter int, race *raceInfo) {
	insPair := race.insPair
	addrPair := race.addrPair
	goIDs := race.goIDs
	//insInd := race.insInd //bz: not used
	stacks := race.stacks

	log.Printf("Data race #%d", counter)
	log.Println(strings.Repeat("=", 100))
	var writeLocks []ssa.Value
	var readLocks []ssa.Value
	for i, anIns := range insPair {
		var errMsg string
		var access string
		if isWriteIns(anIns) {
			access = "Write of "
			if _, ok := anIns.(*ssa.Call); ok {
				errMsg = fmt.Sprint(access, aurora.Magenta(addrPair[i].String()), " in function ", aurora.BrightGreen(anIns.Parent().Name()), " at ", a.getValueLOC(addrPair[i]))
			} else {
				errMsg = fmt.Sprint(access, aurora.Magenta(addrPair[i].String()), " in function ", aurora.BrightGreen(anIns.Parent().Name()), " at ", a.getInstLOC(insPair[i]))
			}
			writeLocks = a.lockMap[anIns]
		} else {
			access = "Read of "
			if _, ok := anIns.(*ssa.Call); ok {
				errMsg = fmt.Sprint(access, aurora.Magenta(addrPair[i].String()), " in function ", aurora.BrightGreen(anIns.Parent().Name()), " at ", a.getValueLOC(addrPair[i]))
			} else {
				errMsg = fmt.Sprint(access, aurora.Magenta(addrPair[i].String()), " in function ", aurora.BrightGreen(anIns.Parent().Name()), " at ", a.getInstLOC(insPair[i]))
			}
			readLocks = append(a.lockMap[anIns], a.RlockMap[anIns]...)
		}
		if testMode {
			colorOutput := regexp.MustCompile(`\x1b\[\d+m`)
			a.racyStackTops = append(a.racyStackTops, colorOutput.ReplaceAllString(errMsg, ""))
		}
		log.Print(errMsg)

		if goIDs[i] == 0 { // main goroutine
			log.Println("\tin goroutine  ***  main  [", goIDs[i], "] *** ")
		} else {
			log.Println("\tin goroutine  ***", a.goNames(a.goCalls[goIDs[i]]), "[", goIDs[i], "] *** ", a.getInstLOC(a.goCalls[goIDs[i]]))
		}
		if doStack { //bz: only print if true -> for performance purpose
			for p, stack := range stacks[i] {
				everyFn := stack.fn
				invoke := stack.invoke
				if invoke != nil { //main has nil invoke
					log.Println("\t ", strings.Repeat(" ", p), "@", invoke.String(), a.getInstLOC(invoke)) //bz: this is the invoke instruction location
					//log.Println("\t  @", invoke.String(), a.prog.Fset.Position(invoke.Pos())) //bz: this is the invoke instruction location
				}
				log.Println("\t ", strings.Repeat(" ", p), "->", everyFn.Name(), a.getValueLOC(everyFn)) //bz: this is the callee func location
				//log.Println("\t  ->", everyFn.Name(), a.prog.Fset.Position(everyFn.Pos())) //bz: this is the callee func location
			}
		}
		if goIDs[i] > 0 { // subroutines
			log.Debug("call stack: ")
		}
		goID := goIDs[i]
		a.printGoStack(goID)
	}
	log.Println("Locks acquired before Write access: ", writeLocks)
	log.Println("Locks acquired before Read  access: ", readLocks)
	log.Println(strings.Repeat("=", 100))
}

//bz: print the call stack of go routine
func (a *analysis) printGoStack(goID int) {
	var pathGo []int
	for goID > 0 {
		pathGo = append([]int{goID}, pathGo...)
		temp := a.goCaller[goID]
		goID = temp
	}
	if !allEntries {
		for q, eachGo := range pathGo {
			eachStack := a.goStack[eachGo]
			for k, eachFn := range eachStack {
				if k == 0 {
					log.Debug("\t ", strings.Repeat(" ", q), "--> Goroutine: ", eachFn.Name(), "[", a.goCaller[eachGo], "] ", a.getValueLOC(eachFn))
				} else {
					log.Debug("\t   ", strings.Repeat(" ", q), "->", strings.Repeat(" ", k), eachFn.Name(), " ", a.getValueLOC(eachFn))
				}
			}
		}
	}
}

//bz: locations of ssa.Value
func (a *analysis) getValueLOC(val ssa.Value) token.Position {
	return a.prog.Fset.Position(val.Pos())
}

//bz: locations of ssa.Instruction
func (a *analysis) getInstLOC(inst ssa.Instruction) token.Position {
	pos := a.prog.Fset.Position(inst.Pos())
	if testMode {
		return pos
	}
	if !pos.IsValid() {
		switch v := inst.(type) {
		case *ssa.UnOp:
			pos = a.prog.Fset.Position(v.X.Pos())
		case *ssa.Send:
			pos = a.prog.Fset.Position(v.X.Pos())
		case *ssa.Call:
			pos = a.prog.Fset.Position(v.Call.Pos())
		case *ssa.RunDefers:
			if v.Referrers() == nil {
				return pos
			}
			pos = a.prog.Fset.Position((*v.Referrers())[0].Pos())
		case *ssa.Go:
			pos = a.prog.Fset.Position(v.Call.Pos())
		default:
			panic("Handle Me: Cannot find pos: type: " + v.String())
		}
	}
	return pos
}
