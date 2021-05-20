package gorace_test

import "fmt"

func main() {
	ch1 := make(chan int)
	ch2 := make(chan int)
	ch3 := make(chan int)
	x44 := 0
	go func() {
		x44 = 1
		ch1 <- 1
	}()
	select {
	case a := <-ch1:
		x44 = a
	case a := <-ch2:
		x44 = a + 1
	case <-ch3:
		x44 = 10
		select {
		case a := <-ch1:
			x44 = a
		case a := <-ch2:
			x44 = a + 1
		}
	}

	fmt.Println(x44)
}
