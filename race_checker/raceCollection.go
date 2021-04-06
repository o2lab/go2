package main

import (
	"fmt"
	"github.com/logrusorgru/aurora"
	log "github.com/sirupsen/logrus"
	"github.com/twmb/algoimpl/go/graph"
	"github.tamu.edu/April1989/go_tools/go/pointer"
	"github.tamu.edu/April1989/go_tools/go/ssa"
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
				if (i == 0 && ii < a.insDRA) || (channelComm && sliceContainsBloc(a.omitComm, goI.Block())) {
					continue
				}
				for jj, goJ := range a.RWIns[j] {
					if channelComm && sliceContainsBloc(a.omitComm, goJ.Block()) {
						continue
					}
					////!!!! bz: for my debug, please comment off, do not delete
					//fmt.Println("goI: ", goI.String(), " (i: ", i, ")  goJ: ", goJ.String(), " (j: ", j, ")")
					//if strings.Contains(goI.String(), "&t6[t9]") && strings.Contains(goJ.String(), "*checkName") {
					//	fmt.Println() //goI:  &t6[t9]  (i:  1 )  goJ:  *checkName  (j:  3 )
					//}

					if (isWriteIns(goI) && isWriteIns(goJ)) || (isWriteIns(goI) && a.isReadIns(goJ)) || (a.isReadIns(goI) && isWriteIns(goJ)) { // only read and write instructions
						if isLocal(goI) && isLocal(goJ) { // both are locally declared
							continue
						}
						insSlice := []ssa.Instruction{goI, goJ}
						addressPair := a.insAddress(insSlice) // one instruction from each goroutine
						if addressPair[0] == nil || addressPair[1] == nil {
							continue
						}
						////!!!! bz: for my debug, please comment off, do not delete
						//var goIinstr string
						//var goJinstr string
						//if i == 0 {
						//	goIinstr = "main"
						//} else {
						//	goIinstr = a.RWIns[i][0].String()
						//}
						//if j == 0 {
						//	goJinstr = "main"
						//} else {
						//	goJinstr = a.RWIns[j][0].String()
						//}
						//fmt.Println(addressPair[0], " Go: ", goIinstr, " loopid: ", a.loopIDs[i], ";  ", addressPair[1], " Go: ", goJinstr, " loopid: ", a.loopIDs[j])
						//if strings.Contains(addressPair[0].String(), "&fp.numFilterCalled [#0]") &&
						//	strings.Contains(addressPair[1].String(), "&fp.numFilterCalled [#0]") {
						//	fmt.Println()
						//}
						if a.sameAddress(addressPair[0], addressPair[1], i, j) &&
							!sliceContains(a.reportedAddr, addressPair[0]) &&
							!a.reachable(goI, i, goJ, j) &&
							!a.reachable(goJ, j, goI, i) &&
							!a.bothAtomic(insSlice[0], insSlice[1]) &&
							!a.lockSetsIntersect(goI, goJ, i, j) &&
							!a.selectMutEx(insSlice[0], insSlice[1]) {
							a.reportedAddr = append(a.reportedAddr, addressPair[0])
							ri := &raceInfo{
								insPair: 	insSlice,
								addrPair: 	addressPair,
								goIDs: 		[]int{i, j},
								insInd: 	[]int{ii, jj},
							}
							if !allEntries {
								a.printRace(len(a.reportedAddr), insSlice, addressPair, []int{i, j}, []int{ii, jj})
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
			if global1.Pos() == global2.Pos() {// compare position of identifiers
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
				if !sliceContainsFreeVar(a.bindingFV[a.RWIns[nonZeroGoID][0].(*ssa.Go)], freevar1) {
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
	if go1 == 0 && go2 == 0 {
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
	if go1 == 0 {
		pt1 = a.ptaRes.PointsToByGoWithLoopID(addr1, nil, a.loopIDs[go1])
	} else {
		pt1 = a.ptaRes.PointsToByGoWithLoopID(addr1, a.RWIns[go1][0].(*ssa.Go), a.loopIDs[go1])
	}
	if go2 == 0 {
		pt2 = a.ptaRes.PointsToByGoWithLoopID(addr2, nil, a.loopIDs[go2])
	} else {
		pt2 = a.ptaRes.PointsToByGoWithLoopID(addr2, a.RWIns[go2][0].(*ssa.Go), a.loopIDs[go2])
	}
	return pt1.MayAlias(pt2)
}

// reachable determines if 2 input instructions are connected in the Happens-Before Graph
func (a *analysis) reachable(fromIns ssa.Instruction, fromGo int, toIns ssa.Instruction, toGo int) bool {
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

	//use breadth-first-search to traverse the Happens-Before Graph
	var visited []graph.Node
	q := &queue{}
	q.enQueue(fromNode)
	for !q.isEmpty() {
		for size := q.size(); size > 0; size-- {
			node := q.deQueue()
			if node == toNode {
				return true
			}
			for _, neighbor := range a.HBgraph.Neighbors(node) {
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
		if bCall, ok0 := insB.(*ssa.Call); ok0 && bCall.Call.StaticCallee() != nil{
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
func (a *analysis) printRace(counter int, insPair []ssa.Instruction, addrPair [2]ssa.Value, goIDs []int, insInd []int) {
	log.Printf("Data race #%d", counter)
	log.Println(strings.Repeat("=", 100))
	var writeLocks []ssa.Value
	var readLocks []ssa.Value
	for i, anIns := range insPair {
		var errMsg string
		var access string
		if isWriteIns(anIns) {
			access = " Write of "
			if _, ok := anIns.(*ssa.Call); ok {
				errMsg = fmt.Sprint(access, aurora.Magenta(addrPair[i].String()), " in function ", aurora.BrightGreen(anIns.Parent().Name()), " at ", a.getLocation(addrPair[i]))
			} else {
				errMsg = fmt.Sprint(access, aurora.Magenta(addrPair[i].String()), " in function ", aurora.BrightGreen(anIns.Parent().Name()), " at ", a.getLocation2(insPair[i]))
			}
			writeLocks = a.lockMap[anIns]
		} else {
			access = " Read of "
			errMsg = fmt.Sprint(access, aurora.Magenta(addrPair[i].String()), " in function ", aurora.BrightGreen(anIns.Parent().Name()), " at ", a.getLocation2(anIns))
			readLocks = append(a.lockMap[anIns], a.RlockMap[anIns]...)
		}
		if testMode {
			colorOutput := regexp.MustCompile(`\x1b\[\d+m`)
			a.racyStackTops = append(a.racyStackTops, colorOutput.ReplaceAllString(errMsg, ""))
		}
		log.Print(errMsg)
		var printStack []string // store functions in stack and pop terminated functions
		fnPos := make(map[string]token.Pos)
		for p, everyIns := range a.RWIns[goIDs[i]] {
			if p < insInd[i]-1 {
				if isFunc, ok := everyIns.(*ssa.Call); ok {
					funcName := checkTokenName(isFunc.Call.Value.Name(), everyIns.(*ssa.Call))
					printStack = append(printStack, funcName)
					fnPos[funcName] = everyIns.Pos()
				} else if aFunc, ok1 := everyIns.(*ssa.Return); ok1 && len(printStack) > 0 {
					funcName := aFunc.Parent().Name()
					ii := len(printStack)-1
					for ii >= 0 && funcName != printStack[ii] {
						ii--
					}
					if ii > -1  {
						printStack = printStack[:ii]
					}
					if _, ok2 := fnPos[funcName]; ok2 {
						delete(fnPos, funcName)
					}
				}
			} else {
				break
			}
		}

		if goIDs[i] == 0 { // main goroutine
			log.Println("\tin goroutine  ***  main  [", goIDs[i], "] *** ")
		} else {
			log.Println("\tin goroutine  ***", a.goNames(a.goCalls[goIDs[i]]), "[", goIDs[i], "] *** ", a.prog.Fset.Position(a.goCalls[goIDs[i]].Pos()))
		}
		if len(printStack) > 0 {
			for p, toPrint := range printStack {
				log.Println("\t ", strings.Repeat(" ", p), toPrint, a.prog.Fset.Position(fnPos[toPrint]))
			}
		}
	}
	log.Println("Locks acquired before Write access: ", writeLocks)
	log.Println("Locks acquired before Read  access: ", readLocks)
	log.Println(strings.Repeat("=", 100))
}

//bz: i want all locations
func (a *analysis) getLocation(val ssa.Value) token.Position {
	return a.prog.Fset.Position(val.Pos())
}

func (a *analysis) getLocation2(inst ssa.Instruction) token.Position {
	return a.prog.Fset.Position(inst.Pos())
}

func (a *analysis) getLocation3(tok token.Pos) token.Position {
	return a.prog.Fset.Position(tok)
}