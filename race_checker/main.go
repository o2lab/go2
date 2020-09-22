package main

import (
	"flag"
	"fmt"
	"github.com/o2lab/race-checker/stats"
	log "github.com/sirupsen/logrus"
	"github.com/twmb/algoimpl/go/graph"
	"golang.org/x/tools/go/pointer"
	"golang.org/x/tools/go/ssa"
	"strings"
)

type analysis struct {
	prog         *ssa.Program
	pkgs         []*ssa.Package
	mains        []*ssa.Package
	result       *pointer.Result
	ptaConfig    *pointer.Config
	analysisStat stat
	HBgraph      *graph.Graph
	RWinsMap     map[ssa.Instruction]graph.Node
	trieMap      map[fnInfo]*trie // map each function to a trie node
}

type fnInfo struct { // all fields must be comparable for fnInfo to be used as key to trieMap
	fnName     string
	contextStr string
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

type RWInsInd struct {
	goID  int
	goInd int
}

var (
	Analysis      *analysis
	focusPkgs     []string
	excludedPkgs  []string
	allPkg        = true
	levels        = make(map[int]int)
	RWIns         [][]ssa.Instruction
	storeIns      []string
	workList      []goroutineInfo
	reportedAddr  []ssa.Value
	racyStackTops []string
	lockMap       = make(map[ssa.Instruction][]ssa.Value) // map each read/write access to a snapshot of actively maintained lockset
	lockSet       []ssa.Value                             // active lockset, to be maintained along instruction traversal
	addrNameMap   = make(map[string][]ssa.Value)          // for potential optimization purposes
	addrMap       = make(map[string][]RWInsInd)           // for potential optimization purposes
	paramFunc     ssa.Value
	goStack       [][]string
	goCaller      = make(map[int]int)
	goNames       = make(map[int]string)
	chanBufMap    = make(map[string][]*ssa.Send)
	chanName      string
	insertIndMap  = make(map[string]int)
	chanMap       = make(map[ssa.Instruction][]string) // map each read/write access to a list of channels with value(s) already sent to it
	trieLimit     = 2                                  // set as user config option later, an integer that dictates how many times a function can be called under identical context
	// proper forloop detection would require trieLimit of at least 2
)

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
	}
}

func main() {
	debug := flag.Bool("debug", false, "Prints debug messages.")
	focus := flag.String("focus", "", "Specifies a list of packages to check races.")
	ptrAnalysis := flag.Bool("ptrAnalysis", false, "Prints pointer analysis results. ")
	flag.BoolVar(&stats.CollectStats, "collectStats", false, "Collect analysis statistics.")
	help := flag.Bool("help", false, "Show all command-line options.")
	//flag.BoolVar(&allPkg, "all-package", true, "Analyze all packages required by the main package.")
	flag.Parse()
	if *help {
		flag.PrintDefaults()
		return
	}
	if *debug {
		log.SetLevel(log.DebugLevel)
	}
	if *ptrAnalysis {
		log.SetLevel(log.TraceLevel)
	}
	if *focus != "" {
		focusPkgs = strings.Split(*focus, ",")
		focusPkgs = append(focusPkgs, "command-line-arguments")
		allPkg = false
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
