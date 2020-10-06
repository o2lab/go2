package main

import (
	"fmt"
	"sync"
)

func main() {
	wg := sync.WaitGroup{}
	x := 1
	wg.Add(1)
	go func() {
		x = 1
		wg.Done()
	}()
	wg.Wait()
	fmt.Println(x)
}
