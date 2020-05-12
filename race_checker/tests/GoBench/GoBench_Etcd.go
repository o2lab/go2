//TestEtcd4876 - PASS
package main

import (
	"sync"
	"testing"
	"time"
)

var ProgressReportInterval = 10 * time.Second

type Watcher interface {
	Watch()
}
type ServerStream interface{}

type Watch_WatchServer interface {
	Send()
	ServerStream
}
type watchWatchServer struct {
	ServerStream
}

func (x *watchWatchServer) Send() {}

type WatchServer interface {
	Watch(Watch_WatchServer)
}

type serverWatchStream struct{}

func (sws *serverWatchStream) sendLoop() {
	_ = time.NewTicker(ProgressReportInterval) // racy read on ProgressReportInterval
}

type watchServer struct{}

func (ws *watchServer) Watch(stream Watch_WatchServer) {
	sws := serverWatchStream{}
	go sws.sendLoop()
}

func TestEtcd4876(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		w := &watchServer{}
		go func() {
			defer wg.Done()
			testInterval := 3 * time.Second
			ProgressReportInterval = testInterval // racy write on ProgressReportInterval
		}()
		go func() {
			defer wg.Done()
			w.Watch(&watchWatchServer{}) // spawns child goroutine that later triggers racy read
		}()
	}()
	wg.Wait()
}

func main() {
	var t *testing.T
	TestEtcd4876(t)
}

//___________________________________________________________________________
//TestEtcd8194 - PASS
//package main
//
//import (
//"sync"
//"testing"
//"time"
//)
//
//var leaseRevokeRate = 1000
//
//func testLessorRenewExtendPileup() {
//	oldRevokeRate := leaseRevokeRate
//	defer func() { leaseRevokeRate = oldRevokeRate }()
//	leaseRevokeRate = 10 // racy write on leaseRevokeRate
//}
//
//type Lease struct {}
//
//type lessor struct {
//	mu sync.Mutex
//	stopC chan struct{}
//	doneC chan struct{}
//}
//
//func (le *lessor) runLoop() {
//	defer close(le.doneC)
//
//	for i:= 0; i < 10; i ++{
//		var ls []*Lease
//
//		ls = append(ls, &Lease{})
//
//		if len(ls) != 0 {
//			// rate limit
//			if len(ls) > leaseRevokeRate/2 { // racy read on leaseRevokeRates
//				ls = ls[:leaseRevokeRate/2]
//			}
//			select {
//			case <-le.stopC:
//				return
//			default:
//			}
//		}
//
//		select {
//		case <-time.After(5 * time.Millisecond):
//		case <-le.stopC:
//			return
//		}
//	}
//}
//
//func newLessor () *lessor {
//	l := &lessor{}
//	go l.runLoop()
//	return l
//}
//
//func testLessorGrant() {
//	newLessor()
//}
//
//func TestEtcd8194(t *testing.T) {
//	var wg sync.WaitGroup
//	wg.Add(2)
//	go func() {
//		defer wg.Done()
//		testLessorGrant() // will spawn child goroutine that later triggers racy read
//	}()
//	go func() {
//		defer wg.Done()
//		testLessorRenewExtendPileup() // triggers racy write
//	}()
//	wg.Wait()
//}
//
//func main() {
//	var t *testing.T
//	TestEtcd8194(t)
//}
//__________________________________________________________________________
//TestEtcd9446
//package main
//
//import (
//	"sync"
//	"testing"
//)
//
//type txBuffer struct {
//	buckets map[string]struct{}
//}
//
//func (txb *txBuffer) reSet() {
//	for k, _ := range txb.buckets {
//		delete(txb.buckets, k) // racy write on buckets field
//	}
//}
//
//type txReadBuffer struct{ txBuffer }
//
//func (txr *txReadBuffer) Range() () {
//	_ = txr.buckets["1"] // racy read on buckets field
//}
//
//type readTx struct {
//	buf txReadBuffer
//}
//
//func (rt *readTx) reset() {
//	rt.buf.reSet()
//}
//
//func (rt *readTx) UnsafeRange() {
//	rt.buf.Range()
//}
//
//func TestEtcd9446(t *testing.T) {
//	var wg sync.WaitGroup
//	wg.Add(3)
//	go func() {
//		defer wg.Done()
//		txn := &readTx{
//			buf: txReadBuffer{
//				txBuffer{
//					buckets:make(map[string]struct{}),
//				},
//			},
//		}
//		txn.buf.buckets["1"] = struct{}{}
//		go func() {
//			defer wg.Done()
//			txn.reset() // trigger racy write
//		}()
//		go func() {
//			defer wg.Done()
//			txn.UnsafeRange() // trigger racy read
//		}()
//	}()
//	wg.Wait()
//}
//
//func main() {
//	var t *testing.T
//	TestEtcd9446(t)
//}
