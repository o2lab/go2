package gorace_test

import "fmt"

var choice int

func main() {
	x9 := 1
	ch0 := make(chan bool)
	ch2 := make(chan bool)
	go func() {
		x9 = 2
		if choice == 0 {
			ch0 <- true
		} else {
			x9 = 3
		}
		x9 /* RACE Write */ = 4
	}()
	select {
	case <-ch0:
		x9 /* RACE Write */ = 1
		fmt.Println(x9)
	case t := <-ch2:
		if t {
			x9 = 2
		}
	}
}
