package main

import "fmt"

func main() {
	ch1 := make(chan int)
	ch2 := make(chan int)
	x := 0
	go func() {
		x /* RACE Write */= 1
		ch1 <- 1
	}()
	go func() {
		ch2 <- 3
	}()
	select {
	case a := <-ch1:
		x = a
	case a := <-ch2:
		x /* RACE Write */ = a + 1
		fmt.Println(x)
	default:
		//x /* RACE Write */= 20
		//fmt.Println(x)
	}
}