package main

import "fmt"

func main() {
	ch1 := make(chan int)
	ch2 := make(chan int)
	ch3 := make(chan int)
	x := 0
	go func() {
		x = 1 /* RACE Write */
		ch1 <- 1
	}()
	select {
	case a := <-ch1:
		x = a
	case a := <-ch2:
		x = a + 1 /* RACE Write */
	case <-ch3:
		x = 10
		select {
		case a := <-ch1:
			x = a
		case a := <-ch2:
			x = a + 1 /* RACE Write */
		}
	}

	fmt.Println(x)
}
