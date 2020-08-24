// +build ignore

package main

import (
	"fmt"
	"time"
)

func Fork(fork *int, ch chan int) {
	for {
		*fork = 1
		<-ch
		ch <- 0
	}
}

func phil(fork1, fork2 *int, ch1, ch2 chan int, id int) {
	for {
		select {
		case ch1 <- *fork1:
			select {
			case ch2 <- *fork2:
				fmt.Printf("phil %d got both fork\n", id)
				<-ch1
				<-ch2
			default:
				<-ch1
			}
		case ch2 <- *fork2:
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
	var fork1, fork2, fork3 int
	ch1 := make(chan int)
	ch2 := make(chan int)
	ch3 := make(chan int)
	go phil(&fork1, &fork2, ch1, ch2, 0)
	go phil(&fork2, &fork3, ch2, ch3, 1)
	go phil(&fork3, &fork1, ch3, ch1, 2)
	go Fork(&fork1, ch1)
	go Fork(&fork2, ch2)
	go Fork(&fork3, ch3)
	time.Sleep(10*time.Second)
}
