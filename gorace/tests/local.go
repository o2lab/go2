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
		my1.myf.f /* RACE Write */ = my1.myf.f /* RACE Read */ + "i want change"
		fmt.Println("mystruct: ", my1.myf.f /* RACE Read */)
		w.Done()
	}()

	w.Add(1)
	go func() {
		my2 := &mystruct{myf: f}
		my2.myf.f /* RACE Write */ /* RACE Write */ = my2.myf.f /* RACE Read */ + "i want change too"
		fmt.Println("mystruct: ", my2.myf.f)
		w.Done()
	}()

	w.Wait()
}
