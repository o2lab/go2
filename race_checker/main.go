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
	prog          *ssa.Program
	pkgs          []*ssa.Package
	mains         []*ssa.Package
	result        *pointer.Result
	ptaConfig     *pointer.Config
	analysisStat  stat
	HBgraph       *graph.Graph
	RWinsMap      map[goIns]graph.Node
	trieMap       map[fnInfo]*trie // map each function to a trie node
	RWIns         [][]ssa.Instruction
	insDRA        int // index of instruction (in main goroutine) at which to begin data race analysis
	storeIns      []string
	workList      []goroutineInfo
	reportedAddr  []ssa.Value // stores already reported addresses
	racyStackTops []string
	levels        map[int]int
	lockMap       map[ssa.Instruction][]ssa.Value // map each read/write access to a snapshot of actively maintained lockset
	lockSet       []ssa.Value                     // active lockset, to be maintained along instruction traversal
	RlockMap      map[ssa.Instruction][]ssa.Value // map each read/write access to a snapshot of actively maintained lockset
	RlockSet      []ssa.Value                     // active lockset, to be maintained along instruction traversal
	goLockset     map[int][]ssa.Value             // map each goroutine to its initial lockset
	goRLockset	  map[int][]ssa.Value			  // map each goroutine to its initial set of read locks
	mapFreeze     bool
	paramFunc     ssa.Value
	goStack       [][]string
	goCaller      map[int]int
	goNames       map[int]string
	chanBufMap    map[string][]*ssa.Send // map each channel name to every send instruction
	insertIndMap  map[string]int
	chanMap       map[ssa.Instruction][]string // map each read/write access to a list of channels with value(s) already sent to it
	chanName      string
	selectedChans map[string]ssa.Instruction // map selected channel name to last instruction in its clause
	selectDefault map[*ssa.Select]ssa.Instruction // map select statement to first instruction in its default block
	afterSelect	  map[ssa.Instruction]ssa.Instruction // map select statement to first instruction after select is done
	selectHB	  map[ssa.Instruction]ssa.Instruction // map edge LEAVING node to ENTERING node
	selectafterHB	  map[ssa.Instruction]ssa.Instruction
	serverWorker   int
}

type fnInfo struct { // all fields must be comparable for fnInfo to be used as key to trieMap
	fnName     string
	contextStr string
}

type goIns struct { // an ssa.Instruction with goroutine info
	ins			ssa.Instruction
	goID 		int
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
	Analysis     *analysis
	allPkg       = true
	excludedPkgs []string
	testMode     = false // Used by race_test.go for collecting output.
)

const trieLimit = 2      // set as user config option later, an integer that dictates how many times a function can be called under identical context
const efficiency = false // configuration setting to avoid recursion in tested program
const fromPath = "" // interested packages are those located at this path
// sample paths:
// gRPC - google.golang.org/grpc
// traefik - github.com/traefik
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
		//"version",
		//"sort",
	}
}

// main sets up arguments and calls staticAnalysis function
func main() {
	debug := flag.Bool("debug", false, "Prints debug messages.")
	lockOps := flag.Bool("lockOps", false, "Prints lock and unlock operations. ")
	flag.BoolVar(&stats.CollectStats, "collectStats", false, "Collect analysis statistics.")
	help := flag.Bool("help", false, "Show all command-line options.")
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

	log.SetFormatter(&log.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "15:04:05",
	})

	err := staticAnalysis(flag.Args())
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
