package main

import "sync"

var y int

func a() {
	y++
	b()
}

func b() {
	y--
}

func main() {
	c := make(chan bool, 1)
	var mu sync.Mutex
	go func() {
		mu = sync.Mutex{}
		c <- true
	}()
	mu.Lock()
	<-c
}