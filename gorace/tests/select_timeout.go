package gorace_test

import (
	"fmt"
	"time"
)

func main() {
	ch1 := make(chan string)
	ch2 := make(chan string)
	go func() {
		time.Sleep(2*time.Second)
		ch1 <- "just in time - 1st attempt"
	}()
	select {
	case check := <-ch1:
		fmt.Println(check)
	case <-time.After(1*time.Second):
		fmt.Println("timeout - 1st attempt")
	}

	go func() {
		time.Sleep(3*time.Second)
		ch2 <- "just in time - 2nd attempt"
	}()
	select {
	case check := <-ch2:
		fmt.Println(check)
	case <-time.After(4*time.Second):
		fmt.Println("timeout - 2nd attempt")
	}
}
