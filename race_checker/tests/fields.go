// Check if we can distinguish accesses to different fields
package main

type T struct {
	a, b int
}

func main() {
	s := &T{
		a: 1,
		b: 2,
	}
	go func() {
		s.a /* RACE Write */ = 1
	}()
	s.b /* RACE Write */ = 2
}
