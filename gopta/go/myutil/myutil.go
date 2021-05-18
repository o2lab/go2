package myutil

import (
	"bufio"
	"flag"
	"fmt"
	"github.com/april1989/origin-go-tools/go/callgraph"
	"github.com/april1989/origin-go-tools/go/myutil/compare"
	"github.com/april1989/origin-go-tools/go/myutil/flags"
	"github.com/april1989/origin-go-tools/go/packages"
	"github.com/april1989/origin-go-tools/go/pointer"
	default_algo "github.com/april1989/origin-go-tools/go/pointer_default"
	"github.com/april1989/origin-go-tools/go/ssa"
	"github.com/april1989/origin-go-tools/go/ssa/ssautil"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

//bz: utility functions and var declared for my use

var scope []string           //bz: now extract from pkgs, or add manually for debug
var excludedPkgs = []string { //bz: excluded a lot of default constraints -> only works if a.config.Level == 1 or turn on DoCallback (check a.createForLevelX() for details)
	//"runtime",
	//"reflect", -> only consider when turn on a.config.Reflection or analyzing tests
	//"os",

	//bz: check /_founds/sum.md for the following exclusions -> create too many interface related type of pointers with pts > 100
	"fmt",
	"errors", //there are so many wrappers of errors ...
}

var MyMaxTime time.Duration
var MyMinTime time.Duration
var MyElapsed int64

var DefaultMaxTime time.Duration
var DefaultMinTime time.Duration
var DefaultElapsed int64

//do preparation job
func InitialMain() []*ssa.Package {
	flags.ParseFlags()
	if flags.DoCallback {
		doCallback("")
	}

	args := flag.Args()
	cfg := &packages.Config{
		Mode:  packages.LoadAllSyntax, // the level of information returned for each package
		Dir:   "",                     // directory in which to run the build system's query tool
		Tests: flags.DoTests,          // setting Tests will include related test packages
	}
	return initial(args, cfg)
}

//bz: for my tests only
func InitialTest() { //default scope of tests
	flags.ParseFlags()
	if flags.DoCallback {
		doCallback("")
	}
	scope = append(scope, "main")
}

//bz: for race checker of callback branch use only, or if want to use callback
func InitialChecker(filepath string, config *pointer.Config) {
	if config.DoCallback {
		doCallback(filepath)
	}
}

func doCallback(filepath string) {
	flags.DoLevel = 1 //only consider app func
	if filepath == "" {
		os := runtime.GOOS
		var path string
		switch os {
		case "darwin":
			fmt.Println("MAC operating system")
			path = "/Users/bozhen/Documents/Go2/origin-go-tools/go/pointer/callback.yml" //bz: my mac mini only
		case "linux":
			fmt.Println("Linux")
			path = "/home/ubuntu/go/origin-go-tools/go/pointer/callback.yml" //bz: aws lightsail
		default:
			panic("Not defined path for OS: " + os)
		}
		pointer.DecodeYaml(path)
	}else{
		pointer.DecodeYaml(filepath + "/callback.yml")
	}
}

//do preparation job: common job
func initial(args []string, cfg *packages.Config) []*ssa.Package {
	fmt.Println("Loading input packages...")
	initial, err := packages.Load(cfg, args...)
	if err != nil {
		panic(fmt.Sprintln(err))
	}
	if len(initial) == 0 {
		fmt.Println("Package list empty")
		return nil
	} else if initial[0] == nil {
		fmt.Println("Nil package in list")
		return nil
	}
	////bz: even though there are errors in initial pkgs, pointer analysis can still run on them
	//// -> tmp comment off the following code
	//else if packages.PrintErrors(initial) > 0 {
	//	errSize, errPkgs := packages.PrintErrorsAndMore(initial) //bz: errPkg will be nil in initial
	//	if errSize > 0 {
	//		fmt.Println("Excluded the following packages contain errors, due to the above errors. ")
	//		for i, errPkg := range errPkgs {
	//			fmt.Println(i, " ", errPkg.ID)
	//		}
	//		fmt.Println("Continue   -- ")
	//	}
	//	if len(initial) == 0 || initial[0] == nil {
	//		fmt.Println("All Error Pkgs, Cannot Analyze. Return. ")
	//		return nil
	//	}
	//}
	fmt.Println("Done  -- " + strconv.Itoa(len(initial)) + " packages loaded")

	// Create and build SSA-form program representation.
	prog, pkgs := ssautil.AllPackages(initial, 0)

	fmt.Println("Building SSA code for entire program...")
	prog.Build()
	fmt.Println("Done  -- SSA code built")

	//extract scope from pkgs
	if !flags.DoTests && len(pkgs) > 1 { //TODO: bz: this only works when running under proj root dir
		//bz: compute the scope info == the root pkg: should follow the pattern xxx.xxx.xx/xxx
		path, err := os.Getwd() //current working directory == project path
		if err != nil {
			panic("Error while os.Getwd: " + err.Error())
		}

		gomodFile, err := os.Open(path + "/go.mod") // For read access.
		if err != nil {
			panic("Error while reading go.mod: " + err.Error())
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
			fmt.Println("Cannot find go.mod in default location: ", gomodFile)
			return nil
		}

		if err := scanner.Err(); err != nil {
			panic("Error while scanning go.mod: " + err.Error())
		}

		parts := strings.Split(mod, " ")
		scope = append(scope, parts[1])
	}else {  //else: default input .go file with default scope
		//scope = append(scope, "command-line-arguments")
		//bz: the following are for debug purpose
		scope = append(scope, "google.golang.org/grpc")
		//scope = append(scope, "github.com/pingcap/tidb")
		//scope = append(scope, "k8s.io/kubernetes")
		//scope = append(scope, "github.com/ethereum/go-ethereum")
	}

	mains, tests, err := findMainPackages(pkgs)
	if err != nil {
		fmt.Println(err)
		return nil
	}

	if flags.DoTests {
		fmt.Println("#TOTAL MAIN: " + strconv.Itoa(len(mains) - len(tests)) + "\n")
		fmt.Println("#TOTAL TESTS: " + strconv.Itoa(len(tests)) + "\n")
	}else{
		fmt.Println("#TOTAL MAIN: " + strconv.Itoa(len(mains)) + "\n")
	}
	if flags.Main != "" {
		fmt.Println("Capture -- ", flags.Main, "\n")
	}

	//initial set
	MyMaxTime = 0
	DefaultMaxTime = 0
	MyMinTime = 10000000000000
	DefaultMinTime = 10000000000000

	return mains
}

// mainPackages returns the main/test packages to analyze.
// Each resulting package is named "main" and has a main function.
func findMainPackages(pkgs []*ssa.Package) ([]*ssa.Package, []*ssa.Package, error) {
	var mains []*ssa.Package
	var tests []*ssa.Package
	for _, p := range pkgs {
		if p != nil {
			if p.Pkg.Name() == "main" && p.Func("main") != nil {
				//bz: we may see a main from out-of-scope pkg, e.g.,
				//  k8s.io/apiextensions-apiserver/test/integration/conversion.test when analyzing k8s.io/kubernetes, which is from /kubernetes/vendor/*
				// now wee need to check the pkg with scope[0], for most cases, we only have one scope
				if len(scope) > 0 && strings.HasPrefix(p.Pkg.Path(), scope[0]) {
					mains = append(mains, p)
					if flags.DoTests && strings.HasSuffix(p.Pkg.String(), ".test") { //this is a test-assembled main, just identify, no other use
						tests = append(tests, p)
					}
				}
			}
		}
	}
	if len(mains) == 0 { //bz: main as the first priority
		return nil, nil, fmt.Errorf("no main packages")
	}
	return mains, tests, nil
}

//baseline: all main together
func DoSameRoot(mains []*ssa.Package) {
	if flags.DoCompare || flags.DoDefault {
		doSameRootDefault(mains)
		fmt.Println("........................................\n........................................")
	}
	if flags.DoDefault {
		return
	}

	doSameRootMy(mains)
}

//bz: test usesage in race checker -> this is the major usage now
func DoSeq(mains []*ssa.Package) {
	level := 0
	if flags.DoLevel != 0 {
		level = flags.DoLevel //bz: reset the analysis scope
	}

	//var logfile *os.File
	//if flags.DoLog { //bz: debug purpose  && len(mains) == 1
	//	logfile, _ = os.Create("/Users/bozhen/Documents/GO2/origin-go-tools/_logs/my_log_0")
	//} else {
	//	logfile = nil
	//}

	ptaConfig := &pointer.Config{
		Mains:          mains,
		Reflection:     false,
		BuildCallGraph: true,
		Log:            nil,//logfile,
		//CallSiteSensitive: true, //kcfa
		Origin: true, //origin
		//shared config
		K:             1,
		LimitScope:    true,                //bz: only consider app methods now -> no import will be considered
		DEBUG:         false,               //bz: rm all printed out info in console
		Scope:         scope,               //bz: analyze scope + input path
		Exclusion:     excludedPkgs,        //bz: copied from race_checker if any
		TrackMore:     true,                //bz: track pointers with all types
		Level:         level,               //bz: see pointer.Config
		DoCallback:    flags.DoCallback,    //bz: sythesize callback
	}

	start := time.Now()                                    //performance
	results, r_err := pointer.AnalyzeMultiMains(ptaConfig) // conduct pointer analysis
	t := time.Now()
	elapsed := t.Sub(start)
	if r_err != nil {
		panic(fmt.Sprintln(r_err))
	}
	fmt.Println("\nDone  -- PTA/CG Build; Using " + elapsed.String() + ".\n ")

	//check queries
	fmt.Println("#Receive Result: ", len(results))
	for main, result := range results {
		fmt.Println("Receive result (#Queries: ", len(result.Queries), ", #IndirectQueries: ", len(result.IndirectQueries),
			", #GlobalQueries: ", len(result.GlobalQueries), ") for main: ", main.String())

		////bz: debug
		//result.Statistics()
		//result.DumpCG()
		//result.CountMyReachUnreachFunctions(true)
	}

	//total statistics
	if len(results) > 1 {
		pointer.TotalStatistics(results)
	}

	//check for test
	if flags.DoTests {
		for main, r := range results {
			mp := r.GetTests()
			if mp == nil {
				continue
			}

			fmt.Println("\n\nTest Functions of: ", main)
			for fn, cgn := range mp {
				fmt.Println(fn, "\t-> ", cgn.String())
			}
		}
	}
}

func doSameRootMy(mains []*ssa.Package) *pointer.Result {
	// Configure pointer analysis to build call-graph
	ptaConfig := &pointer.Config{
		Mains:          mains,
		Reflection:     false,
		BuildCallGraph: true,
		Log:            nil,
		//CallSiteSensitive: true, //kcfa
		Origin: true, //origin
		//shared config
		K:             1,
		LimitScope:    true,                              //bz: only consider app methods now -> no import will be considered
		DEBUG:         false,                             //bz: rm all printed out info in console
		Scope:         scope,                             //bz: analyze scope + input path
		Exclusion:     excludedPkgs,                      //bz: copied from race_checker if any
		TrackMore:     true,                              //bz: track pointers with all types
		Level:         0,                                 //bz: see pointer.Config
		DoCallback:    flags.DoCallback, //bz: sythesize callback
	}

	//*** compute pta here
	start := time.Now() //performance
	var result *pointer.Result
	var r_err error
	if flags.TimeLimit != 0 { //we set time limit
		c := make(chan string, 1)

		// Run the pta in it's own goroutine and pass back it's
		// response into our channel.
		go func() {
			result, r_err = pointer.Analyze(ptaConfig) // conduct pointer analysis
			c <- "done"
		}()

		// Listen on our channel AND a timeout channel - which ever happens first.
		select {
		case res := <-c:
			fmt.Println(res)
		case <-time.After(flags.TimeLimit):
			fmt.Println("\n!! Out of time (", flags.TimeLimit, "). Kill it :(")
			return nil
		}
	} else {
		result, r_err = pointer.Analyze(ptaConfig) // conduct pointer analysis
	}
	t := time.Now()
	elapsed := t.Sub(start)
	if r_err != nil {
		panic(fmt.Sprintln(r_err))
	}

	fmt.Println("\nDone  -- PTA/CG Build; Using " + elapsed.String() + ".\n ")

	if MyMaxTime < elapsed {
		MyMaxTime = elapsed
	}
	if MyMinTime > elapsed {
		MyMinTime = elapsed
	}

	return result
}

func doSameRootDefault(mains []*ssa.Package) []*default_algo.Result {
	// Configure pointer analysis to build call-graph
	ptaConfig := &default_algo.Config{
		Mains:           mains, //one main per time
		Reflection:      false,
		BuildCallGraph:  true,
		Log:             nil,
		DoPerformance:   flags.DoPerformance, //bz: I add to output performance for comparison
		DoRecordQueries: flags.DoCompare,     //bz: record all queries to compare result
	}

	//*** compute pta here
	start := time.Now() //performance
	var result *default_algo.Result
	var r_err error           //bz: result error
	if flags.TimeLimit != 0 { //we set time limit
		c := make(chan string, 1)

		// Run the pta in it's own goroutine and pass back it's
		// response into our channel.
		go func() {
			result, r_err = default_algo.Analyze(ptaConfig) // conduct pointer analysis
			c <- "done"
		}()

		// Listen on our channel AND a timeout channel - which ever happens first.
		select {
		case res := <-c:
			fmt.Println(res)
		case <-time.After(flags.TimeLimit):
			fmt.Println("\n!! Out of time (", flags.TimeLimit, "). Kill it :(")
			return nil
		}
	} else {
		result, r_err = default_algo.Analyze(ptaConfig) // conduct pointer analysis
	}
	t := time.Now()
	elapsed := t.Sub(start)
	if r_err != nil {
		panic(fmt.Sprintln(r_err))
	}

	fmt.Println("\nDone  -- PTA/CG Build; Using ", elapsed.String(), ".\n")

	if DefaultMaxTime < elapsed {
		DefaultMaxTime = elapsed
	}
	if DefaultMinTime > elapsed {
		DefaultMinTime = elapsed
	}

	if len(result.Warnings) > 0 {
		fmt.Println("Warning: ", len(result.Warnings)) //bz: just do not report not used var on result
	}

	return nil
}

//baseline: foreach
func DoEach(mains []*ssa.Package) {
	for i, main := range mains {
		if flags.Main != "" && flags.Main != main.Pkg.Path() { //run for IDX only
			continue
		}

		fmt.Println(i, ". ", main.String())
		var r_default *default_algo.Result
		var r_my *pointer.ResultWCtx

		start := time.Now() //performance
		if flags.DoCompare || flags.DoDefault {
			//default
			fmt.Println("Default Algo: ")
			r_default = doEachMainDefault(i, main) //default pta
			t := time.Now()
			DefaultElapsed = DefaultElapsed + t.Sub(start).Milliseconds()
			start = time.Now()
			fmt.Println("........................................\n........................................")
		}

		if flags.DoDefault {
			continue //skip running mine
		}

		//my
		fmt.Println("My Algo: ")
		r_my = DoEachMainMy(i, main) //mypta
		t := time.Now()
		MyElapsed = MyElapsed + t.Sub(start).Milliseconds()

		if flags.DoCompare {
			if r_default != nil && r_my != nil {
				start = time.Now()
				compare.Compare(r_default, r_my)
				t := time.Now()
				comp_elapsed := t.Sub(start)
				fmt.Println("Compare Total Time: ", comp_elapsed.String()+".")
			} else {
				fmt.Println("\n\n!! Cannot compare results due to OOT.")
			}
		}
		fmt.Println("=============================================================================")
	}
}

func DoEachMainMy(i int, main *ssa.Package) *pointer.ResultWCtx {
	var logfile *os.File
	var err error
	if flags.DoLog { //create my log file
		logfile, err = os.Create("/Users/bozhen/Documents/GO2/origin-go-tools/_logs/my_log_" + strconv.Itoa(i))
	} else {
		logfile = nil
	}

	if err != nil {
		panic(fmt.Sprintln(err))
	}

	if strings.EqualFold(main.String(), "package command-line-arguments") { //default .go input
		scope = append(scope, "command-line-arguments")
	}

	var mains []*ssa.Package
	mains = append(mains, main)
	// Configure pointer analysis to build call-graph
	ptaConfig := &pointer.Config{
		Mains:          mains, //bz: NOW assume only one main
		Reflection:     false,
		BuildCallGraph: true,
		Log:            logfile,
		//CallSiteSensitive: true, //kcfa
		Origin: true, //origin
		//shared config
		K:             1,                   //bz: how many level of origins? default = 1
		LimitScope:    true,                //bz: only consider app methods now -> no import will be considered
		DEBUG:         false,               //bz: rm all printed out info in console
		Scope:         scope,               //bz: analyze scope + input path
		Exclusion:     excludedPkgs,        //bz: copied from race_checker if any
		TrackMore:     true,                //bz: track pointers with types declared in Analyze Scope
		Level:         flags.DoLevel,       //bz: see pointer.Config
		DoCallback:    flags.DoCallback,    //bz: sythesize callback
	}

	//*** compute pta here
	start := time.Now() //performance
	var result *pointer.Result
	var rErr error
	if flags.TimeLimit != 0 { //we set time limit
		c := make(chan string, 1)

		// Run the pta in it's own goroutine and pass back it's
		// response into our channel.
		go func() {
			result, rErr = pointer.Analyze(ptaConfig) // conduct pointer analysis
			c <- "done"
		}()

		// Listen on our channel AND a timeout channel - which ever happens first.
		select {
		case res := <-c:
			fmt.Println(res)
		case <-time.After(flags.TimeLimit):
			fmt.Println("\n!! Out of time (", flags.TimeLimit, "). Kill it :(")
			return nil
		}
	} else {
		result, rErr = pointer.Analyze(ptaConfig) // conduct pointer analysis
	}
	t := time.Now()
	elapsed := t.Sub(start)
	if rErr != nil {
		panic(fmt.Sprintln(rErr))
	}
	defer logfile.Close()

	if flags.DoPerformance {
		_, _, _, preNodes, preFuncs := result.GetResult().CountMyReachUnreachFunctions(flags.DoDetail)
		fmt.Println("#Unreach Nodes: ", len(preNodes))
		fmt.Println("#Reach Nodes: ", len(result.GetResult().CallGraph.Nodes)-len(preNodes))
		fmt.Println("#Unreach Functions: ", len(preNodes))
		fmt.Println("#Reach Functions: ", len(result.GetResult().CallGraph.Nodes)-len(preFuncs))
		fmt.Println("\n#Unreach Nodes from Pre-Gen Nodes: ", len(preNodes))
		fmt.Println("#Unreach Functions from Pre-Gen Nodes: ", len(preFuncs))
		fmt.Println("#(Pre-Gen are created for reflections)")
	}

	fmt.Println("\nDone  -- PTA/CG Build; Using " + elapsed.String() + ".\n ")

	if MyMaxTime < elapsed {
		MyMaxTime = elapsed
	}
	if MyMinTime > elapsed {
		MyMinTime = elapsed
	}

	if ptaConfig.DEBUG {
		result.DumpAll()
	}

	if flags.DoCompare || flags.DoParallel {
		_r := result.GetResult()
		_r.Queries = result.Queries
		_r.IndirectQueries = result.IndirectQueries
		return _r //bz: we only need this when comparing results/run in parallel
	}

	return nil
}

func doEachMainDefault(i int, main *ssa.Package) *default_algo.Result {
	var logfile *os.File
	var err error
	if flags.DoLog { //create my log file
		logfile, err = os.Create("/Users/bozhen/Documents/GO2/origin-go-tools/_logs/default_log_" + strconv.Itoa(i))
	} else {
		logfile = nil
	}

	if err != nil {
		panic(fmt.Sprintln(err))
	}

	var mains []*ssa.Package
	mains = append(mains, main)
	// Configure pointer analysis to build call-graph
	ptaConfig := &default_algo.Config{
		Mains:           mains, //one main per time
		Reflection:      false,
		BuildCallGraph:  true,
		Log:             logfile,
		DoPerformance:   flags.DoPerformance, //bz: I add to output performance for comparison
		DoRecordQueries: flags.DoCompare,     //bz: record all queries to compare result
	}

	//*** compute pta here
	start := time.Now() //performance
	var result *default_algo.Result
	var r_err error           //bz: result error
	if flags.TimeLimit != 0 { //we set time limit
		c := make(chan string, 1)

		// Run the pta in it's own goroutine and pass back it's
		// response into our channel.
		go func() {
			result, r_err = default_algo.Analyze(ptaConfig) // conduct pointer analysis
			c <- "done"
		}()

		// Listen on our channel AND a timeout channel - which ever happens first.
		select {
		case res := <-c:
			fmt.Println(res)
		case <-time.After(flags.TimeLimit):
			fmt.Println("\n!! Out of time (", flags.TimeLimit, "). Kill it :(")
			return nil
		}
	} else {
		result, r_err = default_algo.Analyze(ptaConfig) // conduct pointer analysis
	}
	t := time.Now()
	elapsed := t.Sub(start)
	if r_err != nil {
		panic(fmt.Sprintln(r_err))
	}
	defer logfile.Close()

	if flags.DoPerformance {
		reaches, unreaches := countReachUnreachFunctions(result)
		fmt.Println("#Unreach Nodes: ", len(unreaches))
		fmt.Println("#Reach Nodes: ", len(reaches))
		fmt.Println("#Unreach Functions: ", len(unreaches))
		fmt.Println("#Reach Functions: ", len(reaches))
	}

	fmt.Println("\nDone  -- PTA/CG Build; Using ", elapsed.String(), ".\n")

	if DefaultMaxTime < elapsed {
		DefaultMaxTime = elapsed
	}
	if DefaultMinTime > elapsed {
		DefaultMinTime = elapsed
	}

	if len(result.Warnings) > 0 {
		fmt.Println("Warning: ", len(result.Warnings)) //bz: just do not report not used var on result
	}

	return result
}

//baseline: all main in parallel
//Update: fatal error: concurrent map writes!! --> change to a.track = trackall, no more panic
//go add lock @container/intsets/util.go for nodeset when doing this setting
func DoParallel(mains []*ssa.Package) map[*ssa.Package]*pointer.ResultWCtx {
	ret := make(map[*ssa.Package]*pointer.ResultWCtx) //record of result
	var _wg sync.WaitGroup
	start := time.Now()
	for i, main := range mains[0:3] {
		fmt.Println("Spawn ", i, ". ", main.String())
		_wg.Add(1)
		go func(i int, main *ssa.Package) {
			start := time.Now()
			fmt.Println("My Algo: ")
			r_my := DoEachMainMy(i, main) //mypta
			t := time.Now()
			MyElapsed = MyElapsed + t.Sub(start).Milliseconds()

			//update
			ret[main] = r_my
			_wg.Done()

			fmt.Println("Join ", i, ". ", main.String())
		}(i, main)
	}
	_wg.Wait()

	elapsed := time.Now().Sub(start)
	fmt.Println("\nDone  -- PTA/CG Build; Using " + elapsed.String() + ".\n ")

	//check
	fmt.Println("#Receive Result: ", len(ret))
	for main, result := range ret {
		fmt.Println("Receive result (#Queries: ", len(result.Queries), ", #IndirectQueries: ", len(result.IndirectQueries), ") for main: ", main.String())
	}

	return ret
}

//bz: for default algo
//    exclude root, init and main has root as caller
func countReachUnreachFunctions(result *default_algo.Result) (map[*ssa.Function]*ssa.Function, map[*ssa.Function]*ssa.Function) {
	r := result
	//fn
	reaches := make(map[*ssa.Function]*ssa.Function)
	unreaches := make(map[*ssa.Function]*ssa.Function)

	var checks []*callgraph.Edge
	//start from main and init
	root := r.CallGraph.Root
	reaches[root.Func] = root.Func
	for _, out := range root.Out {
		checks = append(checks, out)
	}

	for len(checks) > 0 {
		var tmp []*callgraph.Edge
		for _, check := range checks {
			if _, ok := reaches[check.Callee.Func]; ok {
				continue //checked already
			}

			reaches[check.Callee.Func] = check.Callee.Func
			for _, out := range check.Callee.Out {
				tmp = append(tmp, out)
			}
		}
		checks = tmp
	}

	//collect unreaches
	for _, node := range r.CallGraph.Nodes {
		if _, ok := reaches[node.Func]; !ok { //unreached
			if _, ok2 := unreaches[node.Func]; ok2 {
				continue //already stored
			}
			unreaches[node.Func] = node.Func
		}
	}

	//preGen and preFuncs are supposed to be the same with mine
	return reaches, unreaches
}