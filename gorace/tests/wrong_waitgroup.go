package main

import (
	"fmt"
	"sync"
)

func main() {
	wg := sync.WaitGroup{}
	x04 := 1
	wg.Add(1)
	go func() {
		x04 /* RACE Write */ = 1
		wg.Done()
	}()
	go func() {
		fmt.Println(x04 /* RACE Read */)
	}()
	wg.Wait()
}
