package main

import (
	"flag"
	"fmt"
	"github.com/logrusorgru/aurora"
	log "github.com/sirupsen/logrus"
	"github.com/twmb/algoimpl/go/graph"
	"go/token"
	"go/types"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/pointer"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
	"strconv"
	"strings"
)

type analysis struct {
	prog         *ssa.Program
	pkgs         []*ssa.Package
	mains        []*ssa.Package
	result       *pointer.Result
	ptaConfig    *pointer.Config
	analysisStat stat
	HBgraph      *graph.Graph
	RWinsMap     map[ssa.Instruction]graph.Node
}

type goroutineInfo struct {
	goIns       *ssa.Go
	entryMethod string
	goID        int
}

type stat struct {
	nAccess    int
	nGoroutine int
}

type RWInsInd struct {
	goID  int
	goInd int
}

var (
	Analysis     *analysis
	focusPkgs    []string
	excludedPkgs []string
	allPkg       bool
	levels       = make(map[int]int)
	RWIns        [][]ssa.Instruction
	storeIns     []string
	workList     []goroutineInfo
	reportedAddr []ssa.Value
	lockMap      = make(map[ssa.Instruction][]ssa.Value) // map each read/write access to a snapshot of actively maintained lockset
	lockSet      []ssa.Value                             // active lockset, to be maintained along instruction traversal
	addrNameMap  = make(map[string][]ssa.Value)          // for potential optimization purposes
	addrMap      = make(map[string][]RWInsInd)           // for potential optimization purposes
	paramFunc    ssa.Value
	goStack      [][]string
	goCaller     = make(map[int]int)
	goNames      = make(map[int]string)
)

func checkTokenName(fnName string, theIns *ssa.Call) string {
	if strings.HasPrefix(fnName, "t") { // function name begins with letter t
		if _, err := strconv.Atoi(string([]rune(fnName)[1:])); err == nil { // function name after first character look like an integer
			switch callVal := theIns.Call.Value.(type) {
			case *ssa.MakeClosure:
				fnName = callVal.Fn.Name()
			default:
				fnName = callVal.Type().String()
			}
		}
	}
	return fnName
}

func checkTokenNameDefer(fnName string, theIns *ssa.Defer) string {
	if strings.HasPrefix(fnName, "t") { // function name begins with letter t
		if _, err := strconv.Atoi(string([]rune(fnName)[1:])); err == nil { // function name after first character look like an integer
			switch callVal := theIns.Call.Value.(type) {
			case *ssa.MakeClosure:
				fnName = callVal.Fn.Name()
			default:
				fnName = callVal.Type().String()
			}
		}
	}
	return fnName
}

func deleteFromLockSet(s []ssa.Value, k int) []ssa.Value {
	var res []ssa.Value
	res = append(s[:k], s[k+1:]...)
	return res
}

func fromPkgsOfInterest(fn *ssa.Function) bool {
	if fn.Pkg == nil || fn.Pkg.Pkg == nil {
		return false
	}
	for _, excluded := range excludedPkgs {
		if fn.Pkg.Pkg.Name() == excluded {
			return false
		}
	}
	for _, path := range focusPkgs {
		if path != "" && strings.HasPrefix(fn.Pkg.Pkg.Path(), path) {
			return true
		}
	}
	return allPkg
}

func init() {
	excludedPkgs = []string{
		"runtime",
		"fmt",
		"reflect",
		"encoding",
		"errors",
		"bytes",
		"strconv",
		"strings",
		"bytealg",
		"race",
		"syscall",
		"poll",
		"trace",
		"logging",
		"os",
		"builtin",
		"pflag",
		"log",
		"reflect",
		"internal",
		"impl",
	}
}

func isLocalAddr(location ssa.Value) bool {
	if location.Pos() == token.NoPos {
		return true
	}
	switch loc := location.(type) {
	case *ssa.Parameter:
		_, ok := loc.Type().(*types.Pointer)
		return !ok
	case *ssa.FieldAddr:
		isLocalAddr(loc.X)
	case *ssa.IndexAddr:
		isLocalAddr(loc.X)
	case *ssa.UnOp:
		isLocalAddr(loc.X)
	case *ssa.Alloc:
		if !loc.Heap {
			return true // false heap means local alloc
		}
	default:
		return false
	}
	return false
}

