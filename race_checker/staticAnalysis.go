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

var scope []string //bz: now extract scope from pkgs


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
	if !strings.HasPrefix(fn.Pkg.Pkg.Path(), a.fromPath) { // path is dependent on tested program
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
			fmt.Print("Enter option number of choice: (or enter \"-\" for other desired entry point)\n")
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

type raceInfo struct {
	insPair 		[]ssa.Instruction
	addrPair 		[]ssa.Value
	goIDs 			[]int
	insInd 			[]int
	total 			int
}

type raceReport struct {
	entryInfo		string
	racePairs		[]*raceInfo
	noGoroutines	int
}

func (runner *AnalysisRunner) Run(args []string) error {
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

	if efficiency && !allEntries { // an entry point was selected by user
		runner.fromPath = initial[0].PkgPath
	}
	log.Info("Done  -- ", len(initial), " packages detected. ")

	mains, prog, pkgs := pkgSelection(initial)
	runner.prog = prog
	runner.pkgs = pkgs

	if useDefaultPTA {
		var wg sync.WaitGroup
		var finalReport []*raceReport
		runner.pta0Cfg = &pta0.Config{
			Mains:          mains,
			BuildCallGraph: false,
		}
		runner.ptaResult, _ = pta0.Analyze(runner.pta0Cfg) // conduct pointer analysis (default version)
		runner.Analysis = &analysis{
			useNewPTA:       useNewPTA,
			pta0Result:      runner.ptaResult,
			useDefaultPTA: 	 useDefaultPTA,
			ptaConfig:       runner.ptaconfig,
			pta0Cfg:         runner.pta0Cfg,
			fromPath:  		 "",
			prog:            runner.prog,
			pkgs:            runner.pkgs,
			mains:           mains,
			RWIns:  		 make(map[string][][]ssa.Instruction),
			RWinsMap:        make(map[goIns]graph.Node),
			insDRA:          0,
			levels:          make(map[int]int),
			lockMap:         make(map[ssa.Instruction][]ssa.Value),
			RlockMap:        make(map[ssa.Instruction][]ssa.Value),
			goLockset:       make(map[int][]ssa.Value),
			goRLockset:      make(map[int][]ssa.Value),
			mapFreeze:       false,
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
		}
		// first forloop for collecting pta data from all entry points
		for _, m := range mains {
			if efficiency && allEntries { // iterate all entry points
				runner.Analysis.fromPath = m.Pkg.Path()
			} else if efficiency && !allEntries { // an entry point was selected by user
				runner.Analysis.fromPath = runner.fromPath
			} else { // running tests
				runner.Analysis.fromPath = m.Pkg.Path()
			}
			if !allEntries {
				log.Info("Compiling stack trace for every Goroutine... ")
				log.Debug(strings.Repeat("-", 35), "Stack trace begins", strings.Repeat("-", 35))
			}
			runner.Analysis.visitAllInstructions(m.Func(entryFn), 0)
			if !allEntries {
				log.Debug(strings.Repeat("-", 35), "Stack trace ends", strings.Repeat("-", 35))
			}
			totalIns := 0
			for g := range runner.Analysis.RWIns {
				totalIns += len(runner.Analysis.RWIns[g])
			}

			if !allEntries {
				log.Info("Done  -- ", len(runner.Analysis.RWIns), " goroutines analyzed! ", totalIns, " instructions of interest detected! ")
			}

			finResult, err9 := pta0.Analyze(runner.Analysis.pta0Cfg) // all queries have been added, conduct pointer analysis
			if err9 != nil {
				log.Fatal(err9)
			}
			runner.Analysis.pta0Result = finResult
		}
		// second forloop for race checking using pta info obtained from first forloop
		for _, m := range mains {
			wg.Add(1)
			go func(main *ssa.Package) {
				defer wg.Done()

				if !allEntries {
					log.Info("Building Happens-Before graph... ")
				}

				analysisData := &analysis{
					useDefaultPTA:  runner.Analysis.useDefaultPTA,
					pta0Result: 	runner.Analysis.pta0Result,
					pta0Cfg:   	  	runner.Analysis.pta0Cfg,
					fromPath: 		main.Pkg.Path(),
					prog: 			runner.Analysis.prog,
					pkgs: 			runner.Analysis.pkgs,
					RWinsMap: 		runner.Analysis.RWinsMap,
					RWIns: 			runner.Analysis.RWIns,
					insDRA: 		runner.Analysis.insDRA,
					lockMap:    	runner.Analysis.lockMap,
					RlockMap:		runner.Analysis.RlockMap,
					goStack:    	runner.Analysis.goStack,
					goCaller:   	runner.Analysis.goCaller,
					goNames:    	runner.Analysis.goNames,
					chanToken:  	runner.Analysis.chanToken,
					chanBuf:    	runner.Analysis.chanBuf,
					chanRcvs:   	runner.Analysis.chanRcvs,
					chanSnds:   	runner.Analysis.chanSnds,
					selectBloc: 	runner.Analysis.selectBloc,
					selReady:   	runner.Analysis.selReady,
					selUnknown: 	runner.Analysis.selUnknown,
					selectCaseBegin:runner.Analysis.selectCaseBegin,
					selectCaseEnd: 	runner.Analysis.selectCaseEnd,
					selectCaseBody: runner.Analysis.selectCaseBody,
					selectDone: 	runner.Analysis.selectDone,
					ifSuccBegin: 	runner.Analysis.ifSuccBegin,
					ifSuccEnd:  	runner.Analysis.ifSuccEnd,
					ifFnReturn: 	runner.Analysis.ifFnReturn,
					commIfSucc: 	runner.Analysis.commIfSucc,
					omitComm:   	runner.Analysis.omitComm,
					racyStackTops: 	runner.Analysis.racyStackTops,
				}

				analysisData.RWInsInd = analysisData.RWIns[analysisData.fromPath]

				// confirm channel readiness for unknown select cases:
				if len(analysisData.selUnknown) > 0 {
					for sel, chs := range analysisData.selUnknown {
						for i, ch := range chs {
							if _, ready := analysisData.chanSnds[ch]; !ready && ch != "" {
								if _, ready0 := analysisData.chanRcvs[ch]; !ready0 {
									if _, ready1 := analysisData.chanBuf[analysisData.chanToken[ch]]; !ready1 {
										analysisData.selReady[sel][i] = ""
									}
								}
							}
						}
					}
				}

				analysisData.HBgraph = graph.New(graph.Directed)
				analysisData.buildHB()

				if !allEntries {
					log.Info("Done  -- Happens-Before graph built ")

					log.Info("Checking for data races... ")
				}
				rr := &raceReport{
					entryInfo: main.Pkg.Path(),
				}
				rr.racePairs = analysisData.checkRacyPairs()
				runner.Analysis.racyStackTops = analysisData.racyStackTops

				if !allEntries {
					log.Info("Done for entry at " + main.Pkg.Path())
				}
				finalReport = append(finalReport, rr)
			}(m)
		}
		wg.Wait()
	} else {
		var wg1 sync.WaitGroup
		var finalReport []*raceReport
		for _, m := range mains {
			wg1.Add(1)
			go func(main *ssa.Package) {
				goReport := runner.runWithEntryPoint(main)
				finalReport = append(finalReport, goReport)
				wg1.Done()
			}(m)
		}
		wg1.Wait()
	}

	return nil
}

func (runner *AnalysisRunner) runWithEntryPoint(main *ssa.Package) *raceReport {
	rr := &raceReport{
		entryInfo: main.Pkg.Path(),
	}
	var result *pointer.Result
	if useNewPTA {
		result = runner.runEachMainBaseline(main)
	}
	analysisData := &analysis{
		useNewPTA:       useNewPTA,
		result: 		 result,
		pta0Result:      runner.ptaResult,
		useDefaultPTA: 	 useDefaultPTA,
		ptaConfig:       runner.ptaconfig,
		pta0Cfg:         runner.pta0Cfg,
		fromPath:  		 "",
		prog:            runner.prog,
		pkgs:            runner.pkgs,
		mains:           []*ssa.Package{main},
		RWinsMap:        make(map[goIns]graph.Node),
		insDRA:          0,
		levels:          make(map[int]int),
		lockMap:         make(map[ssa.Instruction][]ssa.Value),
		RlockMap:        make(map[ssa.Instruction][]ssa.Value),
		goLockset:       make(map[int][]ssa.Value),
		goRLockset:      make(map[int][]ssa.Value),
		mapFreeze:       false,
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
	}
	if efficiency && allEntries { // iterate all entry points
		analysisData.fromPath = main.Pkg.Path()
	} else if efficiency && !allEntries { // an entry point was selected by user
		analysisData.fromPath = runner.fromPath
	}
	// for new PTA: extract scope from pkgs
	if analysisData.fromPath != "" {
		scope = []string{analysisData.fromPath}
	}
	if !allEntries {
		log.Info("Compiling stack trace for every Goroutine... ")
		log.Debug(strings.Repeat("-", 35), "Stack trace begins", strings.Repeat("-", 35))
	}
	analysisData.visitAllInstructions(main.Func(entryFn), 0)
	if !allEntries {
		log.Debug(strings.Repeat("-", 35), "Stack trace ends", strings.Repeat("-", 35))
	}
	totalIns := 0
	for g := range analysisData.RWIns {
		totalIns += len(analysisData.RWIns[g])
	}

	if !allEntries {
		log.Info("Done  -- ", len(analysisData.RWIns), " goroutines analyzed! ", totalIns, " instructions of interest detected! ")
	}


	// confirm channel readiness for unknown select cases:
	if len(analysisData.selUnknown) > 0 {
		for sel, chs := range analysisData.selUnknown {
			for i, ch := range chs {
				if _, ready := analysisData.chanSnds[ch]; !ready && ch != "" {
					if _, ready0 := analysisData.chanRcvs[ch]; !ready0 {
						if _, ready1 := analysisData.chanBuf[analysisData.chanToken[ch]]; !ready1 {
							analysisData.selReady[sel][i] = ""
						}
					}
				}
			}
		}
	}

	if !allEntries {
		log.Info("Building Happens-Before graph... ")
	}

	analysisData.HBgraph = graph.New(graph.Directed)
	//analysisData.buildHB()

	if !allEntries {
		log.Info("Done  -- Happens-Before graph built ")

		log.Info("Checking for data races... ")
	}

	rr.racePairs = analysisData.checkRacyPairs()

	if !allEntries {
		log.Info("Done for entry at " + main.Pkg.Path())
	}

	runner.Analysis = analysisData
	return rr
}

//bz: do each main one by one -> performance base line
func (runner *AnalysisRunner) runEachMainBaseline(main *ssa.Package) *pointer.Result {
	logfile, err := os.Create("go_pta_log") //bz: for me ...
	if err != nil {
		log.Fatal(err)
	}
	var mains []*ssa.Package
	mains = append(mains, main)
	// Configure pointer analysis to build call-graph
	runner.ptaconfig = &pointer.Config{
		Mains:          mains, //bz: NOW assume only one main
		Reflection:     false,
		BuildCallGraph: true,
		Log:            logfile,
		//CallSiteSensitive: true, //kcfa
		Origin: true, //origin
		//shared config
		K:          1,
		LimitScope: true,         //bz: only consider app methods now
		DEBUG:      false,   //bz: do all printed out info in console --> turn off to avoid internal nil reference panic
		Scope:      scope,        //bz: analyze scope, default is "command-line-arguments"
		Exclusion: excludedPkgs, //excludedPkgs here
		Level:      0,
		//bz: Level = 1: if callee is from app or import
		// Level = 2: parent of caller in app, caller in lib, callee also in lib || parent in lib, caller in app, callee in lib || parent in lib, caller in lib, callee in app
		// Level = 3: this also analyze lib's import == lib's lib
		// Level = 0: analyze all
		TrackMore:      true, //bz: track pointers with types declared in Analyze Scope; cannot guarantee all basic types, e.g., []bytes, etc.
	}

	start := time.Now()
	result, err2 := pointer.Analyze(runner.ptaconfig) // conduct pointer analysis (customized version)
	t := time.Now()
	elapsed := t.Sub(start)
	log.Info("Done -- PTA/CG Built; Using " + elapsed.String() + ". Go check go_pta_log for detail. ")

	if err2 != nil {
		log.Fatal(err2)
	}
	if runner.ptaconfig.DEBUG {
		result.DumpAll()
	}

	return result
}

