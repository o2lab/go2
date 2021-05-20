// +build ignore

package main

import (
	"fmt"
	"sync"
	"time"
)

func Deposit(mut *sync.Mutex, bal *int, amt int) {
	mut.Lock()
	*bal += amt
	mut.Unlock()
}

func main() {
	m := new(sync.Mutex)
	var balance int
	go func(mut *sync.Mutex, bal *int) {
		Deposit(mut, bal, 200)
		mut.Lock()
		fmt.Println("Balance:", *bal)
		mut.Unlock()
	}(m, &balance)
	go Deposit(m, &balance, 100)
	time.Sleep(1000*time.Millisecond)
}
