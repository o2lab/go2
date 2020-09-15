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

func (a *analysis) checkRacyPairs() {
	counter := 0 // initialize race counter
	for i := 0; i < len(RWIns); i++ {
		for j := i + 1; j < len(RWIns); j++ { // must be in different goroutines, j always greater than i
			for ii, goI := range RWIns[i] {
				for jj, goJ := range RWIns[j] {
					insSlice := []ssa.Instruction{goI, goJ} // one instruction from each goroutine
					addressPair := a.insAddress(insSlice)
					if len(addressPair) > 1 && a.sameAddress(addressPair[0], addressPair[1]) && !sliceContains(reportedAddr, addressPair[0]) && !a.reachable(goI, goJ) && !a.locksetsInterset(insSlice[0], insSlice[1]) && !a.chanProtected(insSlice[0], insSlice[1]) {
						reportedAddr = append(reportedAddr, addressPair[0])
						counter++
						goIDs := []int{i, j}    // store goroutine IDs
						insInd := []int{ii, jj} // store index of instruction within worker goroutine
						a.printRace(counter, insSlice, addressPair, goIDs, insInd)
					}
				}
			}
		}
	}
	if counter > 1 {
		log.Println("Done  -- ", counter, "races found! ")
	} else {
		log.Println("Done  -- ", counter, "race found! ")
	}
}

func (a *analysis) insAddress(insSlice []ssa.Instruction) []ssa.Value { // obtain addresses of instructions
	minWrite := 0 // at least one write access
	theAddrs := []ssa.Value{}
	for _, anIns := range insSlice {
		switch theIns := anIns.(type) {
		case *ssa.Store:
			minWrite++
			theAddrs = append(theAddrs, theIns.Addr)
		case *ssa.Call:
			if theIns.Call.Value.Name() == "delete" {
				minWrite++
				theAddrs = append(theAddrs, theIns.Call.Args[0].(*ssa.UnOp).X)
			} else if strings.HasPrefix(theIns.Call.Value.Name(), "Add") && theIns.Call.StaticCallee().Pkg.Pkg.Name() == "atomic" {
				minWrite++
				theAddrs = append(theAddrs, theIns.Call.Args[0].(*ssa.FieldAddr).X)
			} else if len(theIns.Call.Args) > 0 {
				for _, anArg := range theIns.Call.Args {
					if readAcc, ok := anArg.(*ssa.FieldAddr); ok {
						theAddrs = append(theAddrs, readAcc.X)
					}
				}
			}
		case *ssa.UnOp:
			theAddrs = append(theAddrs, theIns.X)
		case *ssa.Lookup:
			theAddrs = append(theAddrs, theIns.X)
		case *ssa.FieldAddr:
			theAddrs = append(theAddrs, theIns.X)
		}
	}
	if minWrite > 0 && len(theAddrs) > 1 {
		return theAddrs // write op always before read op
	}
	return []ssa.Value{}
}

func (a *analysis) sameAddress(addr1 ssa.Value, addr2 ssa.Value) bool {
	// check if both are the same global address. for package-level variables only.
	if global1, ok1 := addr1.(*ssa.Global); ok1 {
		if global2, ok2 := addr2.(*ssa.Global); ok2 {
			return global1.Pos() == global2.Pos() // compare position of identifiers
		}
	}

	// check points-to set to see if they can point to the same object
	ptset := a.result.Queries
	return ptset[addr1].PointsTo().Intersects(ptset[addr2].PointsTo())
}

func (a *analysis) reachable(fromIns ssa.Instruction, toIns ssa.Instruction) bool {
	fromBlock := fromIns.Block().Index
	if strings.HasPrefix(fromIns.Block().Comment, "rangeindex") { // if checking in a forloop
		if fromIns.Block().Comment == toIns.Parent().Parent().Blocks[fromBlock].Comment {
			return false
		}
	}
	fromNode := a.RWinsMap[fromIns]
	toNode := a.RWinsMap[toIns]
	nexts := a.HBgraph.Neighbors(fromNode)
	for len(nexts) > 0 {
		curr := nexts[len(nexts)-1]
		nexts = nexts[:len(nexts)-1]
		next := a.HBgraph.Neighbors(curr)
		nexts = append(nexts, next...)
		if curr == toNode {
			return true
		}
	}
	return false
}

func (a *analysis) locksetsInterset(insA ssa.Instruction, insB ssa.Instruction) bool {
	setA := lockMap[insA] // lockset of instruction-A
	setB := lockMap[insB] // lockset of instruction-B
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

func (a *analysis) chanProtected(insA ssa.Instruction, insB ssa.Instruction) bool {
	setA := chanMap[insA] // channelSet of instruction-A
	setB := chanMap[insB] // channelSet of instruction-B
	for _, chanA := range setA {
		for _, chanB := range setB {
			if chanA == chanB {
				return true
			}
		}
	}
	return false
}

func (a *analysis) printRace(counter int, insPair []ssa.Instruction, addrPair []ssa.Value, goIDs []int, insInd []int) {
	log.Printf("Data race #%d", counter)
	log.Println(strings.Repeat("=", 100))
	for i, anIns := range insPair {
		var errMsg string
		if isWriteIns(anIns) {
			errMsg = fmt.Sprint("  Write of ", aurora.Magenta(addrPair[i].String()), " in function ", aurora.BgBrightGreen(anIns.Parent().Name()), " at ", a.prog.Fset.Position(anIns.Pos()))
		} else {
			errMsg = fmt.Sprint("  Read of ", aurora.Magenta(addrPair[i].String()), " in function ", aurora.BgBrightGreen(anIns.Parent().Name()), " at ", a.prog.Fset.Position(anIns.Pos()))
		}
		colorOutput := regexp.MustCompile(`\x1b\[\d+m`)
		errMsg = colorOutput.ReplaceAllString(errMsg, "")
		racyStackTops = append(racyStackTops, errMsg)
		log.Print(errMsg)
		var printStack []string
		var printPos []token.Pos
		for p, everyIns := range RWIns[goIDs[i]] {
			if p < insInd[i]-1 {
				if isFunc, ok := everyIns.(*ssa.Call); ok {
					printName := isFunc.Call.Value.Name()
					printName = checkTokenName(printName, everyIns.(*ssa.Call))
					printStack = append(printStack, printName)
					printPos = append(printPos, everyIns.Pos())
				} else if _, ok1 := everyIns.(*ssa.Return); ok1 && len(printStack) > 0 {
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
		log.Println("\tin goroutine  ***", goNames[goIDs[i]], "[", goIDs[i], "] *** , with the following call stack: ")
		var pathGo []int
		j := goIDs[i]
		for j > 0 {
			pathGo = append([]int{j}, pathGo...)
			temp := goCaller[j]
			j = temp
		}
		for q, eachGo := range pathGo {
			eachStack := goStack[eachGo]
			for k, eachFn := range eachStack {
				if k == 0 {
					log.Println("\t ", strings.Repeat(" ", q), "--> Goroutine: ", eachFn, "[", goCaller[eachGo], "]")
				} else {
					log.Println("\t   ", strings.Repeat(" ", q), strings.Repeat(" ", k), eachFn)
				}
			}
		}
	}
	log.Println(strings.Repeat("=", 100))
}
