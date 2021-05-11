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

func (a *analysis) canRunInParallel(goID1 int, goID2 int) bool {
	paths := [2][]int{}         // thread traversal
	stacks := [2][]fnCallInfo{} // fn traversal
	goIDs := []int{goID1, goID2}
	divFn := fnCallInfo{nil, nil} // fn where divergence happens
	for i, ID := range goIDs {    // for each thread
		for ID > 0 { // traverse up the call chain
			paths[i] = append([]int{ID}, paths[i]...) // prepend
			temp := a.goCaller[ID]
			ID = temp
		}
		for _, eachGo := range paths[i] { // concatenate call chain from each thread
			stacks[i] = append(stacks[i], a.goStack[eachGo]...)
		}
	}
	var b1, b2 *ssa.BasicBlock
	for j, fn := range stacks[0] { // take one of the two call stacks
		if j == 0 { // share main entry
			if j == len(stacks[0])-1 || j == len(stacks[1])-1 {
				return true
			}
			continue
		}
		if fn == stacks[1][j] { // common call
			if j == len(stacks[0])-1 { // end of this stack reached
				return true
			} else if j == len(stacks[0])-1 { // end of other stack reached
				return true
			}
		} else { // divergence happened
			divFn = stacks[0][j-1] // examine caller function
			b1 = stacks[0][j].ssaIns.Block()
			b2 = stacks[1][j].ssaIns.Block()
			break
		}
	}

	if b1 != nil && b2 != nil && b1.Parent() == divFn.fnIns && b2.Parent() == divFn.fnIns &&
		!b1.Dominates(b2) && !b2.Dominates(b1) {
		return false
	}
	return true
}

