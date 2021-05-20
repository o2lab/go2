// +build ignore

package main

import "fmt"

func main() {
	var x int
	ch := make(chan int, 1)
	go f(&x, ch)
	ch <- 0
	x = 1
	<-ch
	ch <- 0
	fmt.Println("x is", x)
	<-ch
}

func f(x *int, ch chan int) {
	ch <- 0
	*x = -1
	<-ch
}
