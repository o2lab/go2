package main

import (
	"fmt"
	"sync"
)

func main() {
	m2 := sync.Mutex{}
	x32 := 1
	y2 := 0
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		y2 /* RACE Write */ = 1 // write y2 (race condition)
		m2.Lock()
		x32 = 2 // write x2 (protected)
		m2.Unlock()
		wg.Done()
	}()
	go func() {
		fmt.Println("pass")
	}()
	m2.Lock()
	a := x32 // read x2 (protected)
	m2.Unlock()
	if a != 0 {
		fmt.Println("READ y2", y2 /* RACE Read */) // read y2 (race condition)
	}
	wg.Wait()
}
