package main

import (
	"flag"
	"fmt"
	"github.tamu.edu/April1989/go_tools/flags"
	"github.tamu.edu/April1989/go_tools/go/callgraph"
	"github.tamu.edu/April1989/go_tools/go/compare"
	"github.tamu.edu/April1989/go_tools/go/packages"
	"github.tamu.edu/April1989/go_tools/go/pointer"
	default_algo "github.tamu.edu/April1989/go_tools/go/pointer_default"
	"github.tamu.edu/April1989/go_tools/go/ssa"
	"github.tamu.edu/April1989/go_tools/go/ssa/ssautil"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

var scope []string           //bz: now extract from pkgs
var excludedPkgs = []string{ //bz: excluded a lot of default constraints
	//"runtime",
	//"reflect",
	//"os",
}
var my_maxTime time.Duration
var my_minTime time.Duration
var my_elapsed int64

var default_maxTime time.Duration
var default_minTime time.Duration
var default_elapsed int64

func main() {
	flags.ParseFlags()

	args := flag.Args()
	cfg := &packages.Config{
		Mode:  packages.LoadAllSyntax, // the level of information returned for each package
		Dir:   "",                     // directory in which to run the build system's query tool
		Tests: false,                  // setting Tests will include related test packages
	}
	fmt.Println("Loading input packages...")
	initial, err := packages.Load(cfg, args...)
	if err != nil {
		panic(fmt.Sprintln(err))
	}
	if packages.PrintErrors(initial) > 0 {
		errSize, errPkgs := packages.PrintErrorsAndMore(initial) //bz: errPkg will be nil in initial
		if errSize > 0 {
			fmt.Println("Excluded the following packages contain errors, due to the above errors. ")
			for i, errPkg := range errPkgs {
				fmt.Println(i, " ", errPkg.ID)
			}
			fmt.Println("Continue   -- ")
		}
	} else if len(initial) == 0 {
		fmt.Println("Package list empty")
		return
	} else if initial[0] == nil {
		fmt.Println("Nil package in list")
		return
	}
	fmt.Println("Done  -- " + strconv.Itoa(len(initial)) + " packages loaded")

	// Create and build SSA-form program representation.
	prog, pkgs := ssautil.AllPackages(initial, 0)

	fmt.Println("Building SSA code for entire program...")
	prog.Build()
	fmt.Println("Done  -- SSA code built")

	mains, err := findMainPackages(pkgs)
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println("#TOTAL MAIN: " + strconv.Itoa(len(mains)) + "\n")
	if flags.Main != "" {
		fmt.Println("Capture -- ", flags.Main, "\n")
	}

	//extract scope from pkgs
	if len(pkgs) > 1 { //run under proj dir
		scope = append(scope, pkgs[0].Pkg.Path()) //bz: the 1st pkg has the scope info == the root pkg
	} //else: default input .go file

	my_maxTime = 0
	default_maxTime = 0
	my_minTime = 10000000000000
	default_minTime = 10000000000000

	if flags.DoRaceReq {
		doRaceReq(mains)
		return
	}else{
		//DoTogether and DoParallel cannot both be true
		if flags.DoParallel {
			doParallel(mains) //bz: --> fatal error: concurrent map writes!! discarded
		} else {
			if flags.DoTogether {
				doTogether(mains)
			} else {
				doEach(mains)
			}
		}
	}

	fmt.Println("\n\nBASELINE All Done  -- PTA/CG Build. \n")

	if flags.DoCompare || flags.DoDefault {
		fmt.Println("Default Algo:")
		fmt.Println("Total: ", (time.Duration(default_elapsed)*time.Millisecond).String()+".")
		fmt.Println("Max: ", default_maxTime.String()+".")
		fmt.Println("Min: ", default_minTime.String()+".")
		fmt.Println("Avg: ", float32(default_elapsed)/float32(len(mains))/float32(1000), "s.")
	}

	if flags.DoDefault {
		return
	}

	fmt.Println("My Algo:")
	fmt.Println("Total: ", (time.Duration(my_elapsed)*time.Millisecond).String()+".")
	fmt.Println("Max: ", my_maxTime.String()+".")
	fmt.Println("Min: ", my_minTime.String()+".")
	fmt.Println("Avg: ", float32(my_elapsed)/float32(len(mains))/float32(1000), "s.")
}

//baseline: all main together
func doTogether(mains []*ssa.Package) {
	if flags.DoCompare || flags.DoDefault {
		doMainDefault(mains)
		fmt.Println("........................................\n........................................")
	}
	if flags.DoDefault {
		return
	}

	doMainMy(mains)
}

//bz: test usesage in race checker
func doRaceReq(mains []*ssa.Package) {
	ptaConfig := &pointer.Config{
		Mains:          mains,
		Reflection:     false,
		BuildCallGraph: true,
		Log:            nil,
		//CallSiteSensitive: true, //kcfa
		Origin: true, //origin
		//shared config
		K:          1,
		LimitScope: true,         //bz: only consider app methods now -> no import will be considered
		DEBUG:      false,        //bz: rm all printed out info in console
		Scope:      scope,        //bz: analyze scope + input path
		Exclusion:  excludedPkgs, //bz: copied from race_checker if any
		TrackMore:  true,         //bz: track pointers with all types
		Level:      0,            //bz: see pointer.Config
		DoPerformance:  false,     //bz: i want to see this performance
	}

	start := time.Now()                                    //performance
	results, r_err := pointer.AnalyzeMultiMains(ptaConfig) // conduct pointer analysis
	t := time.Now()
	elapsed := t.Sub(start)
	if r_err != nil {
		panic(fmt.Sprintln(r_err))
	}
	fmt.Println("\nDone  -- PTA/CG Build; Using " + elapsed.String() + ".\n ")

	//check
	fmt.Println("#Receive Result: ", len(results))
	for main, result := range results {
		fmt.Println("Receive result (#Queries: ", len(result.Queries), ", #IndirectQueries: ", len(result.IndirectQueries), ") for main: ", main.String())
	}
}

func doMainMy(mains []*ssa.Package) *[]pointer.Result {
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
		LimitScope:    true,                //bz: only consider app methods now -> no import will be considered
		DEBUG:         false,               //bz: rm all printed out info in console
		Scope:         scope,               //bz: analyze scope + input path
		Exclusion:     excludedPkgs,        //bz: copied from race_checker if any
		TrackMore:     true,                //bz: track pointers with all types
		Level:         0,                   //bz: see pointer.Config
		DoPerformance: flags.DoPerforamnce, //bz: if we output performance related info
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

	if my_maxTime < elapsed {
		my_maxTime = elapsed
	}
	if my_minTime > elapsed {
		my_minTime = elapsed
	}

	return nil
}

func doMainDefault(mains []*ssa.Package) []*default_algo.Result {
	// Configure pointer analysis to build call-graph
	ptaConfig := &default_algo.Config{
		Mains:           mains, //one main per time
		Reflection:      false,
		BuildCallGraph:  true,
		Log:             nil,
		DoPerformance:   flags.DoPerforamnce, //bz: I add to output performance for comparison
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

	if default_maxTime < elapsed {
		default_maxTime = elapsed
	}
	if default_minTime > elapsed {
		default_minTime = elapsed
	}

	if len(result.Warnings) > 0 {
		fmt.Println("Warning: ", len(result.Warnings)) //bz: just do not report not used var on result
	}

	return nil
}

//baseline: foreach
func doEach(mains []*ssa.Package) {
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
			default_elapsed = default_elapsed + t.Sub(start).Milliseconds()
			start = time.Now()
			fmt.Println("........................................\n........................................")
		}

		if flags.DoDefault {
			continue //skip running mine
		}

		//my
		fmt.Println("My Algo: ")
		r_my = doEachMainMy(i, main) //mypta
		t := time.Now()
		my_elapsed = my_elapsed + t.Sub(start).Milliseconds()

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

func doEachMainMy(i int, main *ssa.Package) *pointer.ResultWCtx {
	var logfile *os.File
	var err error
	if flags.DoLog { //create my log file
		logfile, err = os.Create("/Users/bozhen/Documents/GO2/go_tools/_logs/my_log_" + strconv.Itoa(i))
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
		Level:         0,                   //bz: see pointer.Config
		DoPerformance: flags.DoPerforamnce, //bz: if we output performance related info
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
	defer logfile.Close()

	if flags.DoPerforamnce {
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

	if my_maxTime < elapsed {
		my_maxTime = elapsed
	}
	if my_minTime > elapsed {
		my_minTime = elapsed
	}

	if ptaConfig.DEBUG {
		result.DumpAll()
	}

	if flags.DoCompare {
		_r := result.GetResult()
		_r.Queries = result.Queries
		_r.IndirectQueries = result.IndirectQueries
		return _r //bz: we only need this when comparing results
	}

	return nil
}

func doEachMainDefault(i int, main *ssa.Package) *default_algo.Result {
	var logfile *os.File
	var err error
	if flags.DoLog { //create my log file
		logfile, err = os.Create("/Users/bozhen/Documents/GO2/go_tools/_logs/default_log_" + strconv.Itoa(i))
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
		DoPerformance:   flags.DoPerforamnce, //bz: I add to output performance for comparison
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

	if flags.DoPerforamnce {
		reaches, unreaches := countReachUnreachFunctions(result)
		fmt.Println("#Unreach Nodes: ", len(unreaches))
		fmt.Println("#Reach Nodes: ", len(reaches))
		fmt.Println("#Unreach Functions: ", len(unreaches))
		fmt.Println("#Reach Functions: ", len(reaches))
	}

	fmt.Println("\nDone  -- PTA/CG Build; Using ", elapsed.String(), ".\n")

	if default_maxTime < elapsed {
		default_maxTime = elapsed
	}
	if default_minTime > elapsed {
		default_minTime = elapsed
	}

	if len(result.Warnings) > 0 {
		fmt.Println("Warning: ", len(result.Warnings)) //bz: just do not report not used var on result
	}

	return result
}

// mainPackages returns the main packages to analyze.
// Each resulting package is named "main" and has a main function.
func findMainPackages(pkgs []*ssa.Package) ([]*ssa.Package, error) {
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

//bz: for default
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

//baseline: all main in parallel --> fatal error: concurrent map writes!! discarded
func doParallel(mains []*ssa.Package) map[*ssa.Package]*pointer.ResultWCtx {
	ret := make(map[*ssa.Package]*pointer.ResultWCtx) //record of result
	var _wg sync.WaitGroup
	for i, main := range mains {
		_wg.Add(1)
		go func(i int, main *ssa.Package) {
			fmt.Println(i, ". ", main.String())
			start := time.Now()
			fmt.Println("My Algo: ")
			r_my := doEachMainMy(i, main) //mypta
			t := time.Now()
			my_elapsed = my_elapsed + t.Sub(start).Milliseconds()
			//update
			ret[main] = r_my
			_wg.Done()
		}(i, main)
	}
	_wg.Wait()

	return ret
}
