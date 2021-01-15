package main

import "sync"

type S struct {
	i int
}

func (s *S) read() int {
	return s.i /* RACE Read */
}

func (s *S) write(i int) {
	s.i /* RACE Write */ = i
}


func main() {
	var m sync.Mutex
	s := &S{
		i: 1,
	}
	go func() {
		m.Lock()
		s.write(12)
		m.Unlock()
	}()
	m.Lock()
	s.read()
	m.Unlock()
}
