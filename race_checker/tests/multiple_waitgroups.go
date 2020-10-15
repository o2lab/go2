package main

import (
	"fmt"
	"sync"
)

func main() {
	wg := sync.WaitGroup{}
	otherWG := sync.WaitGroup{}
	x := 1
	// serialize accesses between "wg" and "otherWG"
	wg.Add(1)
	otherWG.Add(1)
	go func() {
		x = 1
		wg.Done()
	}()
	wg.Wait()
	go func() {
		fmt.Println(x)
		otherWG.Done()
	}()
	otherWG.Wait()
	x = 2
	fmt.Println(x)
}
