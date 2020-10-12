package main

import (
	"fmt"
	"github.com/logrusorgru/aurora"
	log "github.com/sirupsen/logrus"
	"go/token"
	"golang.org/x/tools/go/ssa"
	"regexp"
	"strings"
)

// checkRacyPairs determines how many data races are present(race must access variable in at least 2 goroutines and one instruction must be a write)
func (a *analysis) checkRacyPairs() {
	for i := 0; i < len(a.RWIns); i++ {
		for j := i + 1; j < len(a.RWIns); j++ { // must be in different goroutines, j always greater than i
			for ii, goI := range a.RWIns[i] {
				if i == 0 && ii < a.insDRA {
					continue // do not check race-free instructions in main goroutine
				}
				for jj, goJ := range a.RWIns[j] {
					if (isWriteIns(goI) && isWriteIns(goJ)) || (isWriteIns(goI) && isReadIns(goJ)) || (isReadIns(goI) && isWriteIns(goJ)) { // only read and write instructions
						insSlice := []ssa.Instruction{goI, goJ}
						addressPair := a.insAddress(insSlice) // one instruction from each goroutine
						log.Println(insSlice)
						log.Println(!a.reachable(goI, goJ) )
						log.Println(!a.reachable(goJ, goI) )
						if len(addressPair) > 1 && a.sameAddress(addressPair[0], addressPair[1]) && !sliceContains(a.reportedAddr, addressPair[0]) && !a.lockSetsIntersect(insSlice[0], insSlice[1]) && !a.chanProtected(insSlice[0], insSlice[1]) {
							if (isWriteIns(goI) && isReadIns(goJ)) && !a.reachable(goI, goJ) {
								a.reportedAddr = append(a.reportedAddr, addressPair[0])
								a.printRace(len(a.reportedAddr), insSlice, addressPair, []int{i, j}, []int{ii, jj})
							}else{
								if (isReadIns(goI) && isWriteIns(goJ)) && !a.reachable(goJ,goI) {
									a.reportedAddr = append(a.reportedAddr, addressPair[0])
									a.printRace(len(a.reportedAddr), insSlice, addressPair, []int{i, j}, []int{ii, jj})
								}else{
									if (isWriteIns(goI) && isWriteIns(goJ)) && !a.reachable(goJ,goI) && !a.reachable(goI, goJ){
										a.reportedAddr = append(a.reportedAddr, addressPair[0])
										a.printRace(len(a.reportedAddr), insSlice, addressPair, []int{i, j}, []int{ii, jj})
									}
								}
							}
						}
					}
				}
			}
		}
	}
	if len(a.reportedAddr) > 1 {
		log.Println("Done  -- ", len(a.reportedAddr), "races found! ")
	} else { // singular data race
		log.Println("Done  -- ", len(a.reportedAddr), "race found! ")
	}
}

// insAddress takes a slice of ssa instructions and returns a slice of their corresponding addresses
func (a *analysis) insAddress(insSlice []ssa.Instruction) []ssa.Value { // obtain addresses of instructions
	theAddrs := []ssa.Value{}
	for _, anIns := range insSlice {
		switch theIns := anIns.(type) {
		case *ssa.Store: // write
			theAddrs = append(theAddrs, theIns.Addr)
		case *ssa.Call:
			if theIns.Call.Value.Name() == "delete" { // write
				theAddrs = append(theAddrs, theIns.Call.Args[0].(*ssa.UnOp).X)
			} else if strings.HasPrefix(theIns.Call.Value.Name(), "Add") && theIns.Call.StaticCallee().Pkg.Pkg.Name() == "atomic" { // write
				theAddrs = append(theAddrs, theIns.Call.Args[0].(*ssa.FieldAddr).X)
			} else if len(theIns.Call.Args) > 0 { // read
				for _, anArg := range theIns.Call.Args {
					if readAcc, ok := anArg.(*ssa.FieldAddr); ok {
						theAddrs = append(theAddrs, readAcc.X)
					}
				}
			}
		case *ssa.UnOp: // read
			theAddrs = append(theAddrs, theIns.X)
		case *ssa.Lookup: // read
			theAddrs = append(theAddrs, theIns.X)
		case *ssa.FieldAddr: // read
			theAddrs = append(theAddrs, theIns.X)
		case *ssa.MapUpdate: // write
			switch accType := theIns.Map.(type) {
			case *ssa.UnOp:
				theAddrs = append(theAddrs, accType.X)
			case *ssa.MakeMap:
			}
		}
	}
	return theAddrs
}

// sameAddress determines if two addresses have the same global address(for package-level variables only)
func (a *analysis) sameAddress(addr1 ssa.Value, addr2 ssa.Value) bool {
	if global1, ok1 := addr1.(*ssa.Global); ok1 {
		if global2, ok2 := addr2.(*ssa.Global); ok2 {
			return global1.Pos() == global2.Pos() // compare position of identifiers
		}
	} else if freevar1, ok1 := addr1.(*ssa.FreeVar); ok1 {
		if freevar2, ok2 := addr2.(*ssa.FreeVar); ok2 {
			return freevar1.Pos() == freevar2.Pos() // compare position of identifiers
		}
	}

	// check points-to set to see if they can point to the same object
	ptset := a.result.Queries
	return ptset[addr1].PointsTo().Intersects(ptset[addr2].PointsTo())
}

