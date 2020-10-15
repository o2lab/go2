package main

import "fmt"

var choice int

func main() {
	x := 1
	ch := make(chan bool)
	ch2 := make(chan bool)
	go func() {
		x = 2
		if choice == 0 {
			ch <- true
		} else {
			x = 3
		}
		x /* RACE Write */ = 4
	}()
	select {
	case <-ch:
		x /* RACE Write */ = 1
		fmt.Println(x)
	case t := <-ch2:
		if t {
			x = 2
		}
	}
}
