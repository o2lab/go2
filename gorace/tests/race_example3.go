package gorace_test

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
	s := &S{
		i: 1,
	}
	go func() {
		s.write(12)
	}()
	s.read()
}
