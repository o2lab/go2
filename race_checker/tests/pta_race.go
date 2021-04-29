package main

import "fmt"

type objj struct {
	xxy int
}


func someAssign(input *objj) int {
	go func() {
		input.xxy++
	}()
	return 0
}

func main() {
	opq := &objj{xxy: 10}
	pqr := someAssign(opq)
	go func() {
		opq.xxy++
	}()
	fmt.Println(pqr)
}