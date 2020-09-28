// +build ignore

package main

import (
	"fmt"
	"time"
)

func Fork(fork *int, ch chan int) {
	for {
		* /* RACE Write */ /* RACE Write */ fork = 1
		<-ch
		ch <- 0
	}
}

func phil(fork1, fork2 *int, ch1, ch2 chan int, id int) {
	for {
		select {
		case ch1 <- * /* RACE Read */ fork1:
			select {
			case ch2 <- *fork2:
				fmt.Printf("phil %d got both fork\n", id)
				<-ch1
				<-ch2
			default:
				<-ch1
			}
		case ch2 <- * /* RACE Read */ fork2:
			select {
			case ch1 <- *fork1:
				fmt.Printf("phil %d got both fork\n", id)
				<-ch1
				<-ch2
			default:
				<-ch2
			}
		}
	}
}

func main() {
	var fork1, fork2, fork3, fork4, fork5 int
	ch1 := make(chan int)
	ch2 := make(chan int)
	ch3 := make(chan int)
	ch4 := make(chan int)
	ch5 := make(chan int)
	go phil(&fork1, &fork2, ch1, ch2, 0)
	go phil(&fork2, &fork3, ch2, ch3, 1)
	go phil(&fork3, &fork4, ch3, ch4, 2)
	go phil(&fork4, &fork5, ch4, ch5, 3)
	go phil(&fork5, &fork1, ch5, ch1, 4)
	go Fork(&fork1, ch1)
	go Fork(&fork2, ch2)
	go Fork(&fork3, ch3)
	go Fork(&fork4, ch4)
	go Fork(&fork5, ch5)
	time.Sleep(10 * time.Second)
}
