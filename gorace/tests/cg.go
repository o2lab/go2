package gorace_test

import "fmt"

type A struct {
	x int
	y int
}

func (a *A) SetX(x int) {
	a.x /* RACE Write */ = x
	fmt.Println(a.x)
}

func main() {
	a := A{
		2, 2,
	}
	go a.SetX(1)
	fmt.Println(a.x /* RACE Read */)
	s := []int{1, 2, 3, 4}
	writeSlice(s)
	fmt.Println(s)
}

func writeSlice(s []int) {
	s[0] = 10
}
