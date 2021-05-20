package gorace_test

import "sync"

var mutex sync.Mutex

func main() {
	i02 := 1
	go func() {
		i02 /* RACE Write */ = 1
		mutex.Lock()
		mutex.Unlock()
	}()
	mutex.Lock()
	mutex.Unlock()
	_ = i02 /* RACE Read */
}
