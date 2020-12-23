package main

import "sync"

type S struct {
	i int
}

func (s *S) read() int {
	return s.i /* RACE Read */
}

func (s *S) write(i int) {
	_ = s.i
	if s.i == 1 {
		return
	}
	if s.i == 1 {
		s.i /* RACE Write */ = i
	} else if s.i == 2 {
		s.i = 2
		if s == nil {
			s.i = 3
			return
		}
		s.i = 5
	}
}

var m sync.Mutex

func main() {
	s := &S{
		i: 1,
	}
	x := 1
	go func() {
		s.write(12)
		x = 2
	}()
	_ = x
	m.Lock()
	s.read()
	m.Unlock()
}
