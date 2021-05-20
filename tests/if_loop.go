package main

import (
	"fmt"
	"math/rand"
)

type AnalysisRunner struct {
	check 	int
}
func (runner *AnalysisRunner) Run() {
	if rand.Intn(100) == 0 {
		fmt.Println("blah")
	} else if  rand.Intn(100) == 1 {
		fmt.Println("blah1")
	} else {
		runner.check++
	}
	if rand.Intn(100) == 0 {
		fmt.Println("blah2")
	}

	for _, i10 := range []int{1, 3, 5, 7, 9} {
		go func(aNum int) {
			checkX := 0
			checkX = runner.check + aNum
			fmt.Println(checkX)
		}(i10)
	}

}
func main() {
	runner := &AnalysisRunner{
		check: 0,
	}
	runner.Run()
}
