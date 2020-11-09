package main

import (
	"sync"
	"fmt"
)

type ClientConn struct {
	mu	sync.RWMutex
	sc	int
}

func main() {
	x := 0
	y := 1
	cc := &ClientConn{
		sc: x,
	}
	res := false
	go func() {
		res = cc.GetMethodConfig(y)
		fmt.Println(res)
	}()
	cc.mu.Lock()
	cc.sc = 1
	cc.mu.Unlock()
}

func (cc *ClientConn) GetMethodConfig(y int) bool {
	cc.mu.RLock()
	defer cc.mu.RUnlock()
	if cc.sc == 1 {
		return true
	}
	if cc.sc == 2 {
		return true
	}
	return false
}


