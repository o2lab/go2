package main

import (
	"fmt"
	"sync"
)

type myfield struct {
	f string
}

type mystruct struct {
	myf *myfield
}

func main() {
	f := &myfield{f: "hello"}

	var w sync.WaitGroup

	w.Add(1)
	go func() {
		my1 := &mystruct{myf: f}
		my1.myf.f = my1.myf.f + "i want change"
		fmt.Println("mystruct: ", my1.myf.f)
		w.Done()
	}()
	w.Wait()

	w.Add(1)
	go func() {
		my2 := &mystruct{myf: f}
		my2.myf.f = my2.myf.f + "i want change too"
		fmt.Println("mystruct: ", my2.myf.f)
		w.Done()
	}()

	w.Wait()
}
