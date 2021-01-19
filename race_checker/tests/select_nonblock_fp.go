package main

import "fmt"

func main() {
	ch1 := make(chan int)
	ch2 := make(chan int)
	x88 := 0
	go func() {
		x88 /* RACE Write */ = 1
		ch1 <- 1
	}()
	go func() {
		ch2 <- 3
	}()
	select {
	case a := <-ch1:
		x88 = a
	case a := <-ch2:
		x88 /* RACE Write */ = a + 1
		fmt.Println(x88)
	default:
		//x2 /* RACE Write */= 20
		//fmt.Println(x2)
	}
}
