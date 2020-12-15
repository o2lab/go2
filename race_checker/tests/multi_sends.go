package main

import "fmt"

func main() {
	ch1 := make(chan int)
	ch2 := make(chan int)
	x := 0
	go func() {
		x = 2
		ch2 <- 2
	}()
	go func() {
		x = 4
		ch2 <- 1
	}()
	select {
	case a := <-ch1:
		x = a
	case a := <-ch2:
		x = a + 1
	default:
		x = 20
	}
	fmt.Println(x)
}