package main

import (
	"fmt"
	"sync"
)

func main() {
	mumu := sync.RWMutex{}
	x := 0
	mumu.Lock()
	go func() {
		x += 1
		mumu.Unlock()
	}()

	mumu.Lock() // block until unlock?
	go func() {
		mumu.Lock() // bug in code
		x += 1
		fmt.Println("program will never reach here")
		mumu.Unlock()
	}()

	fmt.Println(x)
}