package main

import (
	"flag"
	"fmt"
	log "github.com/sirupsen/logrus"
	"github.tamu.edu/April1989/go_tools/go/packages"
	"github.tamu.edu/April1989/go_tools/go/pointer"
	"github.tamu.edu/April1989/go_tools/go/ssa"
	"github.tamu.edu/April1989/go_tools/go/ssa/ssautil"
	"os"
	"strconv"
	"time"
)

var excludedPkgs = []string{//bz: excluded a lot of default constraints
	"runtime",
	"reflect",
	"os",
}
var includePkgs = []string{ // bz: we only include these pkgs
	"atomic",
	"sync",
	"google.golang.org/grpc",
	"github.com/pingcap/tidb",
}
var projPath = ""   // interested packages are those located at this path
var maxTime time.Duration
var minTime time.Duration


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

/**
bz: test record
16   package google.golang.org/grpc/interop/client  -> .taggedValue panic
---------------------------------------------------------------------------
../race_checker/tests/cg.go
*/
func main() {
	path := flag.String("path", "", "Designated project filepath. ")
	flag.Parse()
	if *path != "" {
		projPath = *path
	}
	args := flag.Args()
	cfg := &packages.Config{
		Mode:  packages.LoadAllSyntax, // the level of information returned for each package
		Dir:   "",                     // directory in which to run the build system's query tool
		Tests: false,                  // setting Tests will include related test packages
	}
	fmt.Println("Loading input packages...")
	initial, err := packages.Load(cfg, args...)
	if err != nil {
		return
	}
	if packages.PrintErrors(initial) > 0 {
		errSize, errPkgs := packages.PrintErrorsAndMore(initial) //bz: errPkg will be nil in initial
		if errSize > 0 {
			log.Info("Excluded the following packages contain errors, due to the above errors. ")
			for i, errPkg := range errPkgs {
				log.Info(i, " ", errPkg.ID)
			}
			log.Info("Continue   -- ")
		}
	} else if len(initial) == 0 {
		fmt.Println("Package list empty")
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

	maxTime = 0
	minTime = 10000000000000

	//baseline: foreach
	start := time.Now()   //performance
	for i, main := range mains {
		if i == 16 {
			continue  //TODO: bz: panic for grpc
		}
 		fmt.Println(i, " ", main.String())
		doEachMain(i, main)
		fmt.Println("=============================================================================")
	}
	t := time.Now()
	elapsed := t.Sub(start)
	fmt.Println("\n\nBASELINE All Done  -- PTA/CG Build.\nTOTAL: ", elapsed.String() + ".")
	fmt.Println("Max: ", maxTime.String() + ".")
	fmt.Println("Min: ", minTime.String() + ".")
	fmt.Println("Avg: ", (float32(elapsed.Milliseconds())/float32(len(mains) - 1)/float32(1000)), "s." )
}

func doEachMain(i int, main *ssa.Package) {
	//create my log file
	logfile, err := os.Create("/Users/bozhen/Documents/GO2/go2/go_tools/_logs/full_log_" + strconv.Itoa(i))
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp: true,
	})
	var scope []string
	if projPath != "" {
		scope = []string {projPath}
	}
	if len(includePkgs) > 0 {
		for _, include := range includePkgs {
			scope = append(scope, include)
		}
	}
	var mains []*ssa.Package
	mains = append(mains, main)
	// Configure pointer analysis to build call-graph
	ptaConfig := &pointer.Config{
		Mains:          mains, //bz: NOW assume only one main
		Reflection:     false,
		BuildCallGraph: true,
		Log:            nil,//logfile,
		//CallSiteSensitive: true, //kcfa
		Origin:     true, //origin
		//shared config
		K:          1,
		LimitScope: true, //bz: only consider app methods now -> no import will be considered
		DEBUG:      false, //bz: rm all printed out info in console
		Scope:      scope, //bz: analyze scope + include
		Exclusion:  excludedPkgs, //bz: copied from race_checker
		DiscardQueries: true, //bz: do not use query any more
	}

	//*** compute pta here
	start := time.Now()                           //performance
	result, err := pointer.AnalyzeWCtx(ptaConfig) // conduct pointer analysis
	t := time.Now()
	elapsed := t.Sub(start)
	if err != nil {
		log.Fatal(err)
	}
	defer logfile.Close()
	log.SetOutput(logfile)
	fmt.Println("\nDone  -- PTA/CG Build; Using " + elapsed.String() + ". \nGo check gologfile for detail. ")

	if maxTime < elapsed {
		maxTime = elapsed
	}
	if minTime > elapsed {
		minTime = elapsed
	}

	if ptaConfig.DEBUG {
		result.DumpAll()
	}
}