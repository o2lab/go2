// +build ignore

package main

import (
	"fmt"
	"time"
)

func Fork(fork *int, ch chan int) {
	for {
		*fork = 1
		ch <- 0
		<-ch
	}
}

func phil(fork1, fork2 *int, ch1, ch2 chan int, id int) {
	for {
		select {
		case <-ch1:
			select {
			case <-ch2:
				fmt.Printf("phil %d got both fork\n", id)
				ch1 <- *fork1
				ch2 <- *fork2
			default:
				ch1 <- *fork1
			}
		case <-ch2:
			select {
			case <-ch1:
				fmt.Printf("phil %d got both fork\n", id)
				ch2 <- *fork2
				ch1 <- *fork1
			default:
				ch2 <- *fork2
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
	time.Sleep(10*time.Second)
}
