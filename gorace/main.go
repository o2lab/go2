//+build !windows

package main

import (
	"flag"
	"fmt"
	log "github.com/sirupsen/logrus"
	"os"
	"syscall"
	"github.com/o2lab/gorace/stats"
	gorace "github.com/o2lab/gorace/analysis"

)




func init() {
	curDir, _ := os.Getwd()
	ymlPath := curDir + "/analysis.yml"
	gorace.DecodeYmlFile(ymlPath)
	main()
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
		gorace.PrintStack = true
	}
	if *runTest {
		gorace.Efficiency = false
		gorace.TrieLimit = 2
		gorace.GoTest = true
	}
	if *help {
		flag.PrintDefaults()
		return
	}
	if *newPTA {
		gorace.UseNewPTA = true
		gorace.UseDefaultPTA = false
	}
	if *builtinPTA {
		gorace.UseDefaultPTA = true
		gorace.UseNewPTA = false
	}
	if *debug {
		gorace.DebugFlag = true
		log.SetLevel(log.DebugLevel)
	}
	if *lockOps {
		log.SetLevel(log.TraceLevel)
	}
	if *withoutComm {
		gorace.ChannelComm = false
	}
	if *withComm {
		gorace.ChannelComm = true
	}
	if *analyzeAll {
		gorace.AllEntries = true
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

	runner := &gorace.AnalysisRunner{}
	//err0 := runner.Run(flag.Args()) //bz: original code
	err0 := runner.Run2(flag.Args())
	if stats.CollectStats {
		stats.ShowStats()
	}
	if err0 != nil {
		log.Fatal(err)
	}
}
