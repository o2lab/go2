package main

import (
	"github.com/april1989/origin-go-tools/go/myutil/flags"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"log"
)

/*
	input is gorace.yml
    analyze yml file -> users specify analysis config
*/

var (
	useNewPTA      = true
	trieLimit      = 2    // set as user config option later, an integer that dictates how many times a function can be called under identical context
	efficiency     = true // configuration setting to avoid recursion in tested program
	channelComm    = true // analyze channel communication
	allEntries     = false
	printDebugInfo = false //bz: replace the usage for old allEntries
	useDefaultPTA  = false
	getGo          = false
	goTest         bool // running test script
	debugFlag      bool
	excludedFns    []string
	testMode       = false  // Used by race_test.go for collecting output.
	excludedPkgs   []string // ******** FOR ISSUE 14 *************
	PTSlimit       int      // ******** FOR ISSUE 14 *************
	PTAscope       []string // ******** FOR ISSUE 14 *************

	////bz: skip traversing some functions that are not important in detection (or too verbose, do not want to analyze)
	//excludedFns = []string{ //bz: grpc specific, hasprefix
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

// DecodeYmlFile takes in absolute path of gorace.yml file
func DecodeYmlFile(absPath string) {
	grfile, err := ioutil.ReadFile(absPath)
	if err != nil {
		log.Fatal(err)
	}
	grs := GoRace{}
	err = yaml.Unmarshal(grfile, &grs)
	if err != nil {
		log.Fatalf("Yml Decode Error: %v", err)
	}

	for _, eachCfg := range grs.GoRaceCfgs {
		excludedPkgs = eachCfg.ExPkgs
		flags.PTSLimit = eachCfg.PTS
		PTAscope = eachCfg.Scope
	}
}
