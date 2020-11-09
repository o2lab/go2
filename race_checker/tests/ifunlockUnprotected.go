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
		var ret bool
		if err{
			mu.Unlock()
			x=3
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
					mu.Unlock()
					x=7
					return ret
				}
			}
		}
		mu.Unlock()
		x=9
		return ret
	}()
	fmt.Println(x)
}
