package main

import "fmt"

func main() {
	messages := make(chan string)
	msg := "hi"
	x02 := 0
	go func() {
		x02 = 2
		messages <- msg
	}()
	select {
	case a := <-messages:
		fmt.Println(a)
		x02 = 10
		fmt.Println("sent message", msg)
	}
	fmt.Println(x02)

}
