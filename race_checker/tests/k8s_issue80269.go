package main

import "fmt"

func main() {
	for i := 0; i < 2; i++ { // modified for simplification
		mayRace()
	}
	showBB(1)
}

func mayRace() {
	i := 0
	ch1 := make(chan bool)
	ch2 := make(chan bool)
	go func() {
		i /* RACE Write */ /* RACE Write */ = 1
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
	fmt.Print(i /* RACE Read */ /* RACE Read */, "\n")
}

func showBB(j int) int {
	i := j * 3
	go mayRace()
	i = 2*j + i
	return i
}
