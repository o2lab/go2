// +build ignore

package gorace_test

import (
	"fmt"
)

func Writer(x *int) {
	* /* RACE Write */ x++
}

func main() {
	var x int
	go Writer(&x)
	fmt.Println("x is", x /* RACE Read */)
}
