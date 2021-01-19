package main

import (
	"fmt"
	"sync"
)

func main() {
	mu := sync.Mutex{}
	x3 := 0
	go func() bool {
		var err bool
		err = false
		mu.Lock()
		var ret bool
		if err {
			mu.Unlock()
			x3 /* RACE Write */ = 3
			return err
		} else {
			if x3 == 5 {
				return err
			} else {
				ret = true
				if err {
					var e bool
					if e {
						e = false
					} else {
						e = true
					}
					mu.Unlock()
					x3 = 7
					return ret
				}
			}
		}
		mu.Unlock()
		x3 = 9
		return ret
	}()
	fmt.Println(x3 /* RACE Read */)
}
