package main

import (
	"flag"
	"fmt"
	"github.com/april1989/origin-go-tools/go/myutil/flags"
	"github.com/o2lab/gorace/stats"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

/*
	input is gorace.yml
    analyze yml file -> users specify analysis config
*/

var (
	//default
	useNewPTA     = true
	useDefaultPTA = false
	trieLimit     = 2    // set as user config option later, an integer that dictates how many times a function can be called under identical context
	efficiency    = true // configuration setting to avoid recursion in tested program
	channelComm   = true // analyze channel communication
	printStack    = true //default
	goTest        bool   // running test script
	excludedFns   []string
	testMode      = false // Used by race_test.go for collecting output.
	PTAscope      []string

	//for our debug use: default value here: false false true
	DEBUG          = false //bz: replace the usage for old allEntries -> print out verbose debug info
	DEBUGHBGraph   = false //bz: print out verbose debug info in buildHB()
	turnOnSpinning = false //bz: if we run this in goland, turn this off... this only works for terminal

	//from users yml or flags
	allEntries    = false  //user flag
	excludedPkgs  []string // ******** FOR ISSUE 14 *************
	inputScope      []string //bz: this can be nil -> default is to analyze the current folder
	userDir       string   //bz: user specify dir -> we run gorace here
	userInputFile []string //bz: used when input is a .go file, not a path

	////bz: skip traversing some functions that are not important in detection (or too verbose, do not want to analyze)
	//excludedFns = []string{ //bz: grpc specific, use hasprefix
	//	"google.golang.org/grpc/grpclog",
	//	"(*testing.common).Log",
	//	"(*testing.common).Error",
	//	"(*testing.common).Fatal",
	//	"(*testing.common).Skip",
	//}
)

type GoRace struct {
	GoRaceCfgs []GoRaceCfg `yaml:"goracecfgs"`
}

type GoRaceCfg struct {
	ExPkgs []string `yaml:"excludePkgs"`
	PTS    int      `yaml:"PTSlimit"`
	Scope  []string `yaml:"analysisScope"`
}

//bz: move the config in main.go here
func ParseFlagsAndInput() { //default: -useNewPTA
	newPTA := flag.Bool("useNewPTA", true, "Use the new pointer analysis in go_tools.")
	builtinPTA := flag.Bool("useDefaultPTA", false, "Use the built-in pointer analysis.")
	debug := flag.Bool("debug", false, "Prints log.Debug messages.")
	lockOps := flag.Bool("lockOps", false, "Prints lock and unlock operations. ")
	flag.BoolVar(&stats.CollectStats, "collectStats", false, "Collect analysis statistics.")
	help := flag.Bool("help", false, "Show all command-line options.")
	withoutComm := flag.Bool("withoutComm", false, "Show analysis results without communication consideration.")
	withComm := flag.Bool("withComm", false, "Show analysis results with communication consideration.")
	analyzeAll := flag.Bool("analyzeAll", false, "Analyze all main() entry-points. ")
	runTest := flag.Bool("runTest", false, "For micro-benchmark debugging... ")
	showStack := flag.Bool("show", true, "Show call stack of each racy access. ")
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
		DEBUG = true
		DEBUGHBGraph = true
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

	input := flag.Args()
	if len(input) != 1 {
		fmt.Fprintf(os.Stderr, "Must provide one analysis directory: %v\n", input)
		os.Exit(1)
	} else {
		userDir = input[0]
		//JEFF: check if directory exists
		_, err := os.Stat(userDir)
		if err != nil {
			//println("os.Stat(): error for directory name ", directoryName)
			fmt.Fprintf(os.Stderr, "Error: %v\n", err.Error())
			os.Exit(1)
		} else {
			userDir, _ = filepath.Abs(userDir)
		}

		if strings.HasSuffix(userDir, ".go") {
			//bz: special handling when input is a .go file
			tmp := userDir
			idx := strings.LastIndex(tmp, "/")
			userDir = tmp[0:idx]
			userInputFile = append(userInputFile, tmp[idx+1:])
		}
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
}

// DecodeYmlFile takes in absolute path of gorace.yml file
func DecodeYmlFile(absPath string) {
	grfile, err := ioutil.ReadFile(absPath)
	if err != nil {
		log.Info("No gorace.yml file found in current directory:", absPath, ". Use default values.") //Please provide gorace.yml file with config info.
		//use default
		excludedPkgs = append(excludedPkgs, "fmt")
		excludedPkgs = append(excludedPkgs, "logrus")
		flags.PTSLimit = 10
		return
	}
	grs := GoRace{}
	err = yaml.Unmarshal(grfile, &grs)
	if err != nil {
		log.Fatalf("Yml Decode Error: %v", err)
	}

	for _, eachCfg := range grs.GoRaceCfgs {
		excludedPkgs = eachCfg.ExPkgs
		flags.PTSLimit = eachCfg.PTS //bz: limit the size of pts
		inputScope = eachCfg.Scope
	}
}
