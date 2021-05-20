package main

import (
	"fmt"
)

type myfield3 struct {
	f string
}

type mystruct3 struct {
	myf *myfield3
}

func main() {
	f := &myfield3{f: "hello"}
	for i2 := 0; i2 < 2; i2++ {
		go func() {
			my1 := &mystruct3{myf: f}
			my1.myf.f = my1.myf.f + "i2 want change"
			fmt.Println("mystruct2: ", my1.myf.f)
		}()
	}
}
