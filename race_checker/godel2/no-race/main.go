// +build ignore

package main

import (
	"fmt"
)

func Writer(x *int) {
	*x++
}

func main() {
	var x, y int
	Writer(&x)
	Writer(&y)
	fmt.Println("x is", x)
	fmt.Println("y is", y)
}
