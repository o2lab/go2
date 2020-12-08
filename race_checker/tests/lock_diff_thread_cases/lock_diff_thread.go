package main

import (
	"fmt"
	"sync"
)

func main() {
	mumu := sync.RWMutex{}
	x := 0
	mumu.Lock()
	go func() {
		x += 1
		mumu.Unlock()
	}()+-

	mumu.Lock() // block until unlock?
	go func() {
		x /* RACE Write */ += 1//current tool gives a false negative for the case
		mumu.Unlock()
	}()

	fmt.Println(x /* RACE Read */)
}