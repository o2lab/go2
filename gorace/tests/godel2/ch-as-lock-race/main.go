// +build ignore

package gorace_test

import "fmt"

func main() {
	var x int
	ch := make(chan int, 2)
	go f(&x, ch)
	ch <- 0
	x /* RACE Write */ = 1
	<-ch
	ch <- 0
	fmt.Println("x is", x)
	<-ch
}

func f(x *int, ch chan int) {
	ch <- 0
	* /* RACE Write */ x = -1
	<-ch
}
