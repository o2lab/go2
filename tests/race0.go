package main

import "time"

var y int

func a() {
	y++
	b()
}

func b() {
	y--
}

func main() {
	c := make(chan *int, 1)
	c <- nil
	go func() {
		i := 42
		c <- &i
	}()
	time.Sleep(10 * time.Millisecond)
	<-c
	select {
	case p := <-c:
		if *p != 42 {

		}
	case <-make(chan int):
	}
}
