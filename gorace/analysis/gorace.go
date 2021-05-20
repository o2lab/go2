package gorace

import (
	"github.com/april1989/origin-go-tools/go/myutil/flags"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"log"
)

/*
	input is analysis.yml
    analyze yml file -> users specify analysis config
*/

var (
	UseNewPTA      = true
	TrieLimit      = 2    // set as user config option later, an integer that dictates how many times a function can be called under identical context
	Efficiency     = true // configuration setting to avoid recursion in tested program
	ChannelComm    = true // analyze channel communication
	AllEntries     = false
	PrintDebugInfo = false //bz: replace the usage for old AllEntries
	UseDefaultPTA  = false
	PrintStack     = false
	GoTest         bool // running test script
	DebugFlag      bool
	ExcludedFns    []string
	TestMode       = false  // Used by race_test.go for collecting output.
	ExcludedPkgs   []string // ******** FOR ISSUE 14 *************
	PTSlimit       int      // ******** FOR ISSUE 14 *************
	PTAscope       []string // ******** FOR ISSUE 14 *************

	////bz: skip traversing some functions that are not important in detection (or too verbose, do not want to analyze)
	//ExcludedFns = []string{ //bz: grpc specific, hasprefix
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
	Scope  []string   `yaml:"analysisScope"`
}

// DecodeYmlFile takes in absolute path of analysis.yml file
func DecodeYmlFile(absPath string) {
	grfile, err := ioutil.ReadFile(absPath)
	if err != nil {
		log.Fatal("No gorace.yml file found in current directory. Please provide gorace.yml file with config info. ")
	}
	grs := GoRace{}
	err = yaml.Unmarshal(grfile, &grs)
	if err != nil {
		log.Fatalf("Yml Decode Error: %v", err)
	}

	for _, eachCfg := range grs.GoRaceCfgs {
		ExcludedPkgs = eachCfg.ExPkgs
		flags.PTSLimit = eachCfg.PTS
		PTAscope = eachCfg.Scope
	}
}
