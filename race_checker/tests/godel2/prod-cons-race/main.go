// +build ignore

package main

import (
	"sync"
	"time"
)

func Producer(mut *sync.Mutex, x *int, end chan int) {
	for i:=0; i<5; {
		if *x == 0 {	// Read not fenced, may not see other Producers
			i++
			*x = i	// Write not fenced, may clash with other Producers
		}
	}
	close(end)
}

func Consumer(mut *sync.Mutex, x *int) {
	for {
		mut.Lock()
		if *x != 0 {	// Correct fencing here, but may clash with Producers that are not fenced.
			print(*x)
			*x = 0
		}
		mut.Unlock()
	}
}

func main() {
	m := new(sync.Mutex)
	var x int
	end1 := make(chan int)
	end2 := make(chan int)
	go Producer(m, &x, end1)
	go Producer(m, &x, end2)
	go Consumer(m, &x)
	<-end1
	<-end2
	time.Sleep(200*time.Millisecond)
}
