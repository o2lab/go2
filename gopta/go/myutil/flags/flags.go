package flags

import (
	"flag"
	"time"
)

//user
var DoLog = false
var Main = ""          //bz: run for a specific main in this pkg; start from 0
var DoDefault = false  //bz: only Do default
var DoCompare = false  //bz: this has a super long time
var DoLevel = 0        //bz: set the analysis scope to level ? default = 0
var DoCallback = false //bz: simplify callback fn + preSolve()
var DoCollapse = false //bz: collapse the lib function with its callback, no matter what are the context of caller of lib func -> DoCallback must be true
var DoTests = false    //bz: treat a test as a main to analyze
var DoCoverage = false //bz: compute (#analyzed fn/#total fn) in a program within the scope

var PTSLimit int   //bz: limit the size of pts; if excess, skip its solving
var DoDiff = false //bz: compute the diff functions when turn on/off ptsLimit

var TimeLimit time.Duration //bz: time limit set by users, unit: ?h?m?s

//my use
var PrintCGNodes = false  //bz: print #cgnodes (before solve())
var DoDetail = false      //bz: print out all data from countReachUnreachXXX
var DoCommonPart = false  //bz: do compute common path
var DoPerformance = false //bz: print out all statistics (time, number)
var DoPrintInfo = false   //bz: whether we print out the details of config and other process info

//different run scenario
var DoSameRoot = false //bz: do all main in a pkg together from the same root -> all mains linked by the root node
var DoParallel = false //bz: do all mains in a pkg in parallel, do each main by itself by parallel
var DoSeq = false      //bz: do all mains in a pkg sequential, but input is multiple mains (test useage in race checker)

//bz: analyze all flags from input
func ParseFlags() {
	//user
	_main := flag.String("main", "", "Run for a specific main in this pkg.")
	_doLog := flag.Bool("doLog", false, "Do log. ")
	_doDefault := flag.Bool("doDefault", false, "Do default algo only. ")
	_doComp := flag.Bool("doCompare", false, "Do compare with default pta. ")
	_time := flag.String("timeLimit", "", "Set time limit to ?h?m?s or ?m?s or ?s, e.g. 1h15m30.918273645s. ")
	_doLevel := flag.Int("doLevel", -1, "Set the analysis scope to level = ? .")
	_doCB := flag.Bool("doCallback", false, "Use simplified and synthetic callback fn + preSolve(). ")
	_doCollapse := flag.Bool("doCollapse", false, "Collapse the context of lib function which has callbacks. ")
	_doTests := flag.Bool("doTests", false, "Treat a test as a main to analyze. ")
	_pts := flag.Int("ptsLimit", 0, "Set a number to limit the size of pts during the solver, e.g. 999. ")

	//my use
	_printCGNodes := flag.Bool("printCGNodes", false, "Print #cgnodes (before solve()).")
	_doSameRoot := flag.Bool("doSameRoot", false, "Do all main together from the same root in one pkg, linked by the root node.")
	_doParallel := flag.Bool("doParallel", false, "Do all mains in a pkg in parallel, but input is multiple mains.")
	_doCoverage := flag.Bool("doCoverage", false, "Compute (#analyzed fn/#total fn) in a program")
	_doPrintConfig := flag.Bool("doPrintInfo", false, "Do we print out the details of config and other process info?")

	//test useage in race checker -> main usage
	_doSeq := flag.Bool("doSeq", false, "Do all mains in a pkg sequential, but input is multiple mains.")

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
	if *_doLevel != -1 {
		DoLevel = *_doLevel
	}
	if *_doCB {
		DoCallback = true
		DoLevel = 1
	}
	if *_doCollapse {
		DoCollapse = true
		DoCallback = true //prerequisite of DoCollapse: must be
	}
	if *_doTests {
		DoTests = true
	}
	if *_doCoverage {
		DoCoverage = true
	}
	if *_pts != 0 {
		PTSLimit = *_pts
		//DoDiff = true
	}

	//my use
	if *_printCGNodes {
		PrintCGNodes = true
	}
	if *_doSameRoot {
		DoSameRoot = true
	}
	if *_doParallel {
		DoParallel = true
	}
	if *_doPrintConfig {
		DoPrintInfo = true
	}

	//test useage in race checker
	if *_doSeq {
		DoSeq = true
	}
}
