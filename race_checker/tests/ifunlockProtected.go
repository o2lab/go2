package main

import (
	"fmt"
	"sync"
)

func main() {
	mu := sync.Mutex{}
	x:=0
	go func() bool{
		var err bool
		err = false
		mu.Lock()
		x=1
		var ret bool
		if err{
			x=3
			mu.Unlock()
			return err
		} else {
			if x==5 {
				return err
			} else {
				ret = true
				if err {
					var err bool
					if err {
						err = false
					} else {
						err = true
					}
					x=7
					mu.Unlock()
					return ret
				}
			}
		}
		x=9
		mu.Unlock()
		x=9
		return ret
	}()
	fmt.Println(x)
}
