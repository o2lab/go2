package main

import "fmt"

func main() {
	ch1 := make(chan string)
	ch2 := make(chan string)
	ch3 := make(chan string) // unbuffered channel

	snd1 := "1st msg"
	snd2 := "2nd msg"

	go func() {
		msg := <-ch2 // corresponding channel receive
		fmt.Println("received ", msg)
	}()


	select {
	case ch1 <- snd1:
		fmt.Println("received ", snd1)
	case ch2 <- snd2:
		fmt.Println("sent", snd2)
	case msg := <- ch3:
		fmt.Println("received ", msg)
	}
}