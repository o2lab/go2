package main

import (
	"bufio"
	"fmt"
	"github.com/april1989/origin-go-tools/go/myutil/flags"
	"github.com/april1989/origin-go-tools/go/packages"
	"github.com/april1989/origin-go-tools/go/pointer"
	pta0 "github.com/april1989/origin-go-tools/go/pointer_default"
	"github.com/april1989/origin-go-tools/go/ssa"
	"github.com/april1989/origin-go-tools/go/ssa/ssautil"
	log "github.com/sirupsen/logrus"
	"github.com/twmb/algoimpl/go/graph"
	"go/token"
	"go/types"
	"sync"

	//"golang.org/x/tools/go/pointer"
	//"golang.org/x/tools/go/ssa"
	"os"
	"strconv"
	"strings"
	"time"
)

// fromPkgsOfInterest determines if a function is from a package of interest
func (a *analysis) fromPkgsOfInterest(fn *ssa.Function) bool {
	if fn.Pkg == nil || fn.Pkg.Pkg == nil {
		if fn.IsFromApp { //bz: do not remove this ... otherwise will miss racy functions
			return true
		}
		return false
	}
	if fn.Pkg.Pkg.Name() == "main" || fn.Pkg.Pkg.Name() == "cli" || fn.Pkg.Pkg.Name() == "testing" {
		return true
	}
	for _, excluded := range excludedPkgs {
		if fn.Pkg.Pkg.Name() == excluded || fn.Pkg.Pkg.Path() == excluded {//bz: some lib's Pkg.Name() == "Package", not the used import xxx; if so, check Pkg.Path()
			return false
		}
	}
	if a.efficiency && a.main.Pkg.Path() != "command-line-arguments" && !strings.HasPrefix(fn.Pkg.Pkg.Path(), strings.Split(a.main.Pkg.Path(), "/")[0]) { // path is dependent on tested program
		return false
	}
	return true
}

// isLocalAddr returns whether location is a local address or not
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
			return true
		}
	default:
		return false
	}
	return false
}

// isSynthetic returns whether fn is synthetic or not
func isSynthetic(fn *ssa.Function) bool { // ignore functions that are NOT true source functions
	return fn.Synthetic != "" || fn.Pkg == nil
}

func pkgSelection(initial []*packages.Package) ([]*ssa.Package, *ssa.Program, []*ssa.Package) {
	var prog *ssa.Program
	var pkgs []*ssa.Package
	var mainPkgs []*ssa.Package

	log.Info("Building SSA code for entire program...")
	prog, pkgs = ssautil.AllPackages(initial, 0)
	if len(pkgs) == 0 {
		log.Errorf("SSA code could not be constructed due to type errors. ")
	}
	prog.Build()
	noFunc := len(ssautil.AllFunctions(prog))
	mainPkgs = ssautil.MainPackages(pkgs)
	if len(mainPkgs) == 0 {
		log.Errorf("No main function detected. ")
	}
	log.Info("Done  -- SSA code built. ", len(pkgs), " packages and ", noFunc, " functions detected. ")

	var mainInd string
	var enterAt string
	var mains []*ssa.Package
	userEP := false // user specified entry function
	if len(mainPkgs) > 1 {
		// Provide entry-point options and retrieve user selection
		fmt.Println(len(mainPkgs), "main() entry-points identified: ")
		for i, ep := range mainPkgs {
			fmt.Println("Option", i+1, ": ", ep.String())
		}
		if allEntries { // no selection from user required
			for pInd := 0; pInd < len(mainPkgs); pInd++ {
				mains = append(mains, mainPkgs[pInd])
			}
			log.Info("Iterating through all entry point options...")
		} else {
			fmt.Print("Enter option number of choice: \n")
			fmt.Print("*** use space delimiter for multiple selections *** \n")
			fmt.Print("*** use \"-\" for a range of selections *** \n")
			fmt.Print("*** if selecting a test folder (ie. ending in .test), please select ONE at a time *** \n")
			fmt.Scan(&mainInd)
			if mainInd == "-" {
				fmt.Print("Enter function name to begin analysis from: ")
				fmt.Scan(&enterAt)
				for _, p := range pkgs {
					if p != nil {
						if fnMem, okf := p.Members[enterAt]; okf { // package contains function to enter at
							userEP = true
							mains = append(mainPkgs, p)
							entryFn = enterAt // start analysis at user specified function
							_ = fnMem
						}
					}
				}
				if !userEP {
					fmt.Print("Function not found. ") // TODO: request input again
				}
			} else if strings.Contains(mainInd, ",") { // multiple selections
				selection := strings.Split(mainInd, ",")
				for _, s := range selection {
					i, _ := strconv.Atoi(s) // convert to integer
					mains = append(mains, mainPkgs[i-1])
				}
			} else if strings.Contains(mainInd, "-") { // selected range
				selection := strings.Split(mainInd, "-")
				begin, _ := strconv.Atoi(selection[0])
				end, _ := strconv.Atoi(selection[1])
				for i := begin; i <= end; i++ {
					mains = append(mains, mainPkgs[i-1])
				}
			} else if i, err0 := strconv.Atoi(mainInd); err0 == nil {
				mains = append(mains, mainPkgs[i-1])
			}
		}
	} else {
		mains = mainPkgs
	}
	return mains, prog, pkgs
}

