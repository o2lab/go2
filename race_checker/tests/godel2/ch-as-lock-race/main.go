// +build ignore

package main

import "fmt"

func main() {
	var x int
	ch := make(chan int, 2)
	go f(&x, ch)
	ch <- 0
	x = 1
	<-ch
	ch <- 0
	fmt.Println("x is", x /* RACE Read */)
	<-ch
}

func f(x *int, ch chan int) {
	ch <- 0
	* /* RACE Write */ x = -1
	<-ch
}
