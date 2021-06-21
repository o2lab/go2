package main

import (
	"fmt"
	"github.com/april1989/origin-go-tools/go/myutil/flags"
	"github.com/april1989/origin-go-tools/go/packages"
	"github.com/april1989/origin-go-tools/go/pointer"
	pta0 "github.com/april1989/origin-go-tools/go/pointer_default"
	"github.com/april1989/origin-go-tools/go/ssa"
	log "github.com/sirupsen/logrus"
	"github.com/twmb/algoimpl/go/graph"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)


type AnalysisRunner struct {
	mu            sync.Mutex
	prog          *ssa.Program
	pkgs          []*ssa.Package
	ptaConfig     *pointer.Config
	ptaResult     *pointer.Result //bz: added for convenience
	ptaResults    map[*ssa.Package]*pointer.Result //bz: original code, renamed here
	ptaConfig0    *pta0.Config
	ptaResult0    *pta0.Result
	trieLimit     int  // set as user config option later, an integer that dictates how many times a function can be called under identical context
	efficiency    bool // configuration setting to avoid recursion in tested program
	racyStackTops []string
	finalReport   []raceReport
}


//bz: for analyzing tests, the string return is not necessary
func (runner *AnalysisRunner) analyzeTestEntry(main *ssa.Package) []*ssa.Function {
	if strings.HasSuffix(main.Pkg.Path(), ".test") || main.IsMainTest { //bz: strict end with .test or the pkg that i set
		log.Info("Extracting test functions from PTA/CG...")
		testFns := runner.ptaResult.GetTests()// all test functions in this entry
		if testFns == nil {
			return nil  //this is a main entry
		}
		fmt.Println("The following are functions found within: ", main.String())
		counter := 1
		for _, fn := range testFns {
			fmt.Println("Option", counter, ": ", fn.Name())
			counter++
		}
		//if allEntries { // analyze all test functions -> bz: if allEntries, will not hit this code
		//	return testFns, ""
		//}

		fmt.Print("Enter option number of choice or test name: \n")
		selectedFns, err := getUserSelectionFn(testFns)
		for selectedFns == nil {
			fmt.Println(err)
			fmt.Print("Enter option number of choice or test name: \n")
			selectedFns, err = getUserSelectionFn(testFns)
		}

		return selectedFns
		//log.Info("Done  -- CG node of test function ", entry, " extracted.")
	}
	return nil
}



