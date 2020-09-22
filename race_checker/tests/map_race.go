package main

import "fmt"

var m map[int]int = make(map[int]int)

func main() {
	var s []int
	s = append(s, 1, 2, 3)
	fmt.Println(s[0])
	m[1] = 2
	m[2] = 3
	go func() {
		m[3] /* RACE Write */ = 3
	}()
	go func() {
		m[4] /* RACE Write */ = 4
	}()
}

func foo() {
	m[3] = 4
}