func (a *analysis) mutuallyExcluded(goI ssa.Instruction, I int, goJ ssa.Instruction, J int) bool {
	paths := [2][]int{}         // thread traversal
	stacks := [2][]fnCallInfo{} // fn traversal
	goIDs := []int{I, J}
	insPair := []ssa.Instruction{goI, goJ}
	divFn := fnCallInfo{nil, nil} // fn where divergence happens
	for i, ID := range goIDs {    // for each thread
		for ID > 0 { // traverse up the call chain
			paths[i] = append([]int{ID}, paths[i]...) // prepend
			temp := a.goCaller[ID]
			ID = temp
		}
		for _, eachGo := range paths[i] { // concatenate call chain from each thread
			stacks[i] = append(stacks[i], a.goStack[eachGo][:len(a.goStack[eachGo])-1]...)
		}
		fnCall := fnCallIns{insPair[i].Parent(), goIDs[i]}
		stacks[i] = append(stacks[i], a.stackMap[fnCall].fnCalls...)
	}
	var divFnAt int
	var b1, b2 *ssa.BasicBlock
	for j, fn := range stacks[0] { // take one of the two call stacks
		if j == 0 { // share main entry
			if j == len(stacks[0])-1 { // this access is in main function
				divFn = fn // divergence happened in main
				divFnAt = j
				b1 = goI.Block()
				b2 = stacks[1][j+1].ssaIns.Block()
				if deferIns, isDefer := stacks[1][j+1].ssaIns.(*ssa.Defer); isDefer && a.deferToRet[deferIns] != nil {
					b2 = a.deferToRet[deferIns].Block()
				}
				break
			} else if j == len(stacks[1])-1 {
				divFn = fn
				divFnAt = j
				b2 = goJ.Block()
				b1 = stacks[0][j+1].ssaIns.Block()
				if deferIns, isDefer := stacks[0][j+1].ssaIns.(*ssa.Defer); isDefer && a.deferToRet[deferIns] != nil {
					b1 = a.deferToRet[deferIns].Block()
				}
				break
			}
		}
		if j == len(stacks[0])-1 { // end of this stack reached
			if fn == stacks[1][j] { // common call
				divFn = fn
				divFnAt = j
				b1 = goI.Block()
				if j == len(stacks[1])-1 { // same access in different iterations of loop
					return false
				} else {
					b2 = stacks[1][j+1].ssaIns.Block()
					if deferIns, isDefer := stacks[1][j+1].ssaIns.(*ssa.Defer); isDefer && a.deferToRet[deferIns] != nil {
						b2 = a.deferToRet[deferIns].Block()
					}
				}
			} else { // divergence happened
				divFn = stacks[0][j-1] // examine caller function
				divFnAt = j - 1
				b1 = stacks[0][j].ssaIns.Block()
				if deferIns, isDefer := stacks[0][j].ssaIns.(*ssa.Defer); isDefer && a.deferToRet[deferIns] != nil {
					b1 = a.deferToRet[deferIns].Block()
				}
				b2 = stacks[1][j].ssaIns.Block()
				if deferIns, isDefer := stacks[1][j].ssaIns.(*ssa.Defer); isDefer && a.deferToRet[deferIns] != nil {
					b2 = a.deferToRet[deferIns].Block()
				}
				break
			}
		} else if j == len(stacks[1])-1 { // end of other stack reached
			if fn == stacks[1][j] { // common call
				divFn = fn
				divFnAt = j
				b2 = goJ.Block()
				b1 = stacks[0][j+1].ssaIns.Block()
				if deferIns, isDefer := stacks[0][j+1].ssaIns.(*ssa.Defer); isDefer && a.deferToRet[deferIns] != nil {
					b1 = a.deferToRet[deferIns].Block()
				}
				break
			} else { // divergence happened
				divFn = stacks[0][j-1] // examine caller function
				divFnAt = j - 1
				b1 = stacks[0][j].ssaIns.Block()
				if deferIns, isDefer := stacks[0][j].ssaIns.(*ssa.Defer); isDefer && a.deferToRet[deferIns] != nil {
					b1 = a.deferToRet[deferIns].Block()
				}
				b2 = stacks[1][j].ssaIns.Block()
				if deferIns, isDefer := stacks[1][j].ssaIns.(*ssa.Defer); isDefer && a.deferToRet[deferIns] != nil {
					b2 = a.deferToRet[deferIns].Block()
				}
				break
			}
		} else if fn != stacks[1][j] { // mid-stack divergence for both threads
			divFn = stacks[0][j-1] // examine caller function
			divFnAt = j - 1
			b1 = stacks[0][j].ssaIns.Block()
			if deferIns, isDefer := stacks[0][j].ssaIns.(*ssa.Defer); isDefer {
				b1 = a.deferToRet[deferIns].Block()
			}
			b2 = stacks[1][j].ssaIns.Block()
			if deferIns, isDefer := stacks[1][j].ssaIns.(*ssa.Defer); isDefer && a.deferToRet[deferIns] != nil {
				b2 = a.deferToRet[deferIns].Block()
			}
			break
		} // otherwise it's common fn call mid-stack
	}
	if b1 != nil && b2 != nil && b1.Parent() == divFn.fnIns && b2.Parent() == divFn.fnIns {
		if !b1.Dominates(b2) && !b2.Dominates(b1) {
			return true
		} else if noGoAfterFn(stacks[0], divFnAt) && b1.Index != b2.Index && b1.Dominates(b2) {
			return true
		}
	}
	if b1 != nil && (strings.Contains(b1.Comment, "if.then") || strings.Contains(b1.Comment, "if.else")) {
		if _, isRet := b1.Instrs[len(b1.Instrs)-1].(*ssa.Return); isRet && b1.Dominates(b2) {
			if !stackContainsDefer(stacks[0]) {
				return true
			}
		}
	}
	if b2 != nil && (strings.Contains(b2.Comment, "if.then") || strings.Contains(b2.Comment, "if.else")) {
		if _, isRet1 := b2.Instrs[len(b2.Instrs)-1].(*ssa.Return); isRet1 && b2.Dominates(b1) {
			if !stackContainsDefer(stacks[1]) {
				return true
			}
		}
	}
	return false
}

