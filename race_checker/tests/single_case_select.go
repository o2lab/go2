package main

import "fmt"

func main() {
	messages := make(chan string)
	msg := "hi"
	x := 0
	go func() {
		x = 2
		//messages <- msg
	}()
	select {
	case a := <- messages:
		fmt.Println(a)
		x = 10
		fmt.Println("sent message", msg)
	}
	fmt.Println(x)

}

