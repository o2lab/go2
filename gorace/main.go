//+build !windows

package main

import (
	"flag"
	"fmt"
	"github.com/april1989/origin-go-tools/go/pointer"
	pta0 "github.com/april1989/origin-go-tools/go/pointer_default"
	"github.com/o2lab/gorace/stats"
	log "github.com/sirupsen/logrus"
	"github.com/twmb/algoimpl/go/graph"
	"os"
	"sync"
	"syscall"

	"github.com/april1989/origin-go-tools/go/ssa"
)

type analysis struct {
	mu      sync.RWMutex
	ptaRes  *pointer.Result //now can reuse the ptaRes
	ptaRes0 *pta0.Result
	ptaCfg  *pointer.Config
	ptaCfg0 *pta0.Config

	efficiency   bool
	trieLimit    int
	//getGo        bool // flag
	prog         *ssa.Program
	main         *ssa.Package
	analysisStat stat
	HBgraph      *graph.Graph
	RWinsMap     map[goIns]graph.Node
	trieMap      map[fnInfo]*trie    // map each function to a trie node
	RWIns        [][]*insInfo // instructions grouped by goroutine
	insMono      int                 // index of instruction (in main goroutine) before which the program is single-threaded
	storeFns     []*fnCallInfo
	workList     []goroutineInfo
	levels       map[int]int
	lockMap      map[ssa.Instruction][]ssa.Value // map each read/write access to a snapshot of actively maintained lockset
	lockSet      map[int][]*lockInfo             // active lockset, to be maintained along instruction traversal
	RlockMap     map[ssa.Instruction][]ssa.Value // map each read/write access to a snapshot of actively maintained lockset
	RlockSet     map[int][]*lockInfo             // active lockset, to be maintained along instruction traversal
	getParam     bool
	paramFunc       *ssa.Function
	goStack         [][]*fnCallInfo
	goCaller        map[int]int
	goCalls         map[int]*goCallInfo
	chanToken       map[string]string      // map token number to channel name
	chanBuf         map[string]int         // map each channel to its buffer length
	chanRcvs        map[string][]*ssa.UnOp // map each channel to receive instructions
	chanSnds        map[string][]*ssa.Send // map each channel to send instructions
	chanName        string
	selectBloc      map[int]*ssa.Select             // index of block where select statement was encountered
	selReady        map[*ssa.Select][]string        // store name of ready channels for each select statement
	selUnknown      map[*ssa.Select][]string        // channels are passed in as parameters
	selectCaseBegin map[ssa.Instruction]string      // map first instruction in clause to channel name
	selectCaseEnd   map[ssa.Instruction]string      // map last instruction in clause to channel name
	selectCaseBody  map[ssa.Instruction]*ssa.Select // map instructions to select instruction
	selectDone      map[ssa.Instruction]*ssa.Select // map first instruction after select is done to select statement
	ifSuccBegin     map[ssa.Instruction]*ssa.If     // map beginning of succ block to if statement
	ifFnReturn      map[*ssa.Function]*ssa.Return   // map "if-containing" function to its final return
	ifSuccEnd       map[ssa.Instruction]*ssa.Return // map ending of successor block to final return statement
	commIfSucc      []ssa.Instruction               // store first ins of succ block that contains channel communication
	omitComm        []*ssa.BasicBlock               // omit these blocks as they are race-free due to channel communication
	racyStackTops   []string
	inLoop          bool // entered a loop
	goInLoop        map[int]bool
	loopIDs         map[int]int // map goID to loopID
	allocLoop       map[*ssa.Function][]string
	bindingFV       map[*ssa.Go][]*ssa.FreeVar
	pbr             *ssa.Alloc
	commIDs         map[int][]int
	deferToRet      map[*ssa.Defer]ssa.Instruction

	entryFn         string         //bz: move from global to analysis field
	testEntry       *ssa.Function  //bz: test entry point -> just one!
	otherTests      []*ssa.Function //bz: all other tests that are in the same test pkg, TODO: bz: exclude myself

	twinGoID        map[*ssa.Go][]int //bz: whether two goroutines are spawned by the same loop; this might not be useful now since !sliceContains(a.reportedAddr, addressPair) && already filtered out the duplicate race check
	//mutualTargets   map[int]*mutualFns //bz: this mutual exclusion is for this specific go id (i.e., int)
}

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
	locBlocInd int
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

func init() {
	curDir, _ := os.Getwd()
	ymlPath := curDir + "/gorace.yml"
	DecodeYmlFile(ymlPath)
}

// main sets up arguments and calls staticAnalysis function
func main() { //default: -useNewPTA
	newPTA := flag.Bool("useNewPTA", false, "Use the new pointer analysis in go_tools.")
	builtinPTA := flag.Bool("useDefaultPTA", false, "Use the built-in pointer analysis.")
	debug := flag.Bool("debug", false, "Prints log.Debug messages.")
	lockOps := flag.Bool("lockOps", false, "Prints lock and unlock operations. ")
	flag.BoolVar(&stats.CollectStats, "collectStats", false, "Collect analysis statistics.")
	help := flag.Bool("help", false, "Show all command-line options.")
	withoutComm := flag.Bool("withoutComm", false, "Show analysis results without communication consideration.")
	withComm := flag.Bool("withComm", false, "Show analysis results with communication consideration.")
	analyzeAll := flag.Bool("analyzeAll", false, "Analyze all main() entry-points. ")
	runTest := flag.Bool("runTest", false, "For micro-benchmark debugging... ")
	showStack := flag.Bool("show", false, "Show call stack of each racy access. ")
	//setTrie := flag.Int("trieLimit", 1, "Set trie limit... ")
	flag.Parse()
	//if *setTrie > 1 {
	//	trieLimit = *setTrie
	//}
	if *showStack {
		printStack = true
	}
	if *runTest {
		efficiency = false
		trieLimit = 2
		goTest = true
	}
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
		debugFlag = true
		log.SetLevel(log.DebugLevel)
	}
	if *lockOps {
		log.SetLevel(log.TraceLevel)
	}
	if *withoutComm {
		channelComm = false
	}
	if *withComm {
		channelComm = true
	}
	if *analyzeAll {
		allEntries = true
	}
	// from Dr. H
	//analysisDirectories := flag.Args()
	//var directoryName = ""
	//if len(analysisDirectories) != 1 {
	//	fmt.Fprintf(os.Stderr, "Must provide one analysis directory: %v\n", analysisDirectories)
	//	os.Exit(1)
	//} else {
	//	directoryName = analysisDirectories[0]
	//	//JEFF: check if directory exists
	//	_, err := os.Stat(directoryName)
	//	if err != nil {
	//		//println("os.Stat(): error for directory name ", directoryName)
	//		fmt.Fprintf(os.Stderr, "Error: %v\n", err.Error())
	//	} else {
	//		directoryName, _ = filepath.Abs(directoryName)
	//	}
	//}

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
		trieLimit:  trieLimit,
		efficiency: efficiency,
	}
	//err0 := runner.Run(flag.Args()) //bz: original code
	err0 := runner.Run2(flag.Args())
	if stats.CollectStats {
		stats.ShowStats()
	}
	if err0 != nil {
		log.Fatal(err)
	}
}
