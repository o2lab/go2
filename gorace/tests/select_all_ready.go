package gorace_test

import "fmt"

func main() {
	ch1 := make(chan int)
	ch2 := make(chan int)
	x55 := 0
	y55 := 0
	go func() {
		x55 /* RACE Write */ = 1
		ch1 <- 1
	}()
	go func() {
		y55 /* RACE Write */ = 2
		ch2 <- 2
	}()
	select {
	case a := <-ch1:
		y55 /* RACE Write */ = a + 200
	case a := <-ch2:
		x55 /* RACE Write */ = a + 100
	}
	fmt.Println(x55, y55)
}
