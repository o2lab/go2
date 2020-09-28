// Check if we can distinguish accesses to different fields
package main

type T struct {
	a, b int
}

func main() {
	s := T{
		a: 1,
		b: 2,
	}
	ch := make(chan struct{})
	go func() {
		s.a = 1
		ch <- struct{}{}
	}()
	s.b = 2
	<-ch
}
