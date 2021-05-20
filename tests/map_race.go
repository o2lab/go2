package main

import "fmt"

var m = make(map[int]int)

func main() {
	var s []int
	s = append(s, 1, 2, 3)
	fmt.Println(s[0])
	m[1] = 2
	m[2] = 3
	go func() {
		m /* RACE Read */ [3] = 3
	}()
	go func() {
		m[ /* RACE Write */ 4] = 4
	}()
}

func foo() {
	m[3] = 4
}
