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
		x += 1
		mumu.Unlock()
	}()
	//commands here are racy
	mumu.Lock()//blocks until goroutine finishes
	//commands here are protected
	fmt.Println(x)
	mumu.Unlock()
}