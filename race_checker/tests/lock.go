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
		x = 2
		m.Unlock()
	}()
	m.Lock()
	fmt.Println(x)
	m.Unlock()
}
