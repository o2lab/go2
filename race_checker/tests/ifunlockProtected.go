package main

import (
	"fmt"
	"sync"
)

func main() {
	mu := sync.Mutex{}
	x2 :=0
	go func() bool{
		var err bool
		err = false
		mu.Lock()
		x2 =1
		var ret bool
		if err{
			x2 =3
			mu.Unlock()
			return err
		} else {
			if x2 ==5 {
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
					x2 =7
					mu.Unlock()
					return ret
				}
			}
		}
		x2 =9
		mu.Unlock()
		return ret
	}()
	mu.Lock()
	fmt.Println(x2)
	mu.Unlock()
}
