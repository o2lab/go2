package main

import (
	"flag"
	"fmt"
	"github.com/o2lab/race-checker/stats"
	log "github.com/sirupsen/logrus"
	"github.com/twmb/algoimpl/go/graph"
	"golang.org/x/tools/go/pointer"
	"golang.org/x/tools/go/ssa"
)

type analysis struct {
	prog            *ssa.Program
	pkgs            []*ssa.Package
	mains           []*ssa.Package
	result          *pointer.Result
	ptaConfig       *pointer.Config
	analysisStat    stat
	HBgraph         *graph.Graph
	RWinsMap        map[goIns]graph.Node
	trieMap         map[fnInfo]*trie // map each function to a trie node
	RWIns           [][]ssa.Instruction
	insDRA          int // index of instruction (in main goroutine) at which to begin data race analysis
	storeIns        []string
	workList        []goroutineInfo
	reportedAddr    []ssa.Value // stores already reported addresses
	racyStackTops   []string
	levels          map[int]int
	lockMap         map[ssa.Instruction][]ssa.Value // map each read/write access to a snapshot of actively maintained lockset
	lockSet         []ssa.Value                     // active lockset, to be maintained along instruction traversal
	RlockMap        map[ssa.Instruction][]ssa.Value // map each read/write access to a snapshot of actively maintained lockset
	RlockSet        []ssa.Value                     // active lockset, to be maintained along instruction traversal
	goLockset       map[int][]ssa.Value             // map each goroutine to its initial lockset
	goRLockset      map[int][]ssa.Value             // map each goroutine to its initial set of read locks
	mapFreeze       bool
	paramFunc       ssa.Value
	goStack         [][]string
	goCaller        map[int]int
	goNames         map[int]string
	chanBuf         map[string]int         // map each channel to its buffer length
	chanRcvs        map[string][]*ssa.UnOp // map each channel to receive instructions
	chanSnds        map[string][]*ssa.Send // map each channel to send instructions
	chanName        string
	selectBloc      map[int]*ssa.Select             // index of block where select statement was encountered
	selReady        map[*ssa.Select][]string        // store name of ready channels for each select statement
	selCaseCnt      map[*ssa.Select]int             // select case count
	selectCaseBegin map[ssa.Instruction]string      // map first instruction in clause to channel name
	selectCaseEnd   map[ssa.Instruction]string      // map last instruction in clause to channel name
	selectDone      map[ssa.Instruction]*ssa.Select // map first instruction after select is done to select statement
	ifSuccBegin     map[ssa.Instruction]*ssa.If     // map beginning of succ block to if statement
	ifFnReturn      map[*ssa.Function]*ssa.Return   // map "if-containing" function to its final return
	ifSuccEnd       map[ssa.Instruction]*ssa.Return // map ending of successor block to final return statement
	commIfSucc      []ssa.Instruction               // store first ins of succ block that contains channel communication
	omitComm        []*ssa.BasicBlock               // omit these blocks as they are race-free due to channel communication
}

type AnalysisRunner struct {
	Analysis *analysis
}

type fnInfo struct { // all fields must be comparable for fnInfo to be used as key to trieMap
	fnName     string
	contextStr string
}

type goIns struct { // an ssa.Instruction with goroutine info
	ins  ssa.Instruction
	goID int
}

type goroutineInfo struct {
	goIns       *ssa.Go
	entryMethod string
	goID        int
}

type stat struct {
	nAccess    int
	nGoroutine int
}

type trie struct {
	fnName    string
	budget    int
	fnContext []string
}

var (
	excludedPkgs []string
	testMode     = false // Used by race_test.go for collecting output.
)

var trieLimit = 2      // set as user config option later, an integer that dictates how many times a function can be called under identical context
var efficiency = false // configuration setting to avoid recursion in tested program
var channelComm = true // analyze channel communication
var fromPath = ""      // interested packages are those located at this path
// sample paths:
// gRPC - google.golang.org/grpc
// traefik - github.com/traefik
// gogs - gogs.io/gogs
// istio - istio.io/istio
// docker -
// kubernetes -

func init() {
	excludedPkgs = []string{
		"runtime",
		"fmt",
		"reflect",
		"encoding",
		"errors",
		"bytes",
		"strconv",
		"strings",
		"bytealg",
		"race",
		"syscall",
		"poll",
		"trace",
		"logging",
		"os",
		"builtin",
		"pflag",
		"log",
		"reflect",
		"internal",
		"impl",
		"transport", // grpc
		"version",
		"sort",
		"filepath",
	}
}

// main sets up arguments and calls staticAnalysis function
func main() {
	debug := flag.Bool("debug", false, "Prints debug messages.")
	lockOps := flag.Bool("lockOps", false, "Prints lock and unlock operations. ")
	flag.BoolVar(&stats.CollectStats, "collectStats", false, "Collect analysis statistics.")
	help := flag.Bool("help", false, "Show all command-line options.")
	withoutComm := flag.Bool("withoutComm", false, "Show analysis results without communication consideration.")
	withComm := flag.Bool("withComm", false, "Show analysis results with communication consideration.")
	flag.Parse()
	if *help {
		flag.PrintDefaults()
		return
	}
	if *debug {
		log.SetLevel(log.DebugLevel)
	}
	if *lockOps {
		log.SetLevel(log.TraceLevel)
	}
	if *withoutComm {
		trieLimit = 1
		efficiency = true
		channelComm = false
		fromPath = "google.golang.org/grpc"
	}
	if *withComm {
		trieLimit = 1
		efficiency = true
		channelComm = true
		fromPath = "google.golang.org/grpc"
	}

	log.SetFormatter(&log.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "15:04:05",
	})
	runner := &AnalysisRunner{}
	err := runner.Run(flag.Args())
	if stats.CollectStats {
		stats.ShowStats()
	}
	if err != nil {
		log.Fatal(err)
	}
}

// mainPackages returns the main packages to analyze.
// Each resulting package is named "main" and has a main function.
func mainPackages(pkgs []*ssa.Package) ([]*ssa.Package, error) {
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
