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

	//for our debug use: default value here
	DEBUG          = false //bz: replace the usage for old allEntries -> print out verbose debug info
	DEBUGHBGraph   = false //bz: print out verbose debug info in buildHB()
	turnOnSpinning = true  //bz: if we run this in goland, turn this off... this only works for terminal

	//from users yml or flags
	allEntries    = false  //user flag
	excludedPkgs  []string // ******** FOR ISSUE 14 *************
	PTAscope      []string // ******** FOR ISSUE 14 *************
	userDir       string   //bz: user specify dir -> we run gorace here
	userInputFile []string //bz: used when input is a .go file, not a path

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
		log.Fatal("No gorace.yml file found in current directory:", absPath, ". Please provide gorace.yml file with config info. ")
	}
	grs := GoRace{}
	err = yaml.Unmarshal(grfile, &grs)
	if err != nil {
		log.Fatalf("Yml Decode Error: %v", err)
	}

	for _, eachCfg := range grs.GoRaceCfgs {
		excludedPkgs = eachCfg.ExPkgs
		flags.PTSLimit = eachCfg.PTS //bz: limit the size of pts
		PTAscope = eachCfg.Scope
	}
}
