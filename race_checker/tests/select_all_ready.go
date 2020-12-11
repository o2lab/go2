package main

import "fmt"

func main() {
	ch1 := make(chan int)
	ch2 := make(chan int)
	x := 0
	y := 0
	go func() {
		x /* RACE Write */ = 1
		ch1 <- 1
	}()
	go func() {
		y /* RACE Write */ = 2
		ch2 <- 2
	}()
	select {
	case a := <-ch1:
		y /* RACE Write */ = a + 200
	case a := <-ch2:
		x /* RACE Write */ = a + 100
	}
	fmt.Println(x, y)
}