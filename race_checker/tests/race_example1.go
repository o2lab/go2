package main

import "fmt"

var (
	i int
	j int
)

func main() {
	i = 1
	t := 2
	fmt.Println(t)
	go func() {
		i = 1
		t1 := 3
		fmt.Println(t1)
	}()
	i = 2
}

//func A() {
//	i = j + 1
//	go func() {
//		B()
//	}()
//}
//
//func B() {
//	j = i + 1
//	A()
//}
