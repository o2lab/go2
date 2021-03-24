package main

import (
	"fmt"
	log "github.com/sirupsen/logrus"
	"github.com/twmb/algoimpl/go/graph"
	"github.tamu.edu/April1989/go_tools/go/packages"
	"github.tamu.edu/April1989/go_tools/go/pointer"
	pta0 "github.tamu.edu/April1989/go_tools/go/pointer_default"
	"github.tamu.edu/April1989/go_tools/go/ssa"
	"github.tamu.edu/April1989/go_tools/go/ssa/ssautil"
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
		return false
	}
	if fn.Pkg.Pkg.Name() == "main" || fn.Pkg.Pkg.Name() == "cli" {
		return true
	}
	for _, excluded := range excludedPkgs {
		if fn.Pkg.Pkg.Name() == excluded {
			return false
		}
	}
	if efficiency && a.main.Pkg.Path() != "command-line-arguments" && !strings.HasPrefix(fn.Pkg.Pkg.Path(), a.main.Pkg.Path()) { // path is dependent on tested program
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
	if efficiency && len(mainPkgs) > 1 {
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
			fmt.Scan(&mainInd)
			if mainInd == "-" {
				fmt.Print("Enter function name to begin analysis from: ")
				fmt.Scan(&enterAt)
				for _ , p := range pkgs {
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
		Tests: false,                  // setting Tests will include related test packages
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
	if useDefaultPTA { // default PTA
		runner.ptaConfig0 = &pta0.Config{
			Mains:          mains,
			BuildCallGraph: false,
		}
		runner.ptaResult0, _ = pta0.Analyze(runner.ptaConfig0)
	} else { // new PTA
		scope := make([]string, 1)
		scope[0] = pkgs[0].Pkg.Path() //bz: the 1st pkg has the scope info == the root pkg or default .go input
		runner.ptaConfig = &pointer.Config{
			Mains:          mains, //bz: all mains in a project
			Reflection:     false,
			BuildCallGraph: true,
			Log:            nil,
			Origin:         true, //origin
			//shared config
			K:          1,
			LimitScope: true,  //bz: only consider app methods now -> no import will be considered
			DEBUG:      false, //bz: rm all printed out info in console
			Scope:      scope,        //bz: analyze scope
			Exclusion: excludedPkgs, //bz: copied from race_checker if any
			TrackMore: true,         //bz: track pointers with all types
			Level:     0,            //bz: see pointer.Config
		}
		start := time.Now()                                               //performance
		runner.ptaResult, _ = pointer.AnalyzeMultiMains(runner.ptaConfig) // conduct pointer analysis for multiple mains
		t := time.Now()
		elapsed := t.Sub(start)
		fmt.Println("\nDone  -- PTA/CG Build; Using " + elapsed.String() + ".\n ")
		//!!!! bz: for my debug, please comment off, do not delete
		//fmt.Println("#Receive Result: ", len(runner.ptaResult))
		//for mainEntry, ptaRes := range runner.ptaResult { //bz: you can get the ptaRes for each main here
		//	fmt.Println("Receive ptaRes (#Queries: ", len(ptaRes.Queries), ", #IndirectQueries: ", len(ptaRes.IndirectQueries), ") for main: ", mainEntry.String())
		//	ptaRes.DumpAll()
		//}
	}
	if len(mains) > 1 {
		allEntries = true
	}
	// Iterate each entry point...
	var wg sync.WaitGroup
	for _, m := range mains {
		wg.Add(1)
		go func(main *ssa.Package) {
			defer wg.Done()
			// Configure static analysis...
			Analysis := &analysis{
				ptaRes:   runner.ptaResult,
				ptaRes0:  runner.ptaResult0,
				ptaCfg:   runner.ptaConfig,
				ptaCfg0:  runner.ptaConfig0,
				prog:     runner.prog,
				pkgs:     runner.pkgs,
				mains:    mains,
				main:            main,
				RWinsMap:        make(map[goIns]graph.Node),
				trieMap:         make(map[fnInfo]*trie),
				insDRA:          0,
				levels:          make(map[int]int),
				lockMap:         make(map[ssa.Instruction][]ssa.Value),
				lockSet:         make(map[int][]*lockInfo),
				RlockMap:        make(map[ssa.Instruction][]ssa.Value),
				RlockSet:   	 make(map[int][]*lockInfo),
				goCaller:        make(map[int]int),
				goNames:         make(map[int]string),
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
				loopIDs:  		 make(map[int]int),
			}
			if !allEntries {
				log.Info("Compiling stack trace for every Goroutine... ")
				log.Debug(strings.Repeat("-", 35), "Stack trace begins", strings.Repeat("-", 35))
			}
			Analysis.visitAllInstructions(main.Func(entryFn), 0)
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
			rr := &raceReport{
				entryInfo: main.Pkg.Path(),
			}
			rr.racePairs = Analysis.checkRacyPairs()
			runner.mu.Lock()
			runner.racyStackTops = Analysis.racyStackTops
			runner.finalReport = append(runner.finalReport, rr)
			runner.mu.Unlock()
		}(m)
	}
	wg.Wait()

	if allEntries {
		raceCount := 0
		for k, e := range runner.finalReport {
			if len(e.racePairs) > 0 && e.racePairs[0] != nil {
				log.Info(len(e.racePairs), " races found for entry point No.", k, ": ", e.entryInfo, "...")
				for i, r := range e.racePairs {
					if r != nil {
						e.printRace(i+1, r.insPair, r.addrPair, r.goIDs, r.insInd)
						raceCount++
					}
				}
			} else {
				log.Info("No races found for ", e.entryInfo, "...")
			}
		}
		log.Info("Total of ", raceCount, " races found for all entry points. ")
	}
	execDur := time.Since(startExec)
	log.Info(execDur, " elapsed. ")

	return nil
}
