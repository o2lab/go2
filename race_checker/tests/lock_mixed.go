package main

import (
	"sync"
)

// The mumu.Lock() should wait untill mumu.RUnlock() is invoked
func main() {
	ch1 := make(chan int)
	mumu := sync.RWMutex{}
	x := 0
	go func() {
		mumu.RLock()
		ch1 <- x
		mumu.RUnlock()
	}()

	go func() {
		mumu.Lock()
		x += 1
		mumu.Unlock()
	}()

}

