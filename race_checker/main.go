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
	"strings"
)

type analysis struct {
	prog          *ssa.Program
	pkgs          []*ssa.Package
	mains         []*ssa.Package
	result        *pointer.Result
	ptaConfig     *pointer.Config
	fn2SummaryMap map[*ssa.Function]*functionSummary
	analysisStat  stat
	HBgraph       *graph.Graph
	RWinsMap      map[ssa.Instruction]graph.Node
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
	allocMap     = make(map[*ssa.Alloc]bool)
	insCounter   int
)

func doAnalysis(args []string) error {
	cfg := &packages.Config{
		Mode:  packages.LoadAllSyntax,
		Dir:   "",
		Tests: false,
	}
	log.Info("Loading input packages...")
	initial, err := packages.Load(cfg, args...)
	if err != nil {
		return err
	}
	if packages.PrintErrors(initial) > 0 {
		return fmt.Errorf("packages contain errors")
	}

	// Print the names of the source files
	// for each package listed on the command line.
	for nP, pkg := range initial {
		log.Info(pkg.ID, pkg.GoFiles)
		log.Infof("Done  -- %d packages loaded", nP+1)
	}

	// Create and build SSA-form program representation.
	prog, pkgs := ssautil.AllPackages(initial, 0)
	//mainPkg := prog.Package(pkgs[0].Pkg)

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
		prog:          prog,
		pkgs:          pkgs,
		mains:         mains,
		ptaConfig:     config,
		fn2SummaryMap: make(map[*ssa.Function]*functionSummary),
		RWinsMap:      make(map[ssa.Instruction]graph.Node),
	}

	log.Info("Compiling stack trace for every Goroutine... ")
	log.Debug(strings.Repeat("-", 35), "Stack trace begins", strings.Repeat("-", 35))
	Analysis.VisitAllInstructions(mains[0].Func("main"), 0)
	log.Debug(strings.Repeat("-", 35), "Stack trace ends", strings.Repeat("-", 35))
	totalIns := 0
	for g, _ := range RWIns {
		totalIns += len(RWIns[g])
	}
	log.Info("Done  -- ", Analysis.analysisStat.nGoroutine, " goroutines analyzed! ", totalIns, " instructions of interest! ")

	log.Info("Building Happened-Before graph... ")
	Analysis.HBgraph = graph.New(graph.Directed)
	var prevN graph.Node
	var goCaller []graph.Node
	for nGo, insSlice := range RWIns {
		for i, anIns := range insSlice {
			if nGo == 0 && i == 0 {
				prevN = Analysis.HBgraph.MakeNode()
				*prevN.Value = anIns
			} else {
				currN := Analysis.HBgraph.MakeNode()
				*currN.Value = anIns
				if nGo != 0 && i == 0 {
					prevN = goCaller[0]
					goCaller = goCaller[1:]
					err := Analysis.HBgraph.MakeEdge(prevN, currN)
					if err != nil {
						log.Fatal(err)
					}
				} else if _, ok := anIns.(*ssa.Go); ok {
					goCaller = append(goCaller, currN)
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

func fromPkgsOfInterest(fn *ssa.Function) bool {
	if fn.Pkg == nil || fn.Pkg.Pkg == nil {
		return false
	}
	for _, excluded := range excludedPkgs {
		if fn.Pkg.Pkg.Name() == excluded {
			if fn.Name() == "AfterFunc" || fn.Name() == "when" { // TODO: revision needed
				return true
			}
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
		"time",
		"testing",
		"strconv",
		"atomic",
		"strings",
		"bytealg",
		"race",
		"syscall",
		"poll",
		"trace",
		"logging",
		"os",
	}
}

func isLocalAddr(location ssa.Value) bool {
	if location.Pos() == token.NoPos {
		return true
	}
	if locPara, ok := location.(*ssa.Parameter); ok {
		_, ok := locPara.Type().(*types.Pointer)
		return !ok
	}
	switch loc := location.(type) {
	case *ssa.FieldAddr:
		return false
	case *ssa.IndexAddr:
		return isLocalAddr(loc.X)
	//case *ssa.Call:
	//	if loc.Call.Value.Name() == "append" {
	//		lastArg := loc.Call.Args[len(loc.Call.Args)-1]
	//		if locSlice, ok := lastArg.(*ssa.Slice); ok {
	//			if locAlloc, ok := locSlice.X.(*ssa.Alloc); ok {
	//				if _, ok := allocMap[locAlloc]; ok || !locAlloc.Heap || locAlloc.Comment == "complit" {
	//					return true
	//				}
	//			}
	//		}
	//	}
	case *ssa.UnOp:
		return isLocalAddr(loc.X)
	case *ssa.Alloc:
		if _, ok := allocMap[loc]; ok || !loc.Heap || loc.Comment == "complit" {
			return true
		} // false heap means local alloc
	default:
		return false
	}
	return false
}

func isReadIns(ins ssa.Instruction) bool { // is the instruction a read access?
	switch insType := ins.(type) {
	case *ssa.UnOp:
		return true
	case *ssa.Lookup:
		return true
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
		}
	}
	return false
}

func main() {
	debug := flag.Bool("debug", false, "Prints debug messages.")
	focus := flag.String("focus", "", "Specifies a list of packages to check races.")
	//flag.BoolVar(&allPkg, "all-package", true, "Analyze all packages required by the main package.")
	flag.Parse()
	if *debug {
		log.SetLevel(log.DebugLevel)
	}
	if *focus != "" {
		focusPkgs = strings.Split(*focus, ",")
		focusPkgs = append(focusPkgs, "command-line-arguments")
	} else {
		allPkg = true
	}

	err := doAnalysis(flag.Args())
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

func sliceContainsG(s []graph.Node, e graph.Node) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}

func (a *analysis) checkRacyPairs() {
	counter := 0 // initialize race counter
	for i := 0; i < len(RWIns); i++ {
		for j := i + 1; j < len(RWIns); j++ { // must be in different goroutines, j always greater than i
			for _, goI := range RWIns[i] {
				for _, goJ := range RWIns[j] {
					insSlice := []ssa.Instruction{goI, goJ} // one instruction from each goroutine
					addressPair := a.insAddress(insSlice)
					if len(addressPair) > 1 && a.sameAddress(addressPair[0], addressPair[1]) && !sliceContains(reportedAddr, addressPair[0]) && !a.reachable(goI, goJ) {
						reportedAddr = append(reportedAddr, addressPair[0])
						counter++
						a.printRace(counter, insSlice, addressPair)
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
			}
		case *ssa.UnOp:
			theAddrs = append(theAddrs, theIns.X)
		case *ssa.Lookup:
			theAddrs = append(theAddrs, theIns.X)
		}
	}
	if minWrite > 0 && len(theAddrs) > 1 {
		return theAddrs // write op always before read op
	}
	return []ssa.Value{}
}

func (a *analysis) newGoroutine(info goroutineInfo) {
	storeIns = append(storeIns, info.entryMethod)
	if info.goID >= len(RWIns) { // initialize interior slice for new goroutine
		RWIns = append(RWIns, []ssa.Instruction{})
	}
	RWIns[info.goID] = append(RWIns[info.goID], info.goIns)
	log.Debug(strings.Repeat("-", 35), "Goroutine ", info.entryMethod, strings.Repeat("-", 35), "[", info.goID, "]")
	log.Debug(strings.Repeat(" ", levels[info.goID]), "PUSH ", info.entryMethod, " at lvl ", levels[info.goID])
	levels[info.goID]++
	a.VisitAllInstructions(info.goIns.Call.StaticCallee(), info.goID)
}

func (a *analysis) pointerAnalysis(location ssa.Value, goID int, theIns ssa.Instruction) {
	a.ptaConfig.AddQuery(location)
	result, err := pointer.Analyze(a.ptaConfig) // conduct pointer analysis
	if err != nil {
		log.Fatal(err)
	}
	Analysis.result = result
	ptrSet := a.result.Queries                    // set of pointers from result of pointer analysis
	PTSet := ptrSet[location].PointsTo().Labels() // set of labels for locations that the pointer points to
	var fnName string
	switch theFunc := PTSet[0].Value().(type) {
	case *ssa.Function:
		fnName = theFunc.Name()
		log.Debug(strings.Repeat(" ", levels[goID]), "PUSH ", fnName, " at lvl ", levels[goID])
		levels[goID]++
		storeIns = append(storeIns, fnName)
		RWIns[goID] = append(RWIns[goID], theIns)
		a.VisitAllInstructions(theFunc, goID)
	case *ssa.MakeInterface: // for abstract method calls
		methodName := theIns.(*ssa.Call).Call.Method.Name()
		check := a.prog.LookupMethod(ptrSet[location].PointsTo().DynamicTypes().Keys()[0], a.mains[0].Pkg, methodName)
		fnName = check.Name()
		log.Debug(strings.Repeat(" ", levels[goID]), "PUSH ", fnName, " at lvl ", levels[goID])
		levels[goID]++
		storeIns = append(storeIns, fnName)
		RWIns[goID] = append(RWIns[goID], theIns)
		a.VisitAllInstructions(check, goID)
	default:
		return
	}
}

func (a *analysis) printRace(counter int, insPair []ssa.Instruction, addrPair []ssa.Value) {
	log.Printf("Data race #%d", counter)
	log.Println(strings.Repeat("=", 100))
	for i, anIns := range insPair {
		if isWriteIns(anIns) {
			log.Println("\tWrite of ", aurora.Magenta(addrPair[i].Name()), " in function ", aurora.BgBrightGreen(anIns.Parent().Name()), " at ", a.prog.Fset.Position(anIns.Pos()))
		} else {
			log.Println("\tRead of  ", aurora.Magenta(addrPair[i].Name()), " in function ", aurora.BgBrightGreen(anIns.Parent().Name()), " at ", a.prog.Fset.Position(anIns.Pos()))
		}
	}
	log.Println(strings.Repeat("=", 100))
}

func (a *analysis) reachable(fromIns ssa.Instruction, toIns ssa.Instruction) bool {
	fromNode := a.RWinsMap[fromIns]
	toNode := a.RWinsMap[toIns]
	er := a.HBgraph.Neighbors(fromNode)
	for len(er) > 0 {
		new := er[len(er)-1]
		er = er[:len(er)-1]
		next := a.HBgraph.Neighbors(new)
		er = append(er, next...)
		if new == toNode {
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

func (a *analysis) VisitAllInstructions(fn *ssa.Function, goID int) { // called by doAnalysis()
	a.analysisStat.nGoroutine = goID + 1 // keep count of goroutine quantity
	if !isSynthetic(fn) {                // if function is NOT synthetic
		for _, excluded := range excludedPkgs { // TODO: need revision
			if fn.Pkg.Pkg.Name() == excluded && fn.Name() != "AfterFunc" && fn.Name() != "when" {
				return
			}
		}
		if fn.Name() == "main" {
			levels[goID] = 0 // initialize level count at main entry
			log.Debug(strings.Repeat(" ", levels[goID]), "PUSH ", fn.Name(), " at lvl ", levels[goID])
			storeIns = append(storeIns, fn.Name())
			levels[goID]++
		}
	}
	if _, ok := levels[goID]; !ok && goID > 0 { // initialize level counter for new goroutine
		levels[goID] = 1
	}
	if goID >= len(RWIns) { // initialize interior slice for new goroutine
		RWIns = append(RWIns, []ssa.Instruction{})
	}
	fnBlocks := fn.Blocks
	var toAppend []*ssa.BasicBlock
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
		if strings.HasSuffix(aBlock.Comment, ".done") && i != len(fnBlocks)-1 { // ignore return block if it doesn't have largest index
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
			for _, ex := range excludedPkgs { // TODO: need revision
				if !isSynthetic(fn) && ex == theIns.Parent().Pkg.Pkg.Name() {
					if fn.Name() != "AfterFunc" && fn.Name() != "when" {
						return
					}
				}
			}
			switch examIns := theIns.(type) {
			case *ssa.Alloc: // reserves space for a variable
				allocMap[examIns] = true // store addresses of reserved space for checking whether an address is local
			case *ssa.Store: // write op
				if !isLocalAddr(examIns.Addr) {
					RWIns[goID] = append(RWIns[goID], theIns)
					a.ptaConfig.AddQuery(examIns.Addr)
				}
			case *ssa.UnOp: // read op
				if examIns.Op == token.MUL && !isLocalAddr(examIns.X) { // pointer dereference
					RWIns[goID] = append(RWIns[goID], theIns)
					a.ptaConfig.AddQuery(examIns.X)
				}
			case *ssa.Lookup: // look up element index
				readIns := examIns.X.(*ssa.UnOp)
				if readIns.Op == token.MUL && !isLocalAddr(readIns.X) {
					RWIns[goID] = append(RWIns[goID], theIns)
					a.ptaConfig.AddQuery(readIns.X)
				}
			case *ssa.ChangeType: // a value-preserving type change
				switch mc := examIns.X.(type) {
				case *ssa.MakeClosure: // yield closure value for *Function and free variable values supplied by Bindings
					theFn := mc.Fn.(*ssa.Function)
					if fromPkgsOfInterest(theFn) {
						fnName := mc.Fn.Name()
						log.Debug(strings.Repeat(" ", levels[goID]), "PUSH ", fnName, " at lvl ", levels[goID])
						levels[goID]++
						storeIns = append(storeIns, fnName)
						RWIns[goID] = append(RWIns[goID], theIns)
						a.VisitAllInstructions(theFn, goID)
					}
				default:
					continue
				}
			case *ssa.Defer:
				if _, ok := examIns.Call.Value.(*ssa.Builtin); ok {
					continue
				}
				if fromPkgsOfInterest(examIns.Call.StaticCallee()) && examIns.Call.StaticCallee().Pkg.Pkg.Name() != "sync" {
					fnName := examIns.Call.Value.Name()
					if strings.HasPrefix(fnName, "t") && len(fnName) <= 3 { // if function name is token number
						switch callVal := examIns.Call.Value.(type) {
						case *ssa.MakeClosure:
							fnName = callVal.Fn.Name()
						default:
							fnName = callVal.Type().String()
						}
					}
					log.Debug(strings.Repeat(" ", levels[goID]), "PUSH ", fnName, " at lvl ", levels[goID])
					storeIns = append(storeIns, fnName)
					RWIns[goID] = append(RWIns[goID], theIns)
					levels[goID]++
					a.VisitAllInstructions(examIns.Call.StaticCallee(), goID)
				}
			case *ssa.MakeInterface: // construct instance of interface type
				if strings.Contains(examIns.X.String(), "complit") {
					continue
				}
				if _, ok := examIns.X.(*ssa.Call); !ok {
					continue
				}
				a.pointerAnalysis(examIns.X, goID, theIns)
			case *ssa.Call:
				if examIns.Call.StaticCallee() == nil && examIns.Call.Method == nil { // calling a parameter function
					if _, ok := examIns.Call.Value.(*ssa.Builtin); !ok {
						a.pointerAnalysis(examIns.Call.Value, goID, theIns)
					} else if examIns.Call.Value.Name() == "delete" { // delete op
						if theVal, ok := examIns.Call.Args[0].(*ssa.UnOp); ok {
							if theVal.Op == token.MUL && !isLocalAddr(theVal.X) {
								RWIns[goID] = append(RWIns[goID], theIns)
								a.ptaConfig.AddQuery(theVal.X)
							}
						}
					} else {
						continue
					}
				} else if examIns.Call.Method != nil { // calling an method
					if _, ok := examIns.Call.Value.(*ssa.Builtin); !ok {
						a.pointerAnalysis(examIns.Call.Value, goID, theIns)
					} else {
						continue
					}
				} else if fromPkgsOfInterest(examIns.Call.StaticCallee()) && (examIns.Call.StaticCallee().Pkg.Pkg.Name() != "sync" || examIns.Call.StaticCallee().Name() == "Range") { // calling a function
					fnName := examIns.Call.Value.Name()
					if fnName == "runtimeNano" || fnName == "startTimer" { // revision needed
						continue
					}
					if strings.HasPrefix(fnName, "t") && len(fnName) <= 3 { // if function name is token number
						switch callVal := examIns.Call.Value.(type) {
						case *ssa.MakeClosure:
							fnName = callVal.Fn.Name()
						default:
							fnName = callVal.Type().String()
						}
					}
					log.Debug(strings.Repeat(" ", levels[goID]), "PUSH ", fnName, " at lvl ", levels[goID])
					storeIns = append(storeIns, fnName)
					RWIns[goID] = append(RWIns[goID], theIns)
					levels[goID]++
					a.VisitAllInstructions(examIns.Call.StaticCallee(), goID)
				}
			case *ssa.Return:
				if i != len(fnBlocks)-1 {
					continue
				}
				if (fromPkgsOfInterest(examIns.Parent()) || isSynthetic(examIns.Parent())) && len(storeIns) > 0 {
					fnName := examIns.Parent().Name()
					if fnName == storeIns[len(storeIns)-1] {
						storeIns = storeIns[:len(storeIns)-1]
						RWIns[goID] = append(RWIns[goID], theIns)
						levels[goID]--
						log.Debug(strings.Repeat(" ", levels[goID]), "POP  ", fnName, " at lvl ", levels[goID])
					}
				}
				if len(storeIns) == 0 && len(workList) != 0 { // finished reporting current goroutine and workList isn't empty
					nextGoInfo := workList[0] // get the goroutine info at end of workList
					workList = workList[1:]   // pop goroutine info from end of workList
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
				}
				newGoID := goID + 1 // increment goID for child goroutine
				if len(workList) > 0 {
					newGoID = workList[len(workList)-1].goID + 1
				}
				RWIns[goID] = append(RWIns[goID], theIns)
				var info = goroutineInfo{examIns, fnName, newGoID}
				workList = append(workList, info) // store encountered goroutines
				log.Debug(strings.Repeat(" ", levels[goID]), "spawning Goroutine ----->  ", fnName)
			}
		}
	}
}
