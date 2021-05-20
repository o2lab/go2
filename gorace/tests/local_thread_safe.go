package gorace_test

import (
	"fmt"
)

type myfield2 struct {
	f string
}

type mystruct2 struct {
	myf *myfield2
}

func main() {

	go func() {
		f := &myfield2{f: "hello"}
		my1 := &mystruct2{myf: f}
		my1.myf.f = my1.myf.f + "i want change"
		fmt.Println("mystruct2: ", my1.myf.f)
	}()

	go func() {
		f := &myfield2{f: "hello"}
		my2 := &mystruct2{myf: f}
		my2.myf.f = my2.myf.f + "i want change too"
		fmt.Println("mystruct2: ", my2.myf.f)
	}()
}