//bz: update the run order of pta and checker
//  -> sequentially now; do not provide selection of test entry
func (runner *AnalysisRunner) Run2() error {
	trieLimit = runner.trieLimit
	efficiency = runner.efficiency
	// Load packages...
	cfg := &packages.Config{
		Mode: packages.LoadAllSyntax, // the level of information returned for each package
		Dir:  userDir,                     // directory in which to run the build system's query tool
		//TODO: bz: change this to true if you want to analyze test
		Tests: true,
		//Tests: false,                  // setting Tests will include related test packages
	}


	doStartLog("Loading input packages... ")
	initial, multiSamePkgs, err := packages.Load(cfg, userInputFile ... ) //bz: see golist.go
	if len(initial) == 0 {
		if DEBUG {
			fmt.Println("packages.Load error: " + err.Error())
		}

		doEndLog("No Go package detected. Return. ")
		return nil
	}
	doEndLog("Done  -- " + strconv.Itoa(len(initial)) + " packages detected.")

	mains, prog, pkgs := pkgSelection(initial, multiSamePkgs)
	if mains == nil {
		return nil
	}

	runner.prog = prog //TODO: bz: optimize, no need to do like this.
	startExec := time.Now() // measure total duration of running entire code base

	//run one by one: Iterate each entry point...
	//var wg sync.WaitGroup
	for _, main := range mains {
		log.Info("****************************************************************************************************")
		if main.IsMainTest {
			log.Info("Start for entry point: ", main.String() + ".main.test", "... ")
		} else {
			log.Info("Start for entry point: ", main.String(), "... ")
		}
		// Configure pointer analysis...
		doStartLog("Running Pointer Analysis... ")
		var scope []string
		if useNewPTA {
			scope = determineScope(main, pkgs)

			var mains []*ssa.Package
			mains = append(mains, main) //TODO: bz: optimize

			//logfile, _ := os.Create("/Users/bozhen/Documents/GO2/go2/gorace/pta_log_0") //bz: debug
			if allEntries || strings.HasSuffix(main.Pkg.Path(), ".test") || main.IsMainTest { //bz: set to default behavior
				flags.DoTests = true //bz: set to true if your folder has tests and you want to analyze them
			}
			runner.ptaConfig = &pointer.Config {
				Mains:          mains, //bz: all mains/tests in a project
				Reflection:     false,
				BuildCallGraph: true,
				Log:            nil, //logfile,
				Origin:         true, //origin
				//shared config
				K:          1,
				LimitScope: true,         //bz: only consider app methods with origin
				Scope:      scope,        //bz: analyze scope
				Exclusion:  excludedPkgs, //bz: copied from gorace if any
				TrackMore:  true,         //bz: track pointers with all types
				Level:      0,            //bz: see pointer.Config
			}
			start := time.Now() //performance
			var err error
			runner.ptaResult, err = pointer.Analyze(runner.ptaConfig) // conduct pointer analysis for multiple mains
			if err != nil {
				panic("Pointer Analysis Error: " + err.Error())
			}

			t := time.Now()
			elapsed := t.Sub(start)
			doEndLog("Done  -- Pointer Analysis Finished. Using " + elapsed.String() + ". ")
			//////!!!! bz: for my debug, please comment off, do not delete
			//if strings.Contains(main.String(), "github.com/ethereum/go-ethereum/cmd/ethkey") && main.IsMainTest {
			//	fmt.Println("Receive Result: ")
			//	fmt.Println("Receive ptaRes (#Queries: ", len(runner.ptaResult.Queries), ", #IndirectQueries: ", len(runner.ptaResult.IndirectQueries), ") for main: ", main.String())
			//	runner.ptaResult.DumpCG()
			//}

		} else {
			//TODO: bz: i did not touch here and we are not using default now
			runner.ptaConfig0 = &pta0.Config{
				Mains:          mains,
				BuildCallGraph: false,
			}
			runner.ptaResult0, _ = pta0.Analyze(runner.ptaConfig0)
		}

		// Configure static analysis...
		var selectTests []*ssa.Function //bz: can be nil if is main
		if allEntries {
			//bz: do for all
			selectTests = runner.ptaResult.GetTests()
		} else {
			//bz: select tests by users, can be nil if is main
			selectTests = runner.analyzeTestEntry(main)
		}

		if selectTests == nil { //bz: is a main TODO: or main.IsMainTest == true but we cannot find/link the test func now
			doStartLog("Traversing Statements... ")

			a := initialAnalysis()
			a.main = main
			a.ptaRes = runner.ptaResult
			a.ptaRes0 = runner.ptaResult0
			a.ptaCfg = runner.ptaConfig
			a.ptaCfg0 = runner.ptaConfig0
			a.prog = runner.prog
			a.entryFn = "main"
			a.scope = scope

			rr := a.runChecker(multiSamePkgs)
			runner.racyStackTops = a.racyStackTops
			runner.finalReport = append(runner.finalReport, rr)
		} else { //bz: is a test
			for _, test := range selectTests {
				log.Info("Traversing Statements for test entry point: ", test.String(), "... ")

				a := initialAnalysis()
				a.main = main
				a.ptaRes = runner.ptaResult
				a.ptaRes0 = runner.ptaResult0
				a.ptaCfg = runner.ptaConfig
				a.ptaCfg0 = runner.ptaConfig0
				a.prog = runner.prog
				a.testEntry = test
				a.entryFn = test.Name()
				a.otherTests = selectTests
				a.scope = scope

				rr := a.runChecker(false)
				runner.racyStackTops = a.racyStackTops
				runner.finalReport = append(runner.finalReport, rr)

				log.Info("Finish for test entry point: ", test.String(), ".")
				log.Info("----------------------------------------------------------------------------------------------------")
			}
		}
		log.Info("Finish for entry point: ", main.String(), ".")
	}
	log.Info("****************************************************************************************************\n\n") //bz: final finish line

	if goTest {//bz: skip the following printout for go test
		return nil
	}

	//summary report
	log.Info("Summary Report:")
	raceCount := 0
	for _, e := range runner.finalReport {
		s := len(e.racePairs)
		if s > 0 && e.racePairs[0] != nil {
			if s == 1 {
				log.Info(s, " race found for entry point ", e.entryInfo, ".")
			} else {
				log.Info(s, " races found for entry point ", e.entryInfo, ".")
			}
			raceCount += s
		} else {
			log.Info("No races found for ", e.entryInfo, ".")
		}
	}
	if raceCount == 1 {
		log.Info("Total of ", raceCount, " race found for all entry points. ")
	} else {
		log.Info("Total of ", raceCount, " races found for all entry points. ")
	}
	execDur := time.Since(startExec)
	log.Info("Total Time:", execDur, ". ")

	return nil
}

