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
		x /* RACE Write */= 1
		wg.Done()
	}()
	go func() {
		fmt.Println(x/* RACE Read */)
	}()
	wg.Wait()
}