func isReadIns(ins ssa.Instruction) bool { // is the instruction a read access?
	switch insType := ins.(type) {
	case *ssa.UnOp:
		return true
	case *ssa.FieldAddr:
		return true
	case *ssa.Lookup:
		return true
	case *ssa.Call:
		if insType.Call.Value.Name() != "delete" && !strings.HasPrefix(insType.Call.Value.Name(), "Add") && len(insType.Call.Args) > 0 {
			return true
		}
	default:
		_ = insType
	}
	return false
}

func isSynthetic(fn *ssa.Function) bool {
	return fn.Synthetic != "" || fn.Pkg == nil
}

func isWriteIns(ins ssa.Instruction) bool { // is the instruction a write access?
	switch insType := ins.(type) {
	case *ssa.Store:
		return true
	case *ssa.Call:
		if insType.Call.Value.Name() == "delete" {
			return true
		} else if strings.HasPrefix(insType.Call.Value.Name(), "Add") {
			return true
		}
	}
	return false
}

func lockSetContainsAt(s []ssa.Value, e ssa.Value) int {
	var aPos, bPos token.Pos
	for i, a := range s {
		switch aType := a.(type) {
		case *ssa.Global:
			aPos = aType.Pos()
		case *ssa.FieldAddr:
			aPos = aType.X.Pos()
		}
		switch eType := e.(type) {
		case *ssa.Global:
			bPos = eType.Pos()
		case *ssa.FieldAddr:
			bPos = eType.X.Pos()
		}
		if aPos == bPos {
			return i
		}
	}
	return -1
}

func main() {
	debug := flag.Bool("debug", false, "Prints debug messages.")
	focus := flag.String("focus", "", "Specifies a list of packages to check races.")
	ptrAnalysis := flag.Bool("ptrAnalysis", false, "Prints pointer analysis results. ")
	//flag.BoolVar(&allPkg, "all-package", true, "Analyze all packages required by the main package.")
	flag.Parse()
	if *debug {
		log.SetLevel(log.DebugLevel)
	}
	if *ptrAnalysis {
		log.SetLevel(log.TraceLevel)
	}
	if *focus != "" {
		focusPkgs = strings.Split(*focus, ",")
		focusPkgs = append(focusPkgs, "command-line-arguments")
	} else {
		allPkg = true
	}

	log.SetFormatter(&log.TextFormatter{
		FullTimestamp: true,
	})

	err := staticAnalysis(flag.Args())
	if err != nil {
		log.Fatal(err)
	}
}

// mainPackages returns the main packages to analyze.
// Each resulting package is named "main" and has a main function.
func mainPackages(pkgs []*ssa.Package) ([]*ssa.Package, error) {
	var mains []*ssa.Package
	for _, p := range pkgs {
		if p != nil && p.Pkg.Name() == "main" && p.Func("main") != nil {
			mains = append(mains, p)
		}
	}
	if len(mains) == 0 {
		return nil, fmt.Errorf("no main packages")
	}
	return mains, nil
}

func sliceContains(s []ssa.Value, e ssa.Value) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}

func sliceContainsBloc(s []*ssa.BasicBlock, e *ssa.BasicBlock) bool {
	for _, a := range s {
		if a.Comment == e.Comment {
			return true
		}
	}
	return false
}

func sliceContainsStr(s []string, e string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}

func sliceContainsIns(s []ssa.Instruction, e ssa.Instruction) bool {
	i := 0
	for i < len(s) {
		if s[i] == e {
			return true
		}
		i++
	}
	return false
}

func sliceContainsInsAt(s []ssa.Instruction, e ssa.Instruction) int {
	i := 0
	for s[i] != e {
		i++
	}
	return i
}

