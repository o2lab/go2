package main

import "sync/atomic"

type stat struct {
	x uint64
}

func main() {
	s := stat{}
	go func() {
		a := atomic.AddUint64(&s.x, 1)
		_ = a
	}()
	b := atomic.AddUint64(&s.x, 2)
	_ = b
}
