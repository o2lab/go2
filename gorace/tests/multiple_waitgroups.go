package main

import (
	"fmt"
	"sync"
)

func main() {
	wg := sync.WaitGroup{}
	otherWG := sync.WaitGroup{}
	x8 := 1
	// serialize accesses between "wg" and "otherWG"
	wg.Add(1)
	otherWG.Add(1)
	go func() {
		x8 = 1
		wg.Done()
	}()
	wg.Wait()
	go func() {
		fmt.Println(x8)
		otherWG.Done()
	}()
	otherWG.Wait()
	x8 = 2
	fmt.Println(x8)
}
