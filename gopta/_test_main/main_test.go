package _test_main

import (
	"fmt"
	"github.com/april1989/origin-go-tools/go/myutil"
	"github.com/april1989/origin-go-tools/go/myutil/flags"
	"testing"
	"time"
)


func TestProfileMain(t *testing.T) {
	mains, tests := myutil.InitialMain()
	if mains == nil {
		return
	}

	//officially start
	if flags.DoSeq { //AnalyzeMultiMains
		myutil.DoSeq(mains, tests)
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

	fmt.Println("\n\nBASELINE All Done  -- PTA/CG Build. ")

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