package gorace_test

import (
	"fmt"
	"sync"
)

func main() {
	rwMu := sync.RWMutex{}
	x33 := 0
	y33 := 1
	go func() {
		rwMu.RLock()
		p := x33 + 2
		q := y33 + 2
		fmt.Println(p, q)
		rwMu.RUnlock()
	}()
	//go func() {
	//	i := x2 + 4
	//	j := y33 + 4
	//	fmt.Println(i, j)
	//}()
	rwMu.Lock()
	x33 = 4
	y33 = 5
	rwMu.Unlock()
	fmt.Println(x33, y33)
}
