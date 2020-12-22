package main

import "fmt"

func main() {
	a := 10 // write owned, not shared
	//b := a + 12

	readPtr(&a)

	b := make(map[int]int)
	b[1] = 1
	a = b[1]

	// MakeClosure - a becomes read owned
	defer func() {
		fmt.Println(a) // read owned
	}()

	b[2] = a // read owned
}

func readPtr(op *int) {
	_ = *op // read owned if 0 is read-owned
}
