package main

import "fmt"

func main() {
	ch1 := make(chan int)
	//ch4 := make(chan int)
	x := 0
	go func() {
		x = 1 /* RACE Write */
		ch1 <- 1
	}()
	select {
	case a := <-ch1:
		x = a
	default:
		x = 20 /* RACE Write */
		fmt.Println(x)
	}

}
