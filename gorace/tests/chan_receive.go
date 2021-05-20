package gorace_test

import "fmt"

func main() {
	messages := make(chan string)
	msg := "hi"
	x22 := 0
	go func() {
		x22 = 2
		messages <- msg
	}()
	a := <-messages
	x22 = 10
	fmt.Println(a)
	fmt.Println(x22)

}
