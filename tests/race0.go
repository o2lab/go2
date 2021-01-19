package main

import "runtime"

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
	c := make(chan int, 10)
	c <- 1
	go func() {
		v = 1
		<-c
	}()
	for len(c) != 0 {
		runtime.Gosched()
	}
	v = 2
}
