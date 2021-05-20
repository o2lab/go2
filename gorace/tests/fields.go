// Check if we can distinguish accesses to different fields
package gorace_test

type T struct {
	a, b int
}

func main() {
	s := T{
		a: 1,
		b: 2,
	}
	c := make(chan struct{})
	go func() {
		s.a = 1
		c <- struct{}{}
	}()
	s.b = 2
	<-c
}