func (runner *AnalysisRunner) Run(args []string) error {
	trieLimit = runner.trieLimit
	efficiency = runner.efficiency
	// Load packages...
	cfg := &packages.Config{
		Mode:  packages.LoadAllSyntax, // the level of information returned for each package
		Dir:   "",                     // directory in which to run the build system's query tool
		//TODO: bz: change this to true if you want to analyze test
		Tests: true,
		//Tests: false,                  // setting Tests will include related test packages
	}
	log.Info("Loading input packages...")

	os.Stderr = nil // No need to output package errors for now. Delete this line to view package errors
	initial, _ := packages.Load(cfg, args...)
	if len(initial) == 0 {
		return fmt.Errorf("No Go files detected. ")
	}
	log.Info("Done  -- ", len(initial), " packages detected. ")

	mains, prog, pkgs := pkgSelection(initial)
	runner.prog = prog
	runner.pkgs = pkgs
	startExec := time.Now() // measure total duration of running entire code base
	// Configure pointer analysis...
	if useNewPTA {
		var scope = make([]string, 1)
		//if pkgs[0] != nil { // Note: only if main dir contains main.go.
		//	scope[0] = pkgs[0].Pkg.Path() //bz: the 1st pkg has the scope info == the root pkg or default .go input
		//} else
		if pkgs[0] == nil && len(pkgs) == 1 {
			log.Fatal("Error: No packages detected. Please verify directory provided contains Go Files. ")
		} else if len(pkgs) > 1 && pkgs[1] != nil && !strings.Contains(pkgs[1].Pkg.Path(), "/") {
			scope[0] = pkgs[1].Pkg.Path()
		} else if len(pkgs) > 1 {
			scope[0] = strings.Split(pkgs[1].Pkg.Path(), "/")[0] + "/" + strings.Split(pkgs[1].Pkg.Path(), "/")[1]
			if strings.Contains(pkgs[1].Pkg.Path(), "checker") {
				scope[0] += "/race-checker"
			}
			if strings.Contains(pkgs[1].Pkg.Path(), "ethereum") {
				scope[0] += "/go-ethereum"
			}
		}
		//scope[0] = "google.golang.org/grpc" //bz: the scope obtained by the above code is "google.golang.org/grpc/health" if you run this under the /health directory, which is not what we want; hard code here

		//bz: update a bit to avoid duplicate scope addition, e.g., grpc
		// return with error if error exist, skip panic
		if len(scope) == 0 && len(pkgs) >= 1 && !goTest {           // ** assuming more than one package detected in real programs
			path, err := os.Getwd() //current working directory == project path
			if err != nil {
				panic("Error while os.Getwd: " + err.Error())
			}
			gomodFile, err := os.Open(path + "/go.mod") // For read access.
			if err != nil {
				return fmt.Errorf("Error while reading go.mod: " + err.Error())
			}
			defer gomodFile.Close()
			scanner := bufio.NewScanner(gomodFile)
			var mod string
			for scanner.Scan() {
				s := scanner.Text()
				if strings.HasPrefix(s, "module ") {
					mod = s
					break //this is the line "module xxx.xxx.xx/xxx"
				}
			}
			if mod == "" {
				return fmt.Errorf("Cannot find go.mod in default location: " + gomodFile.Name())
			}
			if err2 := scanner.Err(); err2 != nil {
				return fmt.Errorf("Error while scanning go.mod: " + err2.Error())
			}
			parts := strings.Split(mod, " ")
			scope = append(scope, parts[1])
		}

		flags.DoTests = true //bz: set to true if your folder has tests and you want to analyze them
		runner.ptaConfig = &pointer.Config{
			Mains:          mains, //bz: all mains/tests in a project
			Reflection:     false,
			BuildCallGraph: true,
			Log:            nil,
			Origin:         true, //origin
			//shared config
			K:          1,
			LimitScope: true,         //bz: only consider app methods with origin
			Scope:      scope,        //bz: analyze scope
			Exclusion:  excludedPkgs, //bz: copied from race_checker if any
			TrackMore:  true,         //bz: track pointers with all types
			Level:      0,            //bz: see pointer.Config
		}
		start := time.Now()                                               //performance
		var err error
		runner.ptaResult, err = pointer.AnalyzeMultiMains(runner.ptaConfig) // conduct pointer analysis for multiple mains
		if err != nil {
			panic("Pointer Analysis Error: " + err.Error())
		}

		t := time.Now()
		elapsed := t.Sub(start)
		fmt.Println("\nDone  -- PTA/CG Build; Using " + elapsed.String() + ".\n ")
		//!!!! bz: for my debug, please comment off, do not delete
		//fmt.Println("#Receive Result: ", len(runner.ptaResult))
		//for mainEntry, ptaRes := range runner.ptaResult { //bz: you can get the ptaRes for each main here
		//	fmt.Println("Receive ptaRes (#Queries: ", len(ptaRes.Queries), ", #IndirectQueries: ", len(ptaRes.IndirectQueries), ") for main: ", mainEntry.String())
		//	ptaRes.DumpAll()
		//}

	} else {
		runner.ptaConfig0 = &pta0.Config{
			Mains: mains,
			BuildCallGraph: false,
		}
		runner.ptaResult0, _ = pta0.Analyze(runner.ptaConfig0)
	}

	if len(mains) > 1 {
		allEntries = true
	}
	if strings.Contains(mains[0].String(), ".test") {
		fmt.Println("Extracting test functions from PTA/CG...")
		for mainEntry, ptaRes := range runner.ptaResult {
			tests := ptaRes.GetTests()
			if tests == nil {
				continue //this is a main entry
			}
			fmt.Println("The following are functions found within: ", mainEntry)
			var testSelect string
			var testFns []*ssa.Function
			var selectedFns []*ssa.Function
			counter := 1
			for fn := range tests {
				fmt.Println("Option", counter, ": ", fn.Name())
				testFns = append(testFns, fn)
				counter++
			}
			fmt.Print("Enter option number of choice or test name: \n")
			fmt.Scan(&testSelect)
			if strings.Contains(testSelect, ",") && testFns != nil { // multiple selections
				selection := strings.Split(testSelect, ",")
				for _, s := range selection {
					i, _ := strconv.Atoi(s) // convert to integer
					selectedFns = append(selectedFns, testFns[i-1]) // TODO: analyze multiple tests concurrently
				}
			} else if strings.Contains(testSelect, "-") && testFns != nil { // selected range
				selection := strings.Split(testSelect, "-")
				begin, _ := strconv.Atoi(selection[0])
				end, _ := strconv.Atoi(selection[1])
				for i := begin; i <= end; i++ {
					selectedFns = append(selectedFns, testFns[i-1]) // TODO: analyze multiple tests concurrently
				}
			} else if i, err0 := strconv.Atoi(testSelect); err0 == nil && testFns != nil {
				selectedFns = append(selectedFns, testFns[i-1]) // TODO: analyze multiple tests concurrently
				testEntry = testFns[i-1]
				entryFn = testEntry.Name()
			} else if strings.Contains(testSelect, "Test") {
				for _, fn := range testFns {
					if fn.Name() == testSelect {
						testEntry = fn
						entryFn = testEntry.Name()
					}
				}
			} else {
				log.Error("Unrecognized input, try again.")
			}
		}
		fmt.Println("Done  -- CG node of test function ", entryFn, " extracted...")
	}

	// Iterate each entry point...
	var wg sync.WaitGroup
	for _, m := range mains {
		wg.Add(1)
		go func(main *ssa.Package) {
			// Configure static analysis...
			Analysis := analysis{
				ptaRes:          runner.ptaResult,
				ptaRes0:         runner.ptaResult0,
				ptaCfg:          runner.ptaConfig,
				ptaCfg0:         runner.ptaConfig0,
				efficiency:      efficiency,
				trieLimit:   	 trieLimit,
				getGo:   		 getGo,
				prog:            runner.prog,
				pkgs:            runner.pkgs,
				mains:           mains,
				main:            main,
				RWinsMap:        make(map[goIns]graph.Node),
				trieMap:         make(map[fnInfo]*trie),
				stackMap:  		 make(map[fnCallIns]stackInfo),
				insDRA:          -1,
				levels:          make(map[int]int),
				lockMap:         make(map[ssa.Instruction][]ssa.Value),
				lockSet:         make(map[int][]*lockInfo),
				RlockMap:        make(map[ssa.Instruction][]ssa.Value),
				RlockSet:        make(map[int][]*lockInfo),
				goCaller:        make(map[int]int),
				goCalls:         make(map[int]*goCallInfo),
				chanToken:       make(map[string]string),
				chanBuf:         make(map[string]int),
				chanRcvs:        make(map[string][]*ssa.UnOp),
				chanSnds:        make(map[string][]*ssa.Send),
				selectBloc:      make(map[int]*ssa.Select),
				selReady:        make(map[*ssa.Select][]string),
				selUnknown:      make(map[*ssa.Select][]string),
				selectCaseBegin: make(map[ssa.Instruction]string),
				selectCaseEnd:   make(map[ssa.Instruction]string),
				selectCaseBody:  make(map[ssa.Instruction]*ssa.Select),
				selectDone:      make(map[ssa.Instruction]*ssa.Select),
				ifSuccBegin:     make(map[ssa.Instruction]*ssa.If),
				ifFnReturn:      make(map[*ssa.Function]*ssa.Return),
				ifSuccEnd:       make(map[ssa.Instruction]*ssa.Return),
				inLoop:          false,
				goInLoop:        make(map[int]bool),
				loopIDs:         make(map[int]int),
				allocLoop:       make(map[*ssa.Function][]string),
				bindingFV:       make(map[*ssa.Go][]*ssa.FreeVar),
				commIDs:  		 make(map[int][]int),
				deferToRet: 	 make(map[*ssa.Defer]ssa.Instruction),
			}
			if strings.Contains(main.Pkg.Path(), "GoBench") { // for testing purposes
				Analysis.efficiency = false
				Analysis.trieLimit = 2
			} else if !goTest {
				Analysis.efficiency = true
			}
			if !allEntries {
				log.Info("Compiling stack trace for every Goroutine... ")
				log.Debug(strings.Repeat("-", 35), "Stack trace begins", strings.Repeat("-", 35))
			}
			if testEntry != nil {
				Analysis.visitAllInstructions(testEntry, 0)
			} else {
				Analysis.visitAllInstructions(main.Func(entryFn), 0)
			}

			if !allEntries {
				log.Debug(strings.Repeat("-", 35), "Stack trace ends", strings.Repeat("-", 35))
			}
			totalIns := 0
			for g := range Analysis.RWIns {
				totalIns += len(Analysis.RWIns[g])
			}
			if !allEntries {
				log.Info("Done  -- ", len(Analysis.RWIns), " goroutines analyzed! ", totalIns, " instructions of interest detected! ")
			}
			if len(mains) > 1 {
				Analysis.getGo = false // turn off debug logging if running in parallel
			}
			if Analysis.getGo { // print call stack of each goroutine
				for i := 0; i < len(Analysis.RWIns); i++ {
					name := "main"
					if i > 0 {
						name = Analysis.goNames(Analysis.goCalls[i].goIns)
					}
					if Analysis.goInLoop[i] {
						log.Debug("Goroutine ", i, "  --  ", name, strings.Repeat(" *", 10), " spawned by a loop", strings.Repeat(" *", 10))
					} else {
						log.Debug("Goroutine ", i, "  --  ", name)
					}
					if i > 0 {
						log.Debug("call stack: ")
					}
					var pathGo []int
					goID := i
					for goID > 0 {
						pathGo = append([]int{goID}, pathGo...)
						temp := Analysis.goCaller[goID]
						goID = temp
					}
					if !allEntries {
						for q, eachGo := range pathGo {
							eachStack := Analysis.goStack[eachGo]
							for k, eachFn := range eachStack {
								if k == 0 {
									log.Debug("\t ", strings.Repeat(" ", q), "--> Goroutine: ", eachFn.fnIns.Name(), "[", Analysis.goCaller[eachGo], "] ", Analysis.prog.Fset.Position(eachFn.ssaIns.Pos()))
								} else {
									log.Debug("\t   ", strings.Repeat(" ", q), strings.Repeat(" ", k), eachFn.fnIns.Name(), " ", Analysis.prog.Fset.Position(eachFn.ssaIns.Pos()))
								}
							}
						}
					}
				}
			}
			if useDefaultPTA {
				Analysis.ptaRes0, _ = pta0.Analyze(Analysis.ptaCfg0) // all queries have been added, conduct pointer analysis
			}
			if !allEntries {
				log.Info("Building Happens-Before graph... ")
			}
			// confirm channel readiness for unknown select cases:
			if len(Analysis.selUnknown) > 0 {
				for sel, chs := range Analysis.selUnknown {
					for i, ch := range chs {
						if _, ready := Analysis.chanSnds[ch]; !ready && ch != "" {
							if _, ready0 := Analysis.chanRcvs[ch]; !ready0 {
								if _, ready1 := Analysis.chanBuf[Analysis.chanToken[ch]]; !ready1 {
									Analysis.selReady[sel][i] = ""
								}
							}
						}
					}
				}
			}
			Analysis.HBgraph = graph.New(graph.Directed)
			Analysis.buildHB()
			if !allEntries {
				log.Info("Done  -- Happens-Before graph built ")
				log.Info("Checking for data races... ")
			}
			rr := raceReport{
				entryInfo: main.Pkg.Path(),
			}
			rr.racePairs = Analysis.checkRacyPairs()
			runner.mu.Lock()
			runner.racyStackTops = Analysis.racyStackTops
			runner.finalReport = append(runner.finalReport, rr)
			runner.mu.Unlock()
			wg.Done()
		}(m)
	}
	wg.Wait()

	raceCount := 0
	for _, e := range runner.finalReport {
		if len(e.racePairs) > 0 && e.racePairs[0] != nil {
			log.Info(len(e.racePairs), " races found for entry point ", e.entryInfo, "...")
			raceCount += len(e.racePairs)
		} else {
			log.Info("No races found for ", e.entryInfo, "...")
		}
	}
	log.Info("Total of ", raceCount, " races found for all entry points. ")

	execDur := time.Since(startExec)
	log.Info(execDur, " elapsed. ")

	return nil
}
