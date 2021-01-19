package main

import "fmt"

var choice int

func main() {
	x9 := 1
	ch := make(chan bool)
	ch2 := make(chan bool)
	go func() {
		x9 = 2
		if choice == 0 {
			ch <- true
		} else {
			x9 = 3
		}
		x9 /* RACE Write */ = 4
	}()
	select {
	case <-ch:
		x9 /* RACE Write */ = 1
		fmt.Println(x9)
	case t := <-ch2:
		if t {
			x9 = 2
		}
	}
}
