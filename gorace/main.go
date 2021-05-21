//+build !windows

package main

import (
	"github.com/o2lab/gorace/stats"
	log "github.com/sirupsen/logrus"
	"os"
)


func init() {
	curDir, _ := os.Getwd()
	ymlPath := curDir + "/gorace.yml"
	DecodeYmlFile(ymlPath)
}

// main sets up arguments and calls staticAnalysis function
func main() {
	ParseFlagsAndInput()
	runner := &AnalysisRunner{
		trieLimit:  trieLimit,
		efficiency: efficiency,
	}
	//err := runner.Run(flag.Args()) //bz: original code
	err := runner.Run2()
	if err != nil {
		log.Fatal(err)
	}
	if stats.CollectStats {
		stats.ShowStats()
	}
}
