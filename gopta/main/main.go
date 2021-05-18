package main

import (
	"fmt"
	"github.com/april1989/origin-go-tools/go/myutil"
	"github.com/april1989/origin-go-tools/go/myutil/flags"
	"time"
)

/*
non-stop mains:
/tidb/tidb-server

cockroach: 'go mod vendor' problem -> remove dir /cockroach/vendor
hugo: "package io/fs is not in GOROOT" on Go 1.15 -> need go 1.16
 */

func main() {
	mains := myutil.InitialMain()
	if mains == nil {
		return
	}

	//officially start
	if flags.DoSeq { //AnalyzeMultiMains
		myutil.DoSeq(mains)
		return
	}else{
		//DoSameRoot and DoParallel cannot both be true
		if flags.DoParallel {
			myutil.DoParallel(mains) //bz: --> fatal error: concurrent map writes!! discarded
		} else {
			if flags.DoSameRoot {
				myutil.DoSameRoot(mains)
			} else { //default
				myutil.DoEach(mains)
			}
		}
	}

	fmt.Println("\n\nBASELINE All Done  -- PTA/CG Build. \n")

	if flags.DoCompare || flags.DoDefault {
		fmt.Println("Default Algo:")
		fmt.Println("Total: ", (time.Duration(myutil.DefaultElapsed)*time.Millisecond).String()+".")
		fmt.Println("Max: ", myutil.DefaultMaxTime.String()+".")
		fmt.Println("Min: ", myutil.DefaultMinTime.String()+".")
		fmt.Println("Avg: ", float32(myutil.DefaultElapsed)/float32(len(mains))/float32(1000), "s.")
	}

	if flags.DoDefault {
		return
	}

	fmt.Println("My Algo:")
	fmt.Println("Total: ", (time.Duration(myutil.MyElapsed)*time.Millisecond).String()+".")
	fmt.Println("Max: ", myutil.MyMaxTime.String()+".")
	fmt.Println("Min: ", myutil.MyMinTime.String()+".")
	fmt.Println("Avg: ", float32(myutil.MyElapsed)/float32(len(mains))/float32(1000), "s.")
}
