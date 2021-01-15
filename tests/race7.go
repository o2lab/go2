package main

import "sync"

func main() {
	var m sync.Mutex
	x := 1
	b := 1
	go func(a int) {
		m.Lock()
		if a == 0 {
			m.Unlock()
			return
		}
		x = 1
		m.Unlock()
	}(1)
	m.Lock()
	if b == 1 {
		m.Unlock()
		return
	}
	b = x
	m.Unlock()
}
