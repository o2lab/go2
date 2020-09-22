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
		y = 1
		m.Lock()
		x = 2
		m.Unlock()
		wg.Done()
	}()
	go func() {
		fmt.Println("pass")
	}()
	m.Lock()
	a := x
	m.Unlock()
	if a == 2 {
		fmt.Println("READ y", y)
	}
	wg.Wait()
	//fmt.Println(y)
}
