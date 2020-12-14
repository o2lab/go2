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
	a := <-messages
	x = 10
	fmt.Println(a)
	fmt.Println(x)

}
