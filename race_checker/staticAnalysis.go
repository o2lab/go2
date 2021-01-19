package main

import (
	"fmt"
	log "github.com/sirupsen/logrus"
	"github.com/twmb/algoimpl/go/graph"
	"github.tamu.edu/April1989/go_tools/go/packages"
	"github.tamu.edu/April1989/go_tools/go/pointer"
	"github.tamu.edu/April1989/go_tools/go/ssa"
	"github.tamu.edu/April1989/go_tools/go/ssa/ssautil"
	"go/token"
	"go/types"
	"os"
	"strconv"
	"strings"
	"time"
)

// fromPkgsOfInterest determines if a function is from a package of interest
func fromPkgsOfInterest(fn *ssa.Function) bool {
	if fn.Pkg == nil || fn.Pkg.Pkg == nil {
		return false
	}
	for _, excluded := range excludedPkgs {
		if fn.Pkg.Pkg.Name() == excluded {
			return false
		}
	}
	if fn.Pkg.Pkg.Name() == "main" || fn.Pkg.Pkg.Name() == "cli" {
		return true
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
	if efficiency && len(initial) > 0 {
		errSize, errPkgs := packages.PrintErrorsAndMore(initial) //bz: errPkg will be nil in initial
		if errSize > 0 {
			//log.Info("Excluded the following packages contain errors, due to the above errors. ")
			//for i, errPkg := range errPkgs {
			//	log.Info(i, " ", errPkg.ID)
			//}
			//log.Info("Continue   -- ")
			_ = errPkgs
		}
	} else if len(initial) == 0 {
		log.Panic("package list empty")
	}

	var prog *ssa.Program
	var pkgs []*ssa.Package
	var mainPkgs []*ssa.Package

	log.Info("Building SSA code for entire program...")
	prog, pkgs = ssautil.AllPackages(initial, 0)
	prog.Build()
	noFunc := len(ssautil.AllFunctions(prog))
	mainPkgs = ssautil.MainPackages(pkgs)
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
		fmt.Print("Enter option number of choice: (or enter - for other desired entry point)\n")
		fmt.Scan(&mainInd)
		if mainInd == "-" {
			fmt.Print("Enter function name to begin analysis from: ")
			fmt.Scan(&enterAt)
			for _, p := range pkgs {
				if p.Func(enterAt) != nil {
					userEP = true
					mains = append(mainPkgs, p)
					entryFn = enterAt // start analysis at user specified function
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
	startLoad := time.Now()
	os.Stderr = nil // No need to output package errors for now. Delete this line to view package errors
	initial, err := packages.Load(cfg, args...)
	if err != nil {
		return err
	}
	t := time.Now()
	elapsedLoad := t.Sub(startLoad)
	log.Info("Done  -- Using ", elapsedLoad.String())
	mains, prog, pkgs := pkgSelection(initial)


	//prog, pkgs := ssautil.AllPackages(mainPkgs, 0)
	//log.Info("Building SSA code for entire program...")
	//prog.Build()
	//log.Info("Done  -- SSA code built. ")
	//
	//mains, err := mainSSAPackages(pkgs)
	//if err != nil {
	//	return err
	//}

	for _, m := range mains {
		log.Info("Solving for " + m.String() + "... ")
		result := runner.runEachMainBaseline(m)
		runner.Analysis = &analysis{
			useNewPTA:       true,
			result: 		 result,
			prog:            prog,
			pkgs:            pkgs,
			mains:           []*ssa.Package{m},
			ptaConfig:       runner.ptaconfig,
			goID2info:       make(map[int]goroutineInfo),
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
		log.Info("Done for " + m.String() + "... \n\n")
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

		log.Info("Building Happens-Before graph... ")
		runner.Analysis.HBgraph = graph.New(graph.Directed)
		runner.Analysis.buildHB(runner.Analysis.HBgraph)
		log.Info("Done  -- Happens-Before graph built ")

		log.Info("Checking for data races... ")
		runner.Analysis.checkRacyPairs()
	}
	return nil
}

//bz: do each main one by one -> performance base line
func (runner *AnalysisRunner) runEachMainBaseline(main *ssa.Package) *pointer.ResultWCtx {
	logfile, err := os.Create("go_pta_log") //bz: for me ...
	if err != nil {
		log.Fatal(err)
	}
	if !doPTALog {
		logfile = nil
	}

	var scope []string
	if fromPath != "" {
		scope = []string{fromPath}
	}
	var mains []*ssa.Package
	mains = append(mains, main)
	// Configure pointer analysis to build call-graph
	runner.ptaconfig = &pointer.Config{
		Mains:          mains, //bz: NOW assume only one main
		Reflection:     false,
		BuildCallGraph: true,
		Log:            logfile,
		//kcfa
		//CallSiteSensitive: true,
		Origin: true, //origin
		//shared config
		K:          1,
		LimitScope: true,         //bz: only consider app methods now
		DEBUG:      doDebugPTA,   //bz: do all printed out info in console --> turn off to avoid internal nil reference panic
		Scope:      scope,        //bz: analyze scope, default is "command-line-arguments"
		Exclusions: excludedPkgs, //excludedPkgs here
	}

	start := time.Now()
	result, err2 := pointer.AnalyzeWCtx(runner.ptaconfig) // conduct pointer analysis
	if err2 != nil {
		log.Fatal(err2)
	}
	t := time.Now()
	elapsed := t.Sub(start)
	log.Info("Done -- PTA/CG Build; Using " + elapsed.String() + ". Go check go_pta_log for detail. ")
	if runner.ptaconfig.DEBUG {
		result.DumpAll()
	}
	return result
}
