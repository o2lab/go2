package main

import (
	"sync"
)

type S struct {
	x, y, z int
}

func main() {
	SSlice := []S{S{2, 3, 1}, S{2, 3, 2}}
	w := sync.WaitGroup{}
	w.Add(len(SSlice))
	var s S
	for i := 0; i < len(SSlice); i++ {
		s = SSlice[i]

		go func() {
			_ = s.z
			w.Done()
		}()
	}
	w.Wait()
}
