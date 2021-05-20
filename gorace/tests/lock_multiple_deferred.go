package gorace_test

import (
	"fmt"
	"sync"
)

type ClientConn struct {
	mu sync.RWMutex
	sc int
}

func main() {
	x5 := 0
	y5 := 1
	cc := &ClientConn{
		sc: x5,
	}
	res := false
	go func() {
		res = cc.GetMethodConfig(y5)
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
