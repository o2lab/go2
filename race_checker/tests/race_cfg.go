package main

import "fmt"

var choice int

func main() {
	x := 1
	ch := make(chan bool)
	ch2 := make(chan bool)
	go func() {
		x = 2 /* RACE Write */
		if choice == 0 {
			ch <- true
		} else {
			x = 3
		}
		x = 4 /* RACE Write */
	}()
	select {
	case <-ch:
		x = 1 /* RACE Write */
	case t := <-ch2:
		if t {
			x = 2 /* RACE Write */
		}
	}
	fmt.Println(x) /* RACE Read */
}
