package main

import "fmt"

func main() {
	ch1 := make(chan int)
	ch2 := make(chan int)
	x7 := 0
	go func() {
		x7 /* RACE Write */ /* RACE Write */ = 2
		ch2 <- 2
	}()
	go func() {
		x7 /* RACE Write */ /* RACE Write */ = 4
		ch2 <- 1
	}()
	select {
	case a := <-ch1:
		x7 = a
	case a := <-ch2:
		x7 = a + 1
	default:
		x7 /* RACE Write */ /* RACE Write */ = 20
	}
	fmt.Println(x7)
}
