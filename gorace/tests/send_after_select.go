package gorace_test

import "fmt"

func main() {
	ch1 := make(chan int)
	ch2 := make(chan int)
	x01 := 0
	select {
	case a := <-ch1:
		x01 = a
	case a := <-ch2:
		x01 = a + 1
	default:
		x01 = 20
		fmt.Println(x01)
	}
	go func() {
		x01 = 2
		ch2 <- 2
	}()
}
