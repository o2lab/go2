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
	if !strings.HasPrefix(fn.Pkg.Pkg.Path(), fromPath) { // path is dependent on tested program
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
	if initial[0] == nil { //bz: no matter what ...
		log.Panic("nil initial package")
	}

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
		if allEntries {
			for pInd := 0; pInd < len(mainPkgs); pInd++ {
				mains = append(mains, mainPkgs[pInd])
			}
			log.Info("Iterating through all options...")
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

func (runner *AnalysisRunner) Run(args []string) error {
	cfg := &packages.Config{
		Mode:  packages.LoadAllSyntax, // the level of information returned for each package
		Dir:   "",                     // directory in which to run the build system's query tool
		Tests: false,                  // setting Tests will include related test packages
	}
	log.Info("Loading input packages...")

	os.Stderr = nil // No need to output package errors for now. Delete this line to view package errors
	initial, _ := packages.Load(cfg, args...)
	//if err != nil {
	//	return err
	//}
	if len(initial) == 0 {
		return fmt.Errorf("No Go files detected. ")
	}

	if efficiency {
		fromPath = initial[0].PkgPath
	}
	log.Info("Done  -- ", len(initial), " packages detected. ")

	mains, prog, pkgs := pkgSelection(initial)

	for _, m := range mains { // TODO: parallelize this step
		log.Info("Solving for entry at " + m.Pkg.Path())
		result, ptaResult := runner.runEachMainBaseline(m)
		runner.Analysis = &analysis{
			useNewPTA:       useNewPTA,
			result: 		 result,
			pta0Result:      ptaResult,
			useDefaultPTA: 	 useDefaultPTA,
			prog:            prog,
			pkgs:            pkgs,
			mains:           []*ssa.Package{m},
			ptaConfig:       runner.ptaconfig,
			pta0Cfg:         runner.pta0Cfg,
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
		//go func() {
			log.Info("Compiling stack trace for every Goroutine... ")
			log.Debug(strings.Repeat("-", 35), "Stack trace begins", strings.Repeat("-", 35))
			runner.Analysis.visitAllInstructions(mains[0].Func(entryFn), 0)
			log.Debug(strings.Repeat("-", 35), "Stack trace ends", strings.Repeat("-", 35))
			totalIns := 0
			for g := range runner.Analysis.RWIns {
				totalIns += len(runner.Analysis.RWIns[g])
			}
			log.Info("Done  -- ", len(runner.Analysis.RWIns), " goroutines analyzed! ", totalIns, " instructions of interest detected! ")

			// confirm channel readiness for unknown select cases:
			if len(runner.Analysis.selUnknown) > 0 {
				for sel, chs := range runner.Analysis.selUnknown {
					for i, ch := range chs {
						if _, ready := runner.Analysis.chanSnds[ch]; !ready && ch != "" {
							if _, ready0 := runner.Analysis.chanRcvs[ch]; !ready0 {
								if _, ready1 := runner.Analysis.chanBuf[runner.Analysis.chanToken[ch]]; !ready1 {
									runner.Analysis.selReady[sel][i] = ""
								}
							}
						}
					}
				}
			}

			if useDefaultPTA {
				finResult, err9 := pta0.Analyze(runner.Analysis.pta0Cfg) // all queries have been added, conduct pointer analysis
				if err9 != nil {
					log.Fatal(err9)
				}
				runner.Analysis.pta0Result = finResult
			}

			log.Info("Building Happens-Before graph... ")
			runner.Analysis.HBgraph = graph.New(graph.Directed)
			runner.Analysis.buildHB(runner.Analysis.HBgraph)
			log.Info("Done  -- Happens-Before graph built ")

			log.Info("Checking for data races... ")
			runner.Analysis.checkRacyPairs()

			log.Info("Done for entry at " + m.Pkg.Path())
		//}()
	}
	return nil
}

//bz: do each main one by one -> performance base line
func (runner *AnalysisRunner) runEachMainBaseline(main *ssa.Package) (*pointer.Result, *pta0.Result) {
	logfile, err := os.Create("go_pta_log") //bz: for me ...
	if err != nil {
		log.Fatal(err)
	}

	var scope []string
	if fromPath != "" {
		scope = []string{fromPath}
	}
	//scope = append(scope, "istio.io/istio/")
	//scope = append(scope, "google.golang.org/grpc")
	//scope = append(scope, "github.com/pingcap/tidb")
	if strings.EqualFold(main.String(), "package command-line-arguments") {//default
		scope = append(scope, "command-line-arguments")
	}else{
		scope = append(scope, main.Pkg.Path())
	}

	var mains []*ssa.Package
	mains = append(mains, main)
	if !useDefaultPTA {
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
			DiscardQueries: true, //bz: new flag -> if we use queries
			Level:      0,
			//bz: Level = 1: if callee is from app or import
			// Level = 2: parent of caller in app, caller in lib, callee also in lib || parent in lib, caller in app, callee in lib || parent in lib, caller in lib, callee in app
			// Level = 3: this also analyze lib's import == lib's lib
			// Level = 0: analyze all

			//bz: new api
			UseQueriesAPI:  true, //bz: change the api the same as default pta
			TrackMore:      true, //bz: track pointers with types declared in Analyze Scope; cannot guarantee all basic types, e.g., []bytes, etc.
		}
	} else {
		runner.pta0Cfg = &pta0.Config{
			Mains:          mains,
			BuildCallGraph: false,
		}
	}

	var result *pointer.Result
	var ptaResult *pta0.Result
	var err2 error
	if useDefaultPTA {
		ptaResult, err2 = pta0.Analyze(runner.pta0Cfg) // conduct pointer analysis (default version)
	} else if useNewPTA {
		start := time.Now()
		result, err2 = pointer.Analyze(runner.ptaconfig) // conduct pointer analysis (customized version)
		t := time.Now()
		elapsed := t.Sub(start)
		log.Info("Done -- PTA/CG Built; Using " + elapsed.String() + ". Go check go_pta_log for detail. ")
	}
	if err2 != nil {
		log.Fatal(err2)
	}
	if useNewPTA && runner.ptaconfig.DEBUG {
		result.DumpAll()
	}

	return result, ptaResult
}

