package main

import (
	"fmt"
	"time"
)

func main() {
	ch1 := make(chan int)
	ch2 := make(chan int)
	x77 := 0
	y77 := 0
	go func() {
		x77 /* RACE Write */ = 1
		ch1 <- 1
		y77 /* RACE Write */ = 2
	}()
	time.Sleep(2 * time.Second)
	select {
	case a := <-ch1:
		y77 /* RACE Write */ = a
		fmt.Println(y77)
	case a := <-ch2:
		x77 = a + 1
	default:
		x77 /* RACE Write */ = 20
		fmt.Println(x77)
	}
}
