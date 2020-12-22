package main

import "sync"

var m sync.Mutex

func main() {
	x := 1
	go g(&x)
	f(&x)
}

func f(p *int) {
	m.Lock()
	*p = 1
	*p = 3 + *p
	m.Unlock()
}

func g(p *int) {
	m.Lock()
	*p = 1
	m.Unlock()
}
