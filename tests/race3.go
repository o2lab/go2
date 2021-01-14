package main

import (
	"sync"
)

type S struct {
	i int
}

func main() {
	SSlice := []S{S{}, S{}}
	w := sync.WaitGroup{}
	w.Add(len(SSlice))
	for _, s := range SSlice{
		go func() {
			_ = s.i
			w.Done()
		}()
	}
	w.Wait()
}
