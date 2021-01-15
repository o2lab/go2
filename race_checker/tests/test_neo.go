package main

import "sync"

var mutex sync.Mutex

func main() {
	i := 1
	go func() {
		i /* RACE Write */ = 1
		mutex.Lock()
		mutex.Unlock()
	}()
	mutex.Lock()
	mutex.Unlock()
	_ = i /* RACE Read */
}