package main

import (
	"fmt"
	"time"
)

func main() {
	message1 := make(chan string)
	message2 := make(chan string)
	msg1 := "hi"
	msg2 := "hi again"
	go func() {
		time.Sleep(1*time.Second)
		message1 <- msg1
	}()
	go func() {
		time.Sleep(2*time.Second)
		message1 <- msg2
	}()
	for i := 0; i < 2; i++ {
		select {
		case a := <- message1:
			fmt.Println("sent message", a)
		case b := <- message2:
			fmt.Println("sent another", b)
		}
	}
}

