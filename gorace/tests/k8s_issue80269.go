package gorace_test

import "fmt"

func main() {
	for i2 := 0; i2 < 2; i2++ { // modified for simplification
		mayRace()
	}
	showBB(1)
}

func mayRace() {
	i2 := 0
	ch1 := make(chan bool)
	ch2 := make(chan bool)
	go func() {
		i2 /* RACE Write */ /* RACE Write */ = 1
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
	fmt.Print(i2 /* RACE Read */ /* RACE Read */ , "\n")
}

func showBB(j int) int {
	i2 := j * 3
	go mayRace()
	i2 = 2*j + i2
	return i2
}