func staticAnalysis(args []string) error {
	cfg := &packages.Config{
		Mode:  packages.LoadAllSyntax, // the level of information returned for each package
		Dir:   "",                     // directory in which to run the build system's query tool
		Tests: false,                  // setting Tests will include related test packages
	}
	log.Info("Loading input packages...")
	initial, err := packages.Load(cfg, args...)
	if err != nil {
		return err
	}
	if packages.PrintErrors(initial) > 0 {
		return fmt.Errorf("packages contain errors")
	} else if len(initial) == 0 {
		return fmt.Errorf("package list empty")
	}

	// Print the names of the source files
	// for each package listed on the command line.
	for nP, pkg := range initial {
		log.Info(pkg.ID, pkg.GoFiles)
		log.Infof("Done  -- %d packages loaded", nP+1)
	}

	// Create and build SSA-form program representation.
	prog, pkgs := ssautil.AllPackages(initial, 0)

	log.Info("Building SSA code for entire program...")
	prog.Build()
	log.Info("Done  -- SSA code built")

	mains, err := mainPackages(pkgs)
	if err != nil {
		return err
	}

	// Configure pointer analysis to build call-graph
	config := &pointer.Config{
		Mains:          mains,
		BuildCallGraph: true,
	}
	Analysis = &analysis{
		prog:      prog,
		pkgs:      pkgs,
		mains:     mains,
		ptaConfig: config,
		RWinsMap:  make(map[ssa.Instruction]graph.Node),
	}

	log.Info("Compiling stack trace for every Goroutine... ")
	log.Debug(strings.Repeat("-", 35), "Stack trace begins", strings.Repeat("-", 35))
	Analysis.visitAllInstructions(mains[0].Func("main"), 0)
	log.Debug(strings.Repeat("-", 35), "Stack trace ends", strings.Repeat("-", 35))
	totalIns := 0
	for g, _ := range RWIns {
		totalIns += len(RWIns[g])
	}
	log.Info("Done  -- ", Analysis.analysisStat.nGoroutine, " goroutines analyzed! ", totalIns, " instructions of interest detected! ")

	log.Info("Building Happened-Before graph... ")
	Analysis.HBgraph = graph.New(graph.Directed)
	var prevN graph.Node
	var goCaller []graph.Node
	var waitingN graph.Node
	for nGo, insSlice := range RWIns {
		for i, anIns := range insSlice {
			if nGo == 0 && i == 0 { // main goroutine, first instruction
				if _, ok := anIns.(*ssa.Go); !ok {
					prevN = Analysis.HBgraph.MakeNode() // initiate for future nodes
					*prevN.Value = anIns
				} else {
					prevN = Analysis.HBgraph.MakeNode() // initiate for future nodes
					*prevN.Value = anIns
					goCaller = append(goCaller, prevN) // sequentially store go calls in the same goroutine
				}
			} else {
				currN := Analysis.HBgraph.MakeNode()
				*currN.Value = anIns
				if nGo != 0 && i == 0 { // worker goroutine, first instruction
					prevN = goCaller[0]
					goCaller = goCaller[1:] // pop from stack
					err := Analysis.HBgraph.MakeEdge(prevN, currN)
					if err != nil {
						log.Fatal(err)
					}
				} else if _, ok := anIns.(*ssa.Go); ok {
					goCaller = append(goCaller, currN) // sequentially store go calls in the same goroutine
					err := Analysis.HBgraph.MakeEdge(prevN, currN)
					if err != nil {
						log.Fatal(err)
					}
				} else {
					err := Analysis.HBgraph.MakeEdge(prevN, currN)
					if err != nil {
						log.Fatal(err)
					}
				}
				prevN = currN
			}
			if isReadIns(anIns) || isWriteIns(anIns) {
				Analysis.RWinsMap[anIns] = prevN
			} else if callIns, ok := anIns.(*ssa.Call); ok {
				if callIns.Call.Value.Name() == "Wait" {
					waitingN = prevN
				}
			} else if callIns, ok := anIns.(*ssa.Call); ok && callIns.Call.Value.Name() == "Done" {
				err := Analysis.HBgraph.MakeEdge(prevN, waitingN)
				if err != nil {
					log.Fatal(err)
				}
			}
		}
	}
	log.Info("Done  -- Happened-Before graph built... ")

	log.Info("Checking for data races... ")
	result, err := pointer.Analyze(Analysis.ptaConfig) // conduct pointer analysis
	if err != nil {
		log.Fatal(err)
	}
	Analysis.result = result
	Analysis.checkRacyPairs()
	return nil
}

func updateRecords(fnName string, goID int, pushPop string) {
	if pushPop == "POP  " {
		storeIns = storeIns[:len(storeIns)-1]
		levels[goID]--
	}
	log.Debug(strings.Repeat(" ", levels[goID]), pushPop, fnName, " at lvl ", levels[goID])
	if pushPop == "PUSH " {
		storeIns = append(storeIns, fnName)
		levels[goID]++
	}
}

