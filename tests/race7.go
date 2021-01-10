package main

func f(p *int) {
	go func() {
		*p = 1
	}()
}

func g(p *int) {
	*p = 0
}

func main() {
	x := 1
	f(&x)
	g(&x)
}
