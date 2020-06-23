package main

import (
"sync"
"testing"
"time"
)

var leaseRevokeRate = 1000

func testLessorRenewExtendPileup() {
	oldRevokeRate := leaseRevokeRate
	defer func() { leaseRevokeRate = oldRevokeRate }()
	leaseRevokeRate = 10 // racy write on leaseRevokeRate
}

type Lease struct {}

type lessor struct {
	mu sync.Mutex
	stopC chan struct{}
	doneC chan struct{}
}

func (le *lessor) runLoop() {
	defer close(le.doneC)

	for i:= 0; i < 10; i ++{
		var ls []*Lease

		ls = append(ls, &Lease{})

		if len(ls) != 0 {
			// rate limit
			if len(ls) > leaseRevokeRate/2 { // racy read on leaseRevokeRates
				ls = ls[:leaseRevokeRate/2]
			}
			select {
			case <-le.stopC:
				return
			default:
			}
		}

		select {
		case <-time.After(5 * time.Millisecond):
		case <-le.stopC:
			return
		}
	}
}

func newLessor () *lessor {
	l := &lessor{}
	go l.runLoop()
	return l
}

func testLessorGrant() {
	newLessor()
}

func TestEtcd8194(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		testLessorGrant() // will spawn child goroutine that later triggers racy read
	}()
	go func() {
		defer wg.Done()
		testLessorRenewExtendPileup() // triggers racy write
	}()
	wg.Wait()
}

func main() {
	var t *testing.T
	TestEtcd8194(t)
}