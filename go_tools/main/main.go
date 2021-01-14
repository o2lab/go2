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

var excludedPkgs = []string{
	//"runtime",
	//"fmt",
	//"reflect",
	//"encoding",
	//"errors",
	//"bytes",
	//"strconv",
	//"strings",
	//"bytealg",
	//"race",
	//"syscall",
	//"poll",
	//"trace",
	//"logging",
	//"os",
	//"builtin",
	//"pflag",
	//"log",
	//"reflect",
	//"internal",
	//"impl",
	//"transport", // grpc
	//"version",
	//"sort",
	//"filepath",
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

//bz: tested
// cmd/callgraph/testdata/src/pkg/pkg.go
// godel2: mytest/dine3-chan-race.go, mytest/no-race-mut-bad.go, mytest/prod-cons-race.go
// ../go2/race_checker/GoBench/Kubernetes/88331/main.go
// ../go2/race_checker/GoBench/Grpc/3090/main.go
// ../go2/race_checker/pointer_analysis_test/main.go

// ../go2/race_checker/GoBench/Cockroach/35501/main.go
// ../go2/race_checker/GoBench/Etcd/9446/main.go
// ../go2/race_checker/tests/GoBench/Grpc/1862/main.go
// ../go2/race_checker/GoBench/Istio/8144/main.go
// ../go2/race_checker/GoBench/Istio/8967/main.go

//TODO: program counter ???
func main() {
	projPath := flag.String("path", "", "Designated project filepath. ")
	flag.Parse()
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
		fmt.Println("packages contain errors")
		return
	} else if len(initial) == 0 {
		fmt.Println("package list empty")
		return
	}

	// Print the names of the source files
	// for each package listed on the command line.
	for nP, pkg := range initial {
		fmt.Println(pkg.ID, pkg.GoFiles)
		fmt.Println("Done  -- " + strconv.Itoa(nP+1) + " packages loaded")
	}
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

	//create my log file
	logfile, err := os.Create("gologfile") //bz: i do not want messed up log, create/overwrite one each time
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp: true,
	})

	var scope []string
	if projPath != nil {
		scope = []string {*projPath}
	}
	// Configure pointer analysis to build call-graph
	ptaConfig := &pointer.Config{
		Mains:          mains, //bz: NOW assume only one main
		Reflection:     false,
		BuildCallGraph: true,
		Log:            logfile,
		//kcfa
		//CallSiteSensitive: true,
		//origin
		Origin: true,
		//shared config
		K:          1,
		LimitScope: true, //bz: only consider app methods now
		DEBUG:      true, //bz: rm all printed out info in console
		Scope:      scope, //bz: analyze scope
		Exclusions: excludedPkgs,//bz: copied from race_checker
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

	if ptaConfig.DEBUG {
		result.DumpAll()
	}
}
