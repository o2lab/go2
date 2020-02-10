package main

import "fmt"

var x int = 1

func main() {
	ch := make(chan bool)
	func() {
		go func() {
			x = 1
			ch <- true
		}()
		<-ch
	}()
	fmt.Println(x) // no race
}