func (a *analysis) checkRacyPairs() {
	counter := 0 // initialize race counter
	for i := 0; i < len(RWIns); i++ {
		for j := i + 1; j < len(RWIns); j++ { // must be in different goroutines, j always greater than i
			for ii, goI := range RWIns[i] {
				for jj, goJ := range RWIns[j] {
					insSlice := []ssa.Instruction{goI, goJ} // one instruction from each goroutine
					addressPair := a.insAddress(insSlice)
					if len(addressPair) > 1 && a.sameAddress(addressPair[0], addressPair[1]) && !sliceContains(reportedAddr, addressPair[0]) && !a.reachable(goI, goJ) && !a.locksetsInterset(insSlice[0], insSlice[1]) {
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
	log.Println("Done  -- ", counter, "race found! ")
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

func (a *analysis) locksetsInterset(insA ssa.Instruction, insB ssa.Instruction) bool {
	setA := lockMap[insA] // lockset of instruction-A
	setB := lockMap[insB] // lockset of instruction-B
	for _, addrA := range setA {
		for _, addrB := range setB {
			if a.sameAddress(addrA, addrB) {
				return true
			}
		}
	}
	return false
}

func (a *analysis) newGoroutine(info goroutineInfo) {
	storeIns = append(storeIns, info.entryMethod)
	if info.goID >= len(RWIns) { // initialize interior slice for new goroutine
		RWIns = append(RWIns, []ssa.Instruction{})
	}
	RWIns[info.goID] = append(RWIns[info.goID], info.goIns)
	goNames[info.goID] = info.entryMethod
	log.Debug(strings.Repeat("-", 35), "Goroutine ", info.entryMethod, strings.Repeat("-", 35), "[", info.goID, "]")
	log.Debug(strings.Repeat(" ", levels[info.goID]), "PUSH ", info.entryMethod, " at lvl ", levels[info.goID])
	levels[info.goID]++
	switch info.goIns.Call.Value.(type) {
	case *ssa.MakeClosure:
		a.visitAllInstructions(info.goIns.Call.StaticCallee(), info.goID)
	case *ssa.TypeAssert:
		a.visitAllInstructions(paramFunc.(*ssa.MakeClosure).Fn.(*ssa.Function), info.goID)
	default:
		a.visitAllInstructions(info.goIns.Call.StaticCallee(), info.goID)
	}
}

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
	if len(PTSet) > 1 { // multiple targets returned
		log.Trace("***Pointer Analysis revealed ", len(PTSet), " targets for location - ", a.prog.Fset.Position(location.Pos()))
		for ind, eachTarget := range PTSet { // check each target
			if eachTarget.Value().Parent() != nil {
				log.Trace("*****target No.", ind+1, " - ", eachTarget.Value().String(), " from function ", eachTarget.Value().Parent().Name())
				if sliceContainsStr(storeIns, eachTarget.Value().Parent().Name()) { // calling function is in current goroutine
					rightLoc = ind
					//break
				} else {
					var allStack []string
					for i := 1; i <= goID; i++ {
						allStack = append(allStack, goStack[i]...)
					}
					if sliceContainsStr(allStack, eachTarget.Value().Parent().Name()) { // check callstack of current goroutine
						rightLoc = ind
						break
					}
				}
			} else {
				log.Debug("target No.", ind+1, " - ", eachTarget.Value().String(), " with no parent function*********")
			}
		}
		log.Trace("***Executing target No.", rightLoc+1)
	} else if len(PTSet) == 0 {
		return
	}
	switch theFunc := PTSet[rightLoc].Value().(type) {
	case *ssa.Function:
		fnName = theFunc.Name()
		if !sliceContainsIns(RWIns[goID], theIns) {
			log.Debug(strings.Repeat(" ", levels[goID]), "PUSH ", fnName, " at lvl ", levels[goID])
			levels[goID]++
			storeIns = append(storeIns, fnName)
			RWIns[goID] = append(RWIns[goID], theIns)
			a.visitAllInstructions(theFunc, goID)
		}
	case *ssa.MakeInterface: // for abstract method calls
		methodName := theIns.(*ssa.Call).Call.Method.Name()
		check := a.prog.LookupMethod(ptrSet[location].PointsTo().DynamicTypes().Keys()[0], a.mains[0].Pkg, methodName)
		fnName = check.Name()
		if !sliceContainsIns(RWIns[goID], theIns) {
			log.Debug(strings.Repeat(" ", levels[goID]), "PUSH ", fnName, " at lvl ", levels[goID])
			levels[goID]++
			storeIns = append(storeIns, fnName)
			RWIns[goID] = append(RWIns[goID], theIns)
			a.visitAllInstructions(check, goID)
		}
	default:
		return
	}
}

func (a *analysis) printRace(counter int, insPair []ssa.Instruction, addrPair []ssa.Value, goIDs []int, insInd []int) {
	log.Printf("Data race #%d", counter)
	log.Println(strings.Repeat("=", 100))
	for i, anIns := range insPair {
		if isWriteIns(anIns) {
			log.Println("  Write of ", aurora.Magenta(addrPair[i].Name()), " in function ", aurora.BgBrightGreen(anIns.Parent().Name()), " at ", a.prog.Fset.Position(anIns.Pos()))
		} else {
			log.Println("  Read of  ", aurora.Magenta(addrPair[i].Name()), " in function ", aurora.BgBrightGreen(anIns.Parent().Name()), " at ", a.prog.Fset.Position(anIns.Pos()))
		}
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

func (a *analysis) sameAddress(addr1 ssa.Value, addr2 ssa.Value) bool {
	// check if both are the same global
	if global1, ok1 := addr1.(*ssa.Global); ok1 {
		if global2, ok2 := addr2.(*ssa.Global); ok2 {
			return global1.Pos() == global2.Pos()
		}
	}

	// check points-to set to see if they can point to the same object
	ptset := a.result.Queries
	return ptset[addr1].PointsTo().Intersects(ptset[addr2].PointsTo())
}

func (a *analysis) visitAllInstructions(fn *ssa.Function, goID int) {
	a.analysisStat.nGoroutine = goID + 1 // keep count of goroutine quantity
	if !isSynthetic(fn) {                // if function is NOT synthetic
		for _, excluded := range excludedPkgs { // TODO: need revision
			if fn.Pkg.Pkg.Name() == excluded {
				return
			}
		}
		if fn.Name() == "main" {
			levels[goID] = 0 // initialize level count at main entry
			updateRecords(fn.Name(), goID, "PUSH ")
			goStack = append(goStack, []string{}) // initialize first interior slice for main goroutine
		}
	}
	if _, ok := levels[goID]; !ok && goID > 0 { // initialize level counter for new goroutine
		levels[goID] = 1
	}
	if goID >= len(RWIns) { // initialize interior slice for new goroutine
		RWIns = append(RWIns, []ssa.Instruction{})
	}
	fnBlocks := fn.Blocks
	var returnIns []ssa.Instruction
	var toAppend []*ssa.BasicBlock
	var toDefer []ssa.Instruction // stack storing deferred calls
	for j, nBlock := range fn.Blocks {
		if strings.HasSuffix(nBlock.Comment, ".done") && j != len(fnBlocks)-1 { // when return block doesn't have largest index
			bLen := len(nBlock.Instrs)
			if _, ok := nBlock.Instrs[bLen-1].(*ssa.Return); ok {
				toAppend = append([]*ssa.BasicBlock{nBlock}, toAppend...)
			}
		} else if nBlock.Comment == "recover" { // ignore built-in recover function
			fnBlocks = append(fnBlocks[:j], fnBlocks[j+1:]...)
		}
	}
	if len(toAppend) > 0 {
		fnBlocks = append(fnBlocks, toAppend...) // move return block to end of slice
	}
	repeatSwitch := false // triggered when encountering basic blocks for body of a forloop
	for i := 0; i < len(fnBlocks); i++ {
		aBlock := fnBlocks[i]
		if strings.HasSuffix(aBlock.Comment, ".done") && i != len(fnBlocks)-1 && sliceContainsBloc(toAppend, aBlock) { // ignore return block if it doesn't have largest index
			continue
		}
		if aBlock.Comment == "for.body" || aBlock.Comment == "rangeindex.body" { // repeat unrolling of forloop
			if repeatSwitch == false {
				i--
				repeatSwitch = true
			} else {
				repeatSwitch = false
			}
		}
		for _, theIns := range aBlock.Instrs { // examine each instruction
			if theIns.String() == "rundefers" { // execute deferred calls at this index
				for _, dIns := range toDefer {
					deferIns := dIns.(*ssa.Defer)
					if _, ok := deferIns.Call.Value.(*ssa.Builtin); ok {
						continue
					}
					if fromPkgsOfInterest(deferIns.Call.StaticCallee()) && deferIns.Call.StaticCallee().Pkg.Pkg.Name() != "sync" {
						fnName := deferIns.Call.Value.Name()
						fnName = checkTokenNameDefer(fnName, deferIns)
						if !sliceContainsIns(RWIns[goID], theIns) {
							updateRecords(fnName, goID, "PUSH ")
							RWIns[goID] = append(RWIns[goID], dIns)
							a.visitAllInstructions(dIns.(*ssa.Defer).Call.StaticCallee(), goID)
						}
					} else if deferIns.Call.StaticCallee().Name() == "Unlock" {
						lockLoc := deferIns.Call.Args[0]
						if k := lockSetContainsAt(lockSet, lockLoc); k >= 0 {
							lockSet = deleteFromLockSet(lockSet, k)
						}
					}
				}
			}
			for _, ex := range excludedPkgs { // TODO: need revision
				if !isSynthetic(fn) && ex == theIns.Parent().Pkg.Pkg.Name() {
					return
				}
			}
			switch examIns := theIns.(type) {
			case *ssa.Store: // write op
				if !isLocalAddr(examIns.Addr) {
					if len(storeIns) > 1 {
						if storeIns[len(storeIns)-2] == "AfterFunc" { // ignore this write instruction as AfterFunc is analyzed elsewhere
							break
						}
					}
					RWIns[goID] = append(RWIns[goID], theIns)
					if len(lockSet) > 0 {
						lockMap[theIns] = lockSet
					}
					addrNameMap[examIns.Addr.Name()] = append(addrNameMap[examIns.Addr.Name()], examIns.Addr)                                                 // map address name to address, used for checking points-to labels later
					addrMap[examIns.Addr.Name()] = append(addrMap[examIns.Addr.Name()], RWInsInd{goID: goID, goInd: sliceContainsInsAt(RWIns[goID], theIns)}) // map address name to slice of instructions accessing the same address name
					a.ptaConfig.AddQuery(examIns.Addr)
				}
				if theFunc, storeFn := examIns.Val.(*ssa.Function); storeFn {
					fnName := theFunc.Name()
					if !sliceContainsIns(RWIns[goID], theIns) {
						updateRecords(fnName, goID, "PUSH ")
						RWIns[goID] = append(RWIns[goID], theIns)
						a.visitAllInstructions(theFunc, goID)
					}
				}
			case *ssa.UnOp: // read op
				if examIns.Op == token.MUL && !isLocalAddr(examIns.X) {
					RWIns[goID] = append(RWIns[goID], theIns)
					if len(lockSet) > 0 {
						lockMap[theIns] = lockSet
					}
					addrNameMap[examIns.X.Name()] = append(addrNameMap[examIns.X.Name()], examIns.X)
					addrMap[examIns.X.Name()] = append(addrMap[examIns.X.Name()], RWInsInd{goID: goID, goInd: sliceContainsInsAt(RWIns[goID], theIns)})
					a.ptaConfig.AddQuery(examIns.X)
				}
			case *ssa.FieldAddr:
				if !isLocalAddr(examIns.X) {
					RWIns[goID] = append(RWIns[goID], theIns)
					if len(lockSet) > 0 {
						lockMap[theIns] = lockSet
					}
					addrNameMap[examIns.X.Name()] = append(addrNameMap[examIns.X.Name()], examIns.X)                                                    // map address name to address, used for checking points-to labels later
					addrMap[examIns.X.Name()] = append(addrMap[examIns.X.Name()], RWInsInd{goID: goID, goInd: sliceContainsInsAt(RWIns[goID], theIns)}) // map address name to slice of instructions accessing the same address name
					a.ptaConfig.AddQuery(examIns.X)
				}
			case *ssa.Lookup: // look up element index, read op
				switch readIns := examIns.X.(type) {
				case *ssa.UnOp:
					if readIns.Op == token.MUL && !isLocalAddr(readIns.X) {
						RWIns[goID] = append(RWIns[goID], theIns)
						if len(lockSet) > 0 {
							lockMap[theIns] = lockSet
						}
						addrNameMap[readIns.X.Name()] = append(addrNameMap[readIns.X.Name()], readIns.X)
						addrMap[readIns.X.Name()] = append(addrMap[readIns.X.Name()], RWInsInd{goID: goID, goInd: sliceContainsInsAt(RWIns[goID], theIns)})
						a.ptaConfig.AddQuery(readIns.X)
					}
				case *ssa.Parameter:
					if !isLocalAddr(readIns) {
						RWIns[goID] = append(RWIns[goID], theIns)
						if len(lockSet) > 0 {
							lockMap[theIns] = lockSet
						}
						addrNameMap[readIns.Name()] = append(addrNameMap[readIns.Name()], readIns)
						addrMap[readIns.Name()] = append(addrMap[readIns.Name()], RWInsInd{goID: goID, goInd: sliceContainsInsAt(RWIns[goID], theIns)})
						a.ptaConfig.AddQuery(readIns)
					}
				}
			case *ssa.ChangeType: // a value-preserving type change, write op
				switch mc := examIns.X.(type) {
				case *ssa.MakeClosure: // yield closure value for *Function and free variable values supplied by Bindings
					theFn := mc.Fn.(*ssa.Function)
					if fromPkgsOfInterest(theFn) {
						fnName := mc.Fn.Name()
						if !sliceContainsIns(RWIns[goID], theIns) {
							updateRecords(fnName, goID, "PUSH ")
							RWIns[goID] = append(RWIns[goID], theIns)
							if len(lockSet) > 0 {
								lockMap[theIns] = lockSet
							}
							a.visitAllInstructions(theFn, goID)
						}
					}
				default:
					continue
				}
			case *ssa.Defer:
				toDefer = append([]ssa.Instruction{theIns}, toDefer...)
			case *ssa.MakeInterface: // construct instance of interface type
				if strings.Contains(examIns.X.String(), "complit") {
					continue
				}
				if _, ok := examIns.X.(*ssa.Call); !ok {
					if _, ok := examIns.X.(*ssa.Parameter); !ok {
						continue
					} else {
						if _, ok1 := examIns.X.(*ssa.Parameter).Type().(*types.Basic); ok1 {
							continue
						}
					}
				}
				a.pointerAnalysis(examIns.X, goID, theIns)
			case *ssa.Call:
				if examIns.Call.StaticCallee() == nil && examIns.Call.Method == nil {
					if _, ok := examIns.Call.Value.(*ssa.Builtin); !ok {
						a.pointerAnalysis(examIns.Call.Value, goID, theIns)
					} else if examIns.Call.Value.Name() == "delete" { // built-in delete op
						if theVal, ok := examIns.Call.Args[0].(*ssa.UnOp); ok {
							if theVal.Op == token.MUL && !isLocalAddr(theVal.X) {
								RWIns[goID] = append(RWIns[goID], theIns)
								if len(lockSet) > 0 {
									lockMap[theIns] = lockSet
								}
								addrNameMap[theVal.X.Name()] = append(addrNameMap[theVal.X.Name()], theVal)
								addrMap[theVal.X.Name()] = append(addrMap[theVal.X.Name()], RWInsInd{goID: goID, goInd: sliceContainsInsAt(RWIns[goID], theIns)})
								a.ptaConfig.AddQuery(theVal.X)
							}
						}
					} else {
						continue
					}
				} else if examIns.Call.Method != nil && examIns.Call.Method.Pkg() != nil { // calling an method
					if examIns.Call.Method.Pkg().Name() != "sync" {
						if _, ok := examIns.Call.Value.(*ssa.Builtin); !ok {
							a.pointerAnalysis(examIns.Call.Value, goID, theIns)
						} else {
							continue
						}
					}
				} else if fromPkgsOfInterest(examIns.Call.StaticCallee()) && examIns.Call.StaticCallee().Pkg.Pkg.Name() != "sync" { // calling a function
					if examIns.Call.Value.Name() == "AfterFunc" && examIns.Call.StaticCallee().Pkg.Pkg.Name() == "time" { // calling time.AfterFunc()
						paramFunc = examIns.Call.Args[1]
					}
					for _, checkArgs := range examIns.Call.Args {
						switch access := checkArgs.(type) {
						case *ssa.FieldAddr:
							if !isLocalAddr(access.X) && strings.HasPrefix(examIns.Call.Value.Name(), "Add") {
								RWIns[goID] = append(RWIns[goID], theIns)
								if len(lockSet) > 0 {
									lockMap[theIns] = lockSet
								}
								addrNameMap[access.X.Name()] = append(addrNameMap[access.X.Name()], access.X)                                                     // map address name to address, used for checking points-to labels later
								addrMap[access.X.Name()] = append(addrMap[access.X.Name()], RWInsInd{goID: goID, goInd: sliceContainsInsAt(RWIns[goID], theIns)}) // map address name to slice of instructions accessing the same address name
								a.ptaConfig.AddQuery(access.X)
							}
						default:
							continue
						}
					}
					if examIns.Call.StaticCallee().Blocks == nil {
						continue
					}
					fnName := examIns.Call.Value.Name()
					fnName = checkTokenName(fnName, examIns)
					if !sliceContainsIns(RWIns[goID], theIns) {
						updateRecords(fnName, goID, "PUSH ")
						RWIns[goID] = append(RWIns[goID], theIns)
						a.visitAllInstructions(examIns.Call.StaticCallee(), goID)
					}
				} else if examIns.Call.StaticCallee() == nil {
					log.Debug("***********************special case*****************************************")
					return
				} else if examIns.Call.StaticCallee().Pkg.Pkg.Name() == "sync" {
					switch examIns.Call.Value.Name() {
					case "Range":
						fnName := examIns.Call.Value.Name()
						if !sliceContainsIns(RWIns[goID], theIns) {
							updateRecords(fnName, goID, "PUSH ")
							RWIns[goID] = append(RWIns[goID], theIns)
							a.visitAllInstructions(examIns.Call.StaticCallee(), goID)
						}
					case "Lock":
						lockLoc := examIns.Call.Args[0]       // identifier for address of lock
						if !sliceContains(lockSet, lockLoc) { // if lock is not already in active lockset
							lockSet = append(lockSet, lockLoc)
						}
					case "Unlock":
						lockLoc := examIns.Call.Args[0]
						if p := lockSetContainsAt(lockSet, lockLoc); p >= 0 {
							lockSet = deleteFromLockSet(lockSet, p)
						}
					case "Wait":
						RWIns[goID] = append(RWIns[goID], theIns)
					case "Done":
						RWIns[goID] = append(RWIns[goID], theIns)
					}
				} else {
					continue
				}
			case *ssa.Return:
				if i != len(fnBlocks)-1 {
					returnIns = append([]ssa.Instruction{theIns}, returnIns...) // store return instructions that don't belong to final block
					continue
				}
				if (fromPkgsOfInterest(examIns.Parent()) || isSynthetic(examIns.Parent())) && len(storeIns) > 0 {
					fnName := examIns.Parent().Name()
					if fnName == storeIns[len(storeIns)-1] {
						updateRecords(fnName, goID, "POP  ")
						RWIns[goID] = append(RWIns[goID], theIns)
					}
				}
				if len(storeIns) == 0 && len(workList) != 0 { // finished reporting current goroutine and workList isn't empty
					nextGoInfo := workList[0] // get the goroutine info at head of workList
					workList = workList[1:]   // pop goroutine info from head of workList
					a.newGoroutine(nextGoInfo)
				} else {
					return
				}
			case *ssa.Go: // for spawning of goroutines
				var fnName string
				switch anonFn := examIns.Call.Value.(type) {
				case *ssa.MakeClosure: // go call for anonymous function
					fnName = anonFn.Fn.Name()
				case *ssa.Function:
					fnName = anonFn.Name()
				case *ssa.TypeAssert:
					fnName = paramFunc.(*ssa.MakeClosure).Fn.Name()
				}
				newGoID := goID + 1 // increment goID for child goroutine
				if len(workList) > 0 {
					newGoID = workList[len(workList)-1].goID + 1
				}
				RWIns[goID] = append(RWIns[goID], theIns)
				var info = goroutineInfo{examIns, fnName, newGoID}
				goStack = append(goStack, []string{}) // initialize interior slice
				goCaller[newGoID] = goID              // map caller goroutine
				goStack[newGoID] = append(goStack[newGoID], storeIns...)
				workList = append(workList, info) // store encountered goroutines
				log.Debug(strings.Repeat(" ", levels[goID]), "spawning Goroutine ----->  ", fnName)
			}
		}
		if i == len(fnBlocks)-1 && len(returnIns) > 0 {
			theIns := returnIns[0]
			examIns := theIns.(*ssa.Return)
			if (fromPkgsOfInterest(examIns.Parent()) || isSynthetic(examIns.Parent())) && len(storeIns) > 0 {
				fnName := examIns.Parent().Name()
				if fnName == storeIns[len(storeIns)-1] {
					updateRecords(fnName, goID, "POP  ")
					RWIns[goID] = append(RWIns[goID], theIns)
				}
			}
			if len(storeIns) == 0 && len(workList) != 0 { // finished reporting current goroutine and workList isn't empty
				nextGoInfo := workList[0] // get the goroutine info at end of workList
				workList = workList[1:]   // pop goroutine info from end of workList
				a.newGoroutine(nextGoInfo)
			} else {
				return
			}
		}
	}
}