func (runner *AnalysisRunner) Run(args []string) error {
	trieLimit = runner.trieLimit
	efficiency = runner.efficiency
	// Load packages...
	cfg := &packages.Config{
		Mode: packages.LoadAllSyntax, // the level of information returned for each package
		Dir:  "",                     // directory in which to run the build system's query tool
		//TODO: bz: change this to true if you want to analyze test
		Tests: true,
		//Tests: false,                  // setting Tests will include related test packages
	}
	log.Info("Loading input packages...")

	os.Stderr = nil // No need to output package errors for now. Delete this line to view package errors
	initial, multiSamePkgs, _ := packages.Load(cfg, args...)
	if len(initial) == 0 {
		return fmt.Errorf("No Go files detected. ")
	}
	log.Info("Done  -- ", len(initial), " packages detected. ")

	mains, prog, pkgs := pkgSelection(initial, multiSamePkgs)
	runner.prog = prog
	runner.pkgs = pkgs
	startExec := time.Now() // measure total duration of running entire code base
	// Configure pointer analysis...
	if useNewPTA {
		scope := determineScope(nil, pkgs)

		//logfile, _ := os.Create("/Users/bozhen/Documents/GO2/pta_replaced/go2/gorace/pta_log_0") //bz: debug
		flags.DoTests = true //bz: set to true if your folder has tests and you want to analyze them
		flags.PTSLimit = 10  //bz: limit the size of pts to 10
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
			Exclusion:  excludedPkgs, //bz: copied from gorace if any
			TrackMore:  true,         //bz: track pointers with all types
			Level:      0,            //bz: see pointer.Config
		}
		start := time.Now() //performance
		var err error
		runner.ptaResults, err = pointer.AnalyzeMultiMains(runner.ptaConfig) // conduct pointer analysis for multiple mains
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
			Mains:          mains,
			BuildCallGraph: false,
		}
		runner.ptaResult0, _ = pta0.Analyze(runner.ptaConfig0)
	}

	if len(mains) > 1 {
		allEntries = true
	}
	selectFn := runner.analyzeTestEntry(mains[0]) //bz: unexpected behavior ...

	// Iterate each entry point...
	//var wg sync.WaitGroup
	for _, m := range mains {
		//wg.Add(1)
		//go func(main *ssa.Package) {
		// Configure static analysis...
		a := analysis{
			ptaRes:          runner.ptaResults[m],
			ptaRes0:         runner.ptaResult0,
			ptaCfg:          runner.ptaConfig,
			ptaCfg0:         runner.ptaConfig0,
			efficiency:      efficiency,
			trieLimit:       trieLimit,
			prog:            runner.prog,
			main:            m,
			RWinsMap:        make(map[goIns]graph.Node),
			trieMap:         make(map[fnInfo]*trie),
			insMono:         -1,
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
			commIDs:         make(map[int][]int),
			deferToRet:      make(map[*ssa.Defer]ssa.Instruction),
			entryFn:         selectFn[0].Name(),
			twinGoID:        make(map[*ssa.Go][]int),
			//mutualTargets:   make(map[int]*mutualFns),
		}
		if strings.Contains(m.Pkg.Path(), "GoBench") { // for testing purposes
			a.efficiency = false
			a.trieLimit = 2
		} else if !goTest {
			a.efficiency = true
		}
		if !allEntries {
			log.Info("Compiling stack trace for every Goroutine... ")
			log.Debug(strings.Repeat("-", 35), "Stack trace begins", strings.Repeat("-", 35))
		}
		if a.testEntry != nil {
			//bz: a test now uses itself as main context, tell pta which test will be analyzed for this analysis
			//for _, eachTest := range a.testEntry {//bz: this is wrong ...
			//	a.ptaRes.AnalyzeTest(eachTest)
			//	a.visitAllInstructions(eachTest, 0)
			//}
		} else {
			a.visitAllInstructions(m.Func(a.entryFn), 0)
		}

		if !allEntries {
			log.Debug(strings.Repeat("-", 35), "Stack trace ends", strings.Repeat("-", 35))
		}
		totalIns := 0
		for g := range a.RWIns {
			totalIns += len(a.RWIns[g])
		}
		if !allEntries {
			log.Info("Done  -- ", len(a.RWIns), " goroutines analyzed! ", totalIns, " instructions of interest detected! ")
		}

		if useDefaultPTA {
			a.ptaRes0, _ = pta0.Analyze(a.ptaCfg0) // all queries have been added, conduct pointer analysis
		}
		if !allEntries {
			log.Info("Building Static Happens-Before graph... ")
		}
		// confirm channel readiness for unknown select cases:
		if len(a.selUnknown) > 0 {
			for sel, chs := range a.selUnknown {
				for i, ch := range chs {
					if _, ready := a.chanSnds[ch]; !ready && ch != "" {
						if _, ready0 := a.chanRcvs[ch]; !ready0 {
							if _, ready1 := a.chanBuf[a.chanToken[ch]]; !ready1 {
								a.selReady[sel][i] = ""
							}
						}
					}
				}
			}
		}
		a.HBgraph = graph.New(graph.Directed)
		a.buildHB()
		log.Info("Done  -- Static Happens-Before graph built. ")
		log.Info("Checking for data races... ")

		rr := raceReport{
			entryInfo: m.Pkg.Path(),
		}
		rr.racePairs = a.checkRacyPairs()
		//runner.mu.Lock()
		runner.racyStackTops = a.racyStackTops
		runner.finalReport = append(runner.finalReport, rr)
		//runner.mu.Unlock()
		//wg.Done()
		//}(m)
	}
	//wg.Wait()

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

