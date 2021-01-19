package main

import "fmt"

func main() {
	ch1 := make(chan int)
	ch2 := make(chan int)
	ch3 := make(chan int)
	ch4 := make(chan int)
	x66 := 0
	go func() {
		x66 /* RACE Write */ = 1
		ch1 <- 1
	}()
	go func() {
		ch3 <- 4
	}()
	select {
	case a := <-ch1:
		x66 = a
	case a := <-ch2:
		x66 = a + 1
	default:
		x66 /* RACE Write */ = 20
		fmt.Println(x66)
	}
	select {
	case a := <-ch3:
		x66 = a + 10
	case a := <-ch4:
		x66 = a + 20
	}
}
