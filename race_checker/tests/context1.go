package main

import "fmt"

var x = 1
var ch = make(chan bool)

func main() {
	func() {
		go func() {
			x = 1
			ch <- true
		}()
		foobar()
	}()
	fmt.Println(x) // no race, synced through chan recv in foobar()
}

func bar() {
	<-ch
}

func foobar() {
	bar()
}
