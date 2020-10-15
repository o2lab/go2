package main

import "fmt"

func main() {
	messages := make(chan string)
	msg := "hi"
	x := 0
	go func() {
		x = 2
		messages <- msg
	}()
	select {
	case messages <- msg:
		x = 10
		fmt.Println("sent message", msg)
	default:
		fmt.Println("no message sent")
	}
	fmt.Println(x)

}

