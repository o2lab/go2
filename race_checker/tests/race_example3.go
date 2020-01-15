package main

type S struct {
	i int
}

func (s *S) read() int {
	return s.i
}

func (s *S) write(i int) {
	s.i = i
}

func main() {
	s := &S{
		i: 1,
	}
	go func() {
		s.write(12)
	}()
	s.read()
}

