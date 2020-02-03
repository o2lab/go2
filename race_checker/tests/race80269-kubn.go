package main

import "fmt"

func main() {
	for i := 0; i < 100; i++ {
		mayRace()
	}
	showBB(1)
}

func mayRace() {
	i := 0
	ch1 := make(chan bool)
	ch2 := make(chan bool)
	go func() {
		i = 1 // racy write
		ch1 <- true
	}()
	go func() {
		ch2 <- true
	}()
	select {
	case <-ch1:
		fmt.Print("ch1 ")
	case <-ch2:
		fmt.Print("ch2 ")
	}
	fmt.Print(i, "\n") // racy read
}

func showBB(j int) int {
	i := j * 3
	go mayRace()
	i = 2*j + i
	return i
}