// checkRacyPairs checks accesses among two concurrent goroutines
func (a *analysis) checkRacyPairs() []*raceInfo {
	var races []*raceInfo
	var ri *raceInfo
	for i := 0; i < len(a.RWIns); i++ {
		//if i != 0 { // for debugging
		//	continue
		//}
		for j := i + 1; j < len(a.RWIns); j++ { // must be in different goroutines, j always greater than i
			//if a.goNames(a.RWIns[j][0].(*ssa.Go)) != "testMetadataStreamingRPC$1" { // for debugging
			//	continue
			//}
			if !a.canRunInParallel(i, j) {
				continue
			}
			for ii, goI := range a.RWIns[i] {
				if (i == 0 && ii < a.insDRA) || (channelComm && sliceContainsBloc(a.omitComm, goI.Block())) {
					continue
				}
				for jj, goJ := range a.RWIns[j] {
					if channelComm && sliceContainsBloc(a.omitComm, goJ.Block()) {
						continue
					}
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
						//if strings.Contains(addressPair[0].String(), "returnBuffers") && strings.Contains(addressPair[1].String(), "returnBuffers") &&
						//	goI.Parent().Name() == "commitAttemptLocked" && goJ.Parent().Name() == "SendMsg" {
						//	fmt.Println(addressPair[0], " Go: ", goIinstr, " loopid: ", a.loopIDs[i], ";  ", addressPair[1], " Go: ", goJinstr, " loopid: ", a.loopIDs[j])
						//}

						if a.sameAddress(addressPair[0], addressPair[1], i, j) &&
							!sliceContains(a.reportedAddr, addressPair[0]) &&
							!a.reachable(goI, i, goJ, j) &&
							!a.reachable(goJ, j, goI, i) &&
							!a.bothAtomic(insSlice[0], insSlice[1]) &&
							!a.lockSetsIntersect(goI, goJ, i, j) &&
							!a.selectMutEx(insSlice[0], insSlice[1]) &&
							!a.mutuallyExcluded(goI, i, goJ, j) {
							a.reportedAddr = append(a.reportedAddr, addressPair[0])
							ri = &raceInfo{
								insPair:  insSlice,
								addrPair: addressPair,
								goIDs:    []int{i, j},
								insInd:   []int{ii, jj},
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
			if len(theIns.Call.Args) > 0 {
				if writeArg, ok := theIns.Call.Args[0].(*ssa.UnOp); ok && theIns.Call.Value.Name() == "delete" { // write
					theAddrs[i] = writeArg.X
				} else if writeArg1, ok1 := theIns.Call.Args[0].(*ssa.FieldAddr); ok1 && strings.HasPrefix(theIns.Call.Value.Name(), "Add") && theIns.Call.StaticCallee().Pkg.Pkg.Name() == "atomic" { // write
					theAddrs[i] = writeArg1.X
				} else { // read
					for _, anArg := range theIns.Call.Args {
						if readAcc, ok2 := anArg.(*ssa.FieldAddr); ok2 {
							theAddrs[i] = readAcc.X
						}
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
				if go1 != 0 && !sliceContainsFreeVar(a.bindingFV[a.RWIns[go1][0].(*ssa.Go)], freevar1) {
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
		ptset1 := a.ptaRes[a.main].Queries[addr1]
		ptset2 := a.ptaRes[a.main].Queries[addr2]
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
		pt1 = a.ptaRes[a.main].PointsToByGoWithLoopID(addr1, nil, a.loopIDs[go1])
	} else {
		pt1 = a.ptaRes[a.main].PointsToByGoWithLoopID(addr1, a.RWIns[go1][0].(*ssa.Go), a.loopIDs[go1])
	}
	if go2 == 0 {
		pt2 = a.ptaRes[a.main].PointsToByGoWithLoopID(addr2, nil, a.loopIDs[go2])
	} else {
		pt2 = a.ptaRes[a.main].PointsToByGoWithLoopID(addr2, a.RWIns[go2][0].(*ssa.Go), a.loopIDs[go2])
	}
	return pt1.MayAlias(pt2)
}

// reachable determines if 2 input instructions are connected in the Happens-Before Graph
func (a *analysis) reachable(fromIns ssa.Instruction, fromGo int, toIns ssa.Instruction, toGo int) bool {
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
				errMsg = fmt.Sprint(access, aurora.Magenta(addrPair[i].String()), " in function ", aurora.BrightGreen(anIns.Parent().Name()), " at ", a.prog.Fset.Position(addrPair[i].Pos()))
			} else {
				errMsg = fmt.Sprint(access, aurora.Magenta(addrPair[i].String()), " in function ", aurora.BrightGreen(anIns.Parent().Name()), " at ", a.prog.Fset.Position(insPair[i].Pos()))
			}
			writeLocks = a.lockMap[anIns]
		} else {
			access = " Read of "
			errMsg = fmt.Sprint(access, aurora.Magenta(addrPair[i].String()), " in function ", aurora.BrightGreen(anIns.Parent().Name()), " at ", a.prog.Fset.Position(anIns.Pos()))
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
			log.Println("\tin goroutine  ***", a.goNames(a.goCalls[goIDs[i]].goIns), "[", goIDs[i], "] *** ")
		}
		var pathGo []int
		goID := goIDs[i]
		for goID > 0 {
			pathGo = append([]int{goID}, pathGo...)
			temp := a.goCaller[goID]
			goID = temp
		}
		if !allEntries {
			for q, eachGo := range pathGo {
				eachStack := a.goStack[eachGo][:len(a.goStack[eachGo])-1]
				for k, eachFn := range eachStack {
					if k == 0 {
						if eachFn.ssaIns == nil {
							log.Println("\t ", strings.Repeat(" ", q), "--> Goroutine: ", eachFn.fnIns.Name(), "[", a.goCaller[eachGo], "] ", a.prog.Fset.Position(eachFn.fnIns.Pos()))
						} else {
							log.Println("\t ", strings.Repeat(" ", q), "--> Goroutine: ", eachFn.fnIns.Name(), "[", a.goCaller[eachGo], "] ", a.prog.Fset.Position(eachFn.ssaIns.Pos()))
						}
					} else {
						if eachFn.ssaIns == nil {
							log.Println("\t   ", strings.Repeat(" ", q), strings.Repeat(" ", k), eachFn.fnIns.Name(), " ", a.prog.Fset.Position(eachFn.fnIns.Pos()))
						} else {
							log.Println("\t   ", strings.Repeat(" ", q), strings.Repeat(" ", k), eachFn.fnIns.Name(), " ", a.prog.Fset.Position(eachFn.ssaIns.Pos()))
						}

					}
				}
			}
			fnCall := fnCallIns{insPair[i].Parent(), goIDs[i]}
			for p, everyFn := range a.stackMap[fnCall].fnCalls {
				if everyFn.ssaIns == nil {
					if p == 0 {
						log.Println("\t ", strings.Repeat(" ", p+len(pathGo)), "--> Goroutine: ", everyFn.fnIns.Name(), "[", goIDs[i], "] ", a.prog.Fset.Position(everyFn.fnIns.Pos()))
					} else {
						log.Println("\t ", strings.Repeat(" ", p+len(pathGo)), everyFn.fnIns.Name(), a.prog.Fset.Position(everyFn.fnIns.Pos()))
					}
				} else {
					if p == 0 {
						log.Println("\t ", strings.Repeat(" ", p+len(pathGo)), "--> Goroutine: ", everyFn.fnIns.Name(), "[", goIDs[i], "] ", a.prog.Fset.Position(everyFn.ssaIns.Pos()))
					} else {
						log.Println("\t ", strings.Repeat(" ", p+len(pathGo)), everyFn.fnIns.Name(), a.prog.Fset.Position(everyFn.ssaIns.Pos()))
					}
				}
			}
		}
	}
	log.Println("Locks acquired before Write access: ", writeLocks)
	log.Println("Locks acquired before Read  access: ", readLocks)
	log.Println(strings.Repeat("=", 100))
}