// reachable determines if 2 input instructions are connected in the Happens-Before Graph
func (a *analysis) reachable(fromIns ssa.Instruction, toIns ssa.Instruction) bool {
	fromBlock := fromIns.Block().Index
	if strings.HasPrefix(fromIns.Block().Comment, "rangeindex") && toIns.Parent() != nil && toIns.Parent().Parent() != nil { // checking both instructions belong to same forloop
		if fromIns.Block().Comment == toIns.Parent().Parent().Blocks[fromBlock].Comment {
			return false
		}
	}
	fromNode := a.RWinsMap[fromIns]
	toNode := a.RWinsMap[toIns]
	nexts := a.HBgraph.Neighbors(fromNode) // get all reachable nodes
	counter := 0
	for len(nexts) > 0 {
		if counter == 10000 {
			break
		}
		curr := nexts[len(nexts)-1]
		nexts = nexts[:len(nexts)-1]
		next := a.HBgraph.Neighbors(curr)
		nexts = append(nexts, next...)
		if curr == toNode {
			return true
		}
		counter++
	}
	return false
}

// lockSetsIntersect determines if two input instructions are trying to access a variable that is protected by the same set of locks
func (a *analysis) lockSetsIntersect(insA ssa.Instruction, insB ssa.Instruction) bool {
	setA := a.lockMap[insA] // lockset of instruction-A
	if isReadIns(insA) {
		setA = append(setA, a.RlockMap[insA]...)
	}
	setB := a.lockMap[insB] // lockset of instruction-B
	if isReadIns(insB) {
		setB = append(setB, a.RlockMap[insB]...)
	}
	for _, addrA := range setA {
		for _, addrB := range setB {
			if a.sameAddress(addrA, addrB) {
				return true
			} else {
				if param1, ok1 := addrA.(*ssa.Parameter); ok1 {
					if param2, ok2 := addrB.(*ssa.Parameter); ok2 {
						if param1.Pos() == param2.Pos() {
							return true
						}
					}
				} else if param1, ok1 := addrA.(*ssa.FieldAddr); ok1 {
					if param2, ok2 := addrB.(*ssa.FieldAddr); ok2 {
						if param1.Pos() == param2.Pos() {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

// chanProtected will determine if 2 input instructions are both using the same channel
func (a *analysis) chanProtected(insA ssa.Instruction, insB ssa.Instruction) bool {
	setA := a.chanMap[insA] // channelSet of instruction-A
	setB := a.chanMap[insB] // channelSet of instruction-B
	for _, chanA := range setA {
		for _, chanB := range setB {
			if chanA == chanB {
				return true
			}
		}
	}
	return false
}

// printRace will print the details of a data race such as the write/read of a variable and other helpful information
func (a *analysis) printRace(counter int, insPair []ssa.Instruction, addrPair []ssa.Value, goIDs []int, insInd []int) {
	log.Printf("Data race #%d", counter)
	log.Println(strings.Repeat("=", 100))
	for i, anIns := range insPair {
		var errMsg string
		var access string
		if isWriteIns(anIns) {
			if i == 0 {
				access = " Previous Write of "
			} else {
				access = " Write of "
			}
			if _, ok := anIns.(*ssa.Call); ok {
				errMsg = fmt.Sprint(access, aurora.Magenta(addrPair[i].String()), " in function ", aurora.BgBrightGreen(anIns.Parent().Name()), " at ", a.prog.Fset.Position(addrPair[i].Pos()))
			} else {
				errMsg = fmt.Sprint(access, aurora.Magenta(addrPair[i].String()), " in function ", aurora.BgBrightGreen(anIns.Parent().Name()), " at ", a.prog.Fset.Position(insPair[i].Pos()))
			}
		} else {
			if i == 0 {
				access = " Previous Read of "
			} else {
				access = " Read of "
			}
			errMsg = fmt.Sprint(access, aurora.Magenta(addrPair[i].String()), " in function ", aurora.BgBrightGreen(anIns.Parent().Name()), " at ", a.prog.Fset.Position(anIns.Pos()))
		}
		if testMode {
			colorOutput := regexp.MustCompile(`\x1b\[\d+m`)
			a.racyStackTops = append(a.racyStackTops, colorOutput.ReplaceAllString(errMsg, ""))
		}
		log.Print(errMsg)
		var printStack []string
		var printPos []token.Pos
		for p, everyIns := range a.RWIns[goIDs[i]] {
			if p < insInd[i]-1 {
				if isFunc, ok := everyIns.(*ssa.Call); ok {
					printName := isFunc.Call.Value.Name()
					printName = checkTokenName(printName, everyIns.(*ssa.Call))
					printStack = append(printStack, printName)
					printPos = append(printPos, everyIns.Pos())
				} else if _, ok1 := everyIns.(*ssa.Return); ok1 && len(printStack) > 0 { // TODO: need to consider function with multiple return statements
					printStack = printStack[:len(printStack)-1]
					printPos = printPos[:len(printPos)-1]
				}
			} else {
				continue
			}
		}
		if len(printStack) > 0 {
			log.Println("\tcalled by function[s]: ")
			for p, toPrint := range printStack {
				log.Println("\t ", strings.Repeat(" ", p), toPrint, a.prog.Fset.Position(printPos[p]))
			}
		}
		log.Println("\tin goroutine  ***", a.goNames[goIDs[i]], "[", goIDs[i], "] *** , with the following call stack: ")
		var pathGo []int
		j := goIDs[i]
		for j > 0 {
			pathGo = append([]int{j}, pathGo...)
			temp := a.goCaller[j]
			j = temp
		}
		for q, eachGo := range pathGo {
			eachStack := a.goStack[eachGo]
			for k, eachFn := range eachStack {
				if k == 0 {
					log.Println("\t ", strings.Repeat(" ", q), "--> Goroutine: ", eachFn, "[", a.goCaller[eachGo], "]")
				} else {
					log.Println("\t   ", strings.Repeat(" ", q), strings.Repeat(" ", k), eachFn)
				}
			}
		}
	}
	log.Println(strings.Repeat("=", 100))
}
