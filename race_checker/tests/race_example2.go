package main

import "fmt"

// from goroutine 0
func main() {
	//var i int
	//go writeI(&i)
	//var _ = i
	//i = 1
	fmt.Println(getNumber())
}

func getNumber() int {
	var i int
	go writeI(&i)
	var ch chan int
	if i == 0 {
		return 0
	} else {
		ch = make(chan int)
		ch <- i // blocking
	}
	k := <-ch
	return k
}

// from goroutine 1
func writeI(j *int) {
	*j = 5
}
