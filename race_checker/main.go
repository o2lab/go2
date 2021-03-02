package main

import (
	"flag"
	"fmt"
	"github.com/o2lab/race-checker/stats"
	log "github.com/sirupsen/logrus"
	"github.com/twmb/algoimpl/go/graph"
	"github.tamu.edu/April1989/go_tools/go/pointer"
	pta0 "github.tamu.edu/April1989/go_tools/go/pointer_default"
	"sync"
	"syscall"

	//"golang.org/x/tools/go/pointer"
	"github.tamu.edu/April1989/go_tools/go/ssa"
)

type analysis struct {
	mu 				sync.RWMutex
	useNewPTA    	bool //useNewPTA the new pta
	useDefaultPTA	bool //use default go pta
	result       	*pointer.Result //now can reuse the result
	pta0Result 		*pta0.Result
	ptaConfig    	*pointer.Config
	pta0Cfg			*pta0.Config

	fromPath		string
	prog            *ssa.Program
	pkgs            []*ssa.Package
	mains           []*ssa.Package
	analysisStat    stat
	HBgraph         *graph.Graph
	RWinsMap        map[goIns]graph.Node
	trieMap         map[fnInfo]*trie 				// map each function to a trie node
	RWIns           map[string][][]ssa.Instruction
	RWInsInd	    [][]ssa.Instruction
	insDRA          int 							// index of instruction (in main goroutine) at which to begin data race analysis
	storeIns        []string
	workList        []goroutineInfo
	reportedAddr    []ssa.Value 					// stores already reported addresses
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
	chanToken		map[string]string				// map token number to channel name
	chanBuf         map[string]int         			// map each channel to its buffer length
	chanRcvs        map[string][]*ssa.UnOp 			// map each channel to receive instructions
	chanSnds        map[string][]*ssa.Send 			// map each channel to send instructions
	chanName        string
	selectBloc      map[int]*ssa.Select             // index of block where select statement was encountered
	selReady        map[*ssa.Select][]string        // store name of ready channels for each select statement
	selUnknown		map[*ssa.Select][]string		// channels are passed in as parameters
	selectCaseBegin map[ssa.Instruction]string      // map first instruction in clause to channel name
	selectCaseEnd   map[ssa.Instruction]string      // map last instruction in clause to channel name
	selectCaseBody	map[ssa.Instruction]*ssa.Select		// map instructions to select instruction
	selectDone      map[ssa.Instruction]*ssa.Select // map first instruction after select is done to select statement
	ifSuccBegin     map[ssa.Instruction]*ssa.If     // map beginning of succ block to if statement
	ifFnReturn      map[*ssa.Function]*ssa.Return   // map "if-containing" function to its final return
	ifSuccEnd       map[ssa.Instruction]*ssa.Return // map ending of successor block to final return statement
	commIfSucc      []ssa.Instruction               // store first ins of succ block that contains channel communication
	omitComm        []*ssa.BasicBlock               // omit these blocks as they are race-free due to channel communication
}

type AnalysisRunner struct {
	mu    			sync.Mutex
	Analysis 		*analysis
	fromPath		string
	prog 			*ssa.Program
	pkgs 			[]*ssa.Package
	ptaconfig 		*pointer.Config
	pta0Cfg			*pta0.Config
	ptaResult 		*pta0.Result
	trieLimit		int      // set as user config option later, an integer that dictates how many times a function can be called under identical context
	efficiency		bool // configuration setting to avoid recursion in tested program
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
	nonMainPkgs  []string
	testMode     = false // Used by race_test.go for collecting output.
)

var useNewPTA = false
var trieLimit = 2      // set as user config option later, an integer that dictates how many times a function can be called under identical context
var efficiency = false // configuration setting to avoid recursion in tested program
var channelComm = true // analyze channel communication
var entryFn = "main"
var allEntries = false
var useDefaultPTA = true

func init() {
	excludedPkgs = []string{
		"fmt",
		"reflect",
	}
}

// main sets up arguments and calls staticAnalysis function
func main() {//default: -useNewPTA
	newPTA := flag.Bool("useNewPTA", false, "Use the new pointer analysis in go_tools.")
	builtinPTA := flag.Bool("useDefaultPTA", true, "Use the built-in pointer analysis.")
	debug := flag.Bool("debug", true, "Prints log.Debug messages.")
	lockOps := flag.Bool("lockOps", false, "Prints lock and unlock operations. ")
	flag.BoolVar(&stats.CollectStats, "collectStats", false, "Collect analysis statistics.")
	help := flag.Bool("help", false, "Show all command-line options.")
	withoutComm := flag.Bool("withoutComm", false, "Show analysis results without communication consideration.")
	withComm := flag.Bool("withComm", false, "Show analysis results with communication consideration.")
	analyzeAll := flag.Bool("analyzeAll", false, "Analyze all main() entry-points. ")
	flag.Parse()
	if *help {
		flag.PrintDefaults()
		return
	}
	if *newPTA {
		useNewPTA = true
		useDefaultPTA = false
	}
	if *builtinPTA {
		useDefaultPTA = true
		useNewPTA = false
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
	}
	if *withComm {
		trieLimit = 1
		efficiency = true
		channelComm = true
	}
	if *analyzeAll {
		allEntries = true
	}

	log.SetFormatter(&log.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "15:04:05",
	})

	// set ulimit -n within the executed program
	var rLimit syscall.Rlimit
	err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rLimit)
	if err != nil {
		fmt.Println("Error Getting Rlimit ", err)
	}
	rLimit.Max = 10240
	rLimit.Cur = 10240
	err = syscall.Setrlimit(syscall.RLIMIT_NOFILE, &rLimit)
	if err != nil {
		fmt.Println("Error Setting Rlimit ", err)
	}
	err = syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rLimit)
	if err != nil {
		fmt.Println("Error Getting Rlimit ", err)
	}

	runner := &AnalysisRunner{
		trieLimit: trieLimit,
		efficiency: efficiency,
	}
	err0 := runner.Run(flag.Args())
	if stats.CollectStats {
		stats.ShowStats()
	}
	if err0 != nil {
		log.Fatal(err)
	}
}
