package main

import "sync"

func main() {
	x := 0
	var m sync.Mutex
	go func() {
		m.Lock()
		x = 1
		m.Unlock()
	}()
	m.Lock()
	_ = x
	m.Unlock()
}
