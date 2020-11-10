package main

import "fmt"

func main() {
	ch1 := make(chan int)
	ch2 := make(chan int)
	ch3 := make(chan int)
	ch4 := make(chan int)
	x := 0
	go func() {
		x /* RACE Write */= 1
		ch1 <- 1
	}()
	go func() {
		ch3 <- 4
	}()
	select {
	case a := <-ch1:
		x = a
	case a := <-ch2:
		x = a + 1
	default:
		x /* RACE Write */= 20
		fmt.Println(x)
	}
	select {
	case a := <-ch3:
		x = a + 10
	case a := <-ch4:
		x = a + 20
	}
}