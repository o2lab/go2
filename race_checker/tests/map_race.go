package main

import "fmt"

func main() {
	var s []int
	s = append(s, 1, 2, 3)
	fmt.Println(s[0])
	var m map[int]int = make(map[int]int)
	m[1] = 2
	m[2] = 3
	go func() {
		m[3] = 3
	}()
	m[4] = 1
}
