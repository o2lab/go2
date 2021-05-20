package gorace_test

import "fmt"

var x2 = 1
var ch = make(chan bool)
var y = false

func main() {
	func() {
		go func() {
			x2 = 1
			ch <- true
		}()
		funcOne()
	}()
	fmt.Println(y) // no race, synced through chan recv in funcTwo()
}

func funcTwo() {
	y = <-ch
}

func funcOne() {
	funcTwo()
}
