package main

import "sync"

type Shape interface {
	Area() int
	Reset()
}

type Square struct {
	a int
}

func (s *Square) Area() int {
	return s.a * s.a
}

func (s *Square) Reset() {
	s.a = 0
}

type Rectangle struct {
	a, b int
}

func (r *Rectangle) Area() int {
	return r.a * r.b
}

func (r *Rectangle) Reset() {
	r.a = 0
	r.b = 0
}

func reset(s Shape, times int)  {
	if times == 0 {
		s.Reset()
		return
	}
	reset(s, times - 1)
}

func main() {
	var r Rectangle
	var m sync.Mutex
	ch := make(chan struct{})
	go func() {
		m.Lock()
		reset(&r, 2)
		m.Unlock()
		ch <- struct{}{}
	}()
	m.Lock()
	r.Area()
	m.Unlock()
	<-ch
}
