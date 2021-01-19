package main

import (
	"fmt"
	"sync"
)

var m1 sync.Mutex

// from goroutine 0
func main() {
	//var i int
	//go writeI(&i)
	//var _ = i
	//i = 1
	fmt.Println(getNumber())
}

func getNumber() int {
	var i22 int
	writeI := func() {
		i22 = 1
	}
	go func() {
		_ = i22
		m1.Lock()
		writeI()
		m1.Unlock()
	}()
	//var ch chan int
	//m.Lock()

	//m.Unlock()
	//k := <-ch
	m1.Lock()
	_ = i22
	m1.Unlock()
	return 0
}
