package flags

import (
	"flag"
	"time"
)



//user
var DoLog = false
var Main = "" //bz: run for a specific main in this pkg; start from 0
var DoDefault = false //bz: only Do default
var DoCompare = false //bz: this is super long
var TimeLimit time.Duration //bz: time limit, unit: ?h?m?s

//my use
var PrintCGNodes = false //bz: print #cgnodes (before solve())
var DoPerforamnce = true
var DoDetail = false //bz: print out all data from countReachUnreachXXX
var DoTogether = false //bz: do all main in a pkg together from the same root -> all mains linked by the root node
var DoParallel = false //bz: do all mains in a pkg in parallel, do each main by itself by parallel

//test useage in race checker
var DoRaceReq = false //bz: do all mains in a pkg sequential, but input is multiple mains



//bz: analyze all flags from input
func ParseFlags() {
	//user
	_main := flag.String("main", "", "Run for a specific main in this pkg.")
	_doLog := flag.Bool("doLog", false, "Do log. ")
	_doDefault := flag.Bool("doDefault", false, "Do default algo only. ")
	_doComp := flag.Bool("doCompare", false, "Do compare with default pta. ")
	_time := flag.String("timeLimit", "", "Set time limit to ?h?m?s or ?m?s or ?s, e.g. 1h15m30.918273645s. ")
	//my use
	_printCGNodes := flag.Bool("printCGNodes", false, "Print #cgnodes (before solve()).")
	_doTogether := flag.Bool("doTogether", false, "Do all main together from the same root in one pkg, linked by the root node.")
	_doParallel := flag.Bool("doParallel", false, "Do all mains in a pkg in parallel.")
	//test useage in race checker
	_doRaceReq := flag.Bool("doRaceReq", false, "Do all mains in a pkg sequential, but input is multiple mains.")

	flag.Parse()
	if *_main != "" {
		Main = *_main
	}
	if *_doLog {
		DoLog = true
	}
	if *_doDefault {
		DoDefault = true
	}
	if *_doComp {
		DoCompare = true
	}
	if *_time != "" {
		TimeLimit, _ = time.ParseDuration(*_time)
	}

	//my use
	if *_printCGNodes {
		PrintCGNodes = true
	}
	if *_doTogether {
		DoTogether = true
	}
	if *_doParallel {
		DoParallel = true
	}
	if *_doRaceReq {
		DoRaceReq = true
	}
}