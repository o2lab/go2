package main

var y int

func a() {
	y++
	b()
}

func b() {
	y--
}

func main() {
	v := 0
	_ = v
	c := make(chan int)
	go func() {
		v = 1
		c <- 0
	}()
	<-c
	v = 2
}