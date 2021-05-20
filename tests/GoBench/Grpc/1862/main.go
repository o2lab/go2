// TestGrpc1862
package main

import (
	"sync"
	"testing"
	"time"
)

func TestGrpc1862(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		abort := false
		time.AfterFunc(time.Nanosecond, func() { abort /* RACE Write */ = true }) // spawns child goroutine in time package triggering racy write on abort
		if abort /* RACE Read */ {
		} // racy read on abort
	}()
	wg.Wait()
}

func main() {
	var t *testing.T
	TestGrpc1862(t)
}
