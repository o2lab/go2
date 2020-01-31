package main

import "fmt"

var choice int

func main() {
	x := 1
	ch := make(chan bool)
	ch2 := make(chan bool)
	go func() {
		x = 2
		if choice == 1 {
			ch <- true
		} else {
			ch2 <- true
		}
	}()
	<-ch
	fmt.Println(x)
}
