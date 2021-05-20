package gorace_test

import (
	"fmt"
	"sync"
)

func main() {
	mumu := sync.RWMutex{}
	x6 := 0

	mumu.Lock()
	go func() {
		mumu.Lock() // bug in code
		x6 += 1
		fmt.Println("program will never reach here")
		mumu.Unlock()
	}()

	fmt.Println(x6)
}
