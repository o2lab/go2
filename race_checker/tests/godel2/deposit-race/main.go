// +build ignore

package main

import (
	"fmt"
	"time"
)

func Deposit(bal *int, amt int) {
	*bal += amt
}

func main() {
	var balance int
	go func(bal *int) {
		Deposit(bal, 200)
		fmt.Println("Balance:", *bal)
	}(&balance)
	go Deposit(&balance, 100)
	time.Sleep(1000*time.Millisecond)
}
