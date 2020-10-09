package main

import (
	"fmt"
	"sync"
)

func main() {
	m := sync.Mutex{}
	x := 1
	y := 0
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		y /* RACE Write */ = 1 // write y (race condition)
		m.Lock()
		x = 2 // write x (protected)
		m.Unlock()
		wg.Done()
	}()
	go func() {
		fmt.Println("pass")
	}()
	m.Lock()
	a := x // read x (protected)
	m.Unlock()
	if a != 0 {
		fmt.Println("READ y", y /* RACE Read */ ) // read y (race condition)
	}
	wg.Wait()
}
