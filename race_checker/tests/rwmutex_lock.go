package main

import (
	"fmt"
	"sync"
)

func main() {
	rwMu := sync.RWMutex{}
	x := 0
	y := 1
	go func() {
		rwMu.RLock()
		p := x + 2
		q := y + 2
		fmt.Println(p, q)
		rwMu.RUnlock()
	}()
	//go func() {
	//	i := x + 4
	//	j := y + 4
	//	fmt.Println(i, j)
	//}()
	rwMu.Lock()
	x = 4
	y = 5
	rwMu.Unlock()
	fmt.Println(x, y)
}
