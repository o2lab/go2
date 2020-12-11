package main

import (
	"fmt"
)

func main() {
	ch1 := make(chan int)
	ch2 := make(chan int)
	x := 0
	go func() {
		x /* RACE Write */ = 1
		ch1 <- 1
		x /* RACE Write */ = 2
	}()
	//time.Sleep(2*time.Second)
	select {
	case a := <-ch1:
		x /* RACE Write */ = a
		fmt.Println(x)
	case a := <-ch2:
		x = a + 1
	default:
		x /* RACE Write */= 20
		fmt.Println(x)
	}
}
