package main

func main() {
	x := 1
	go g(&x)
	f(&x)
}

func f(p *int) {
	*p = 1
}

func g(p *int) {
	*p = 1
}
