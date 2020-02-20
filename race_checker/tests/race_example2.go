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
	var i int
	writeI := func() {
		i = 1
	}
	go func() {
		_ = i
		m1.Lock()
		writeI()
		m1.Unlock()
	}()
	//var ch chan int
	//m.Lock()

	//m.Unlock()
	//k := <-ch
	m1.Lock()
	_ = i
	m1.Unlock()
	return 0
}
