package gorace_test

import (
	"sync"
	"testing"
)

type txBuffer struct {
	buckets map[string]struct{}
}

func (txb *txBuffer) reSet() {
	for k, _ := range txb.buckets {
		delete(txb.buckets /* RACE Write */, k) // racy write on buckets field
	}
}

type txReadBuffer struct{ txBuffer }

func (txr *txReadBuffer) Range() {
	_ = txr.buckets /* RACE Read */ ["1"] // racy read on buckets field
}

type readTx struct {
	buf txReadBuffer
}

func (rt *readTx) reset() {
	rt.buf.reSet()
}

func (rt *readTx) UnsafeRange() {
	rt.buf.Range()
}

func TestEtcd9446(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		txn := &readTx{
			buf: txReadBuffer{
				txBuffer{
					buckets: make(map[string]struct{}),
				},
			},
		}
		txn.buf.buckets["1"] = struct{}{}
		go func() {
			defer wg.Done()
			txn.reset() // trigger racy write
		}()
		go func() {
			defer wg.Done()
			txn.UnsafeRange() // trigger racy read
		}()
	}()
	wg.Wait()
}

func main() {
	var t *testing.T
	TestEtcd9446(t)
}
