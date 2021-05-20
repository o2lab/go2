// +build ignore

package main

import (
	"fmt"
	"sync"
)

func Writer(mut *sync.Mutex, x *int) {
	mut.Lock()
	*x++
	mut.Unlock()
}

func main() {
	m := new(sync.Mutex)
	var x int
	go Writer(m, &x)
	m.Lock()
	fmt.Println("x is", x)
	m.Unlock()
}
