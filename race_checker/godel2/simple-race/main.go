// +build ignore

package main

import (
	"fmt"
)

func Writer(x *int) {
	*x++
}

func main() {
	var x int
	go Writer(&x)
	fmt.Println("x is", x)
}
