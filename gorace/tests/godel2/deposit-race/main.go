// +build ignore

package gorace_test

import (
	"fmt"
	"time"
)

func Deposit(bal *int, amt int) {
	* /* RACE Write */ /* RACE Write */ /* RACE Read */ bal += amt
}

func main() {
	var balance int
	go func(bal *int) {
		Deposit(bal, 200)
		fmt.Println("Balance:", * /* RACE Read */ bal)
	}(&balance)
	go Deposit(&balance, 100)
	time.Sleep(1000 * time.Millisecond)
}
