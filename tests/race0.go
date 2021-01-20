package main

import "sync"

type P struct {
	x, y int
}

type S struct {
	s1, s2 P
}

type P2 P
type S2 S
type X2 X

type X struct {
	V [4]P
	m sync.Mutex
}

func main() {
	x := X{}
	a := 0
	_ = a
	go func() {
		x.m.Lock()
		defer x.m.Unlock()
		a = 1
	}()
	go func() {
		x.m.Lock()
		defer x.m.Unlock()
		a = 1
	}()
}
