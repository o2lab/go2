package main

import (
	"fmt"
	"sync"
)

func main() {
	mu := sync.Mutex{}
	x22 := 0
	go func() bool {
		var err bool
		err = false
		mu.Lock()
		x22 = 1
		var ret bool
		if err {
			x22 = 3
			mu.Unlock()
			return err
		} else {
			if x22 == 5 {
				return err
			} else {
				ret = true
				if err {
					var err1 bool
					if err1 {
						err1 = false
					} else {
						err1 = true
					}
					x22 = 7
					mu.Unlock()
					return ret
				}
			}
		}
		x22 = 9
		mu.Unlock()
		return ret
	}()
	mu.Lock()
	fmt.Println(x22)
	mu.Unlock()
}
