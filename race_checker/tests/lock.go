package main

import (
	"fmt"
	"sync"
)

func main() {
	m := sync.Mutex{}
	x := 1
	go func() {
		m.Lock()
		defer m.Unlock()
		x = 2
	}()
	m.Lock()
	defer m.Unlock()
	fmt.Println(x)
}