//bz: below are all util structures used in analysis

//type mutualFns struct {
//	fns     map[*ssa.Function]*mutualGroup //bz: fn <-> all its mutual fns (now including itself)
//}

type mutualGroup struct {
	group   map[*ssa.Function]*ssa.Function //bz: this is a group of mutual fns
}

type insInfo struct {
	ins 	ssa.Instruction
	stack 	[]*fnCallInfo
}

type fnCallInfo struct {
	fnIns  *ssa.Function
	ssaIns ssa.Instruction
}


type lockInfo struct {
	locAddr    ssa.Value
	locFreeze  bool
	locBloc		*ssa.BasicBlock
	parentFn   *ssa.Function
}

type raceInfo struct {
	insPair  []*insInfo
	addrPair [2]ssa.Value
	goIDs    []int
	insInd   []int
}

type raceReport struct {
	entryInfo string
	racePairs []*raceInfo
}

type fnInfo struct { // all fields must be comparable for fnInfo to be used as key to trieMap
	fnName     *ssa.Function
	contextStr string
}

type goCallInfo struct {
	ssaIns ssa.Instruction
	goIns  *ssa.Go
}

type goIns struct { // an ssa.Instruction with goroutine info
	ins  ssa.Instruction
	goID int
}

type goroutineInfo struct {
	ssaIns      ssa.Instruction
	goIns       *ssa.Go
	entryMethod *ssa.Function
	goID        int
}

type stat struct {
	nAccess    int
	nGoroutine int
}

type trie struct {
	fnName    string
	budget    int
	fnContext []*ssa.Function
}

// isBudgetExceeded determines if the budget has exceeded the limit
func (t trie) isBudgetExceeded() bool {
	if t.budget > trieLimit {
		return true
	}
	return false
}
