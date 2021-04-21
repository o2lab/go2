package main

import (
	"fmt"
	"k8s.io/apimachinery/pkg/util/rand"
	"sync"
)

func goFn(ijk int, wg sync.WaitGroup) {
	ijk++ // event A
	wg.Done()
}

func workerFn(ijk int) int {
	var wg sync.WaitGroup
	if rand.Intn(100) == 0 {
		wg.Add(1)
		go goFn(ijk, wg)
	} else {
		ijk++
	}
	wg.Wait()
	return ijk
}

func main() {
	ijk := 0
	NEWijk := workerFn(ijk)
	fmt.Println(NEWijk)

	// check can run in parallel, check creation site of each goroutine
	// for event A, creation at line 8
	// for event B, creation at line 12
}