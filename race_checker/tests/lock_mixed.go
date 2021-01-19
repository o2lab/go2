package main

import (
	"sync"
)

// The mumu.Lock() should wait untill mumu.RUnlock() is invoked
func main() {
	ch1 := make(chan int)
	mumu := sync.RWMutex{}
	x4 := 0
	go func() {
		mumu.RLock()
		ch1 <- x4
		mumu.RUnlock()
	}()

	go func() {
		mumu.Lock()
		x4 += 1
		mumu.Unlock()
	}()

}
