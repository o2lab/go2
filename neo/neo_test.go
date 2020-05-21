package main

import (
	"sync"
	"testing"
	"time"
)

// Detectable only by predictive analysis
func TestRacePureHappensBefore(t *testing.T) {
	var x int
	ch := make(chan struct{}, 2)
	m := sync.Mutex{}
	go func() {
		x = 1
		m.Lock()
		defer m.Unlock()
		ch <- struct{}{}
	}()

	go func() {
		<-time.After(1e5)
		m.Lock()
		m.Unlock()
		_ = x
		ch <- struct{}{}
	}()
	<-ch
	<-ch
}

func TestNoRaceCausalPrecedence(t *testing.T) {
	var x, y int
	ch := make(chan struct{}, 2)
	m := sync.Mutex{}
	go func() {
		x = 1
		m.Lock()
		y = 1
		defer m.Unlock()
		ch <- struct{}{}
	}()

	go func() {
		<-time.After(1e5)
		m.Lock()
		_ = y
		m.Unlock()
		_ = x
		ch <- struct{}{}
	}()
	<-ch
	<-ch
}

func TestOVCond(t *testing.T) {
	mu := sync.Mutex{}
	c := sync.NewCond(&mu)
	ch := make(chan struct{})
	started := make(chan struct{})
	go func() {
		defer close(started)
		mu.Lock()
		c.Signal() // signal may be missed!
		mu.Unlock()
	}()
	go func() {
		defer close(ch)
		mu.Lock()
		c.Wait() // block
		mu.Unlock()
	}()
	<-started
	select {
	case <-time.After(10 * time.Millisecond):
		t.Fatal("Timeout waiting for all goroutine to exit")
	case <-ch:
	}
}

// Correct usage of cond
func TestNoOVCond(t *testing.T) {
	mu := sync.Mutex{}
	c := sync.NewCond(&mu)
	ch := make(chan struct{})
	started := make(chan struct{})
	x := 0
	go func() {
		defer close(started)
		mu.Lock()
		x = 1
		mu.Unlock()
		c.Signal() // It's ok to miss this signal
	}()
	go func() {
		defer close(ch)
		mu.Lock()
		for x == 0 {
			c.Wait()
		}
		mu.Unlock()
	}()
	<-started
	select {
	case <-time.After(10 * time.Millisecond):
		t.Fatal("Timeout waiting for all goroutine to exit")
	case <-ch:
	}
}

func TestOVCloseChanLocked(t *testing.T) {
	mu := sync.Mutex{}
	c := make(chan struct{})
	ch := make(chan struct{}, 2)
	go func() {
		<-time.After(1e6)
		mu.Lock()
		close(c)
		mu.Unlock()
		ch <- struct{}{}
	}()
	go func() {
		mu.Lock()
		c <- struct{}{} // c may have been closed already
		mu.Unlock()
		ch <- struct{}{}
	}()
	<-c
	<-ch
	<-ch
}

func TestOVWaitgroupMisuse(t *testing.T) {
	mu := sync.Mutex{}
	wg := sync.WaitGroup{}
	ch := make(chan struct{}, 2)
	go func() {
		time.After(1e5)
		//mu.Lock()
		wg.Add(1) // The first Add must happen before Wait
		//mu.Unlock()
		ch <- struct{}{}
	}()
	go func() {
		mu.Lock()
		wg.Wait() // Not well synchronized with the first Add
		mu.Unlock()
		ch <- struct{}{}
	}()
	<-ch
	select {
	case <-time.After(5 * time.Millisecond):
		t.Fatal("Timeout waiting for all goroutine to exit")
	case <-ch:
	}
}

func TestDeadlockChanSend(t *testing.T) {
	done := make(chan int)
	ch := make(chan int)
	var mu sync.Mutex
	go func() {
		mu.Lock()
		ch <- 1 // Blocking on chan send while holding a mutex.
		mu.Unlock()
		done <- 1
	}()
	mu.Lock()
	mu.Unlock()
	<-ch // Unblock the send
	<-done
}

func TestDeadlockChanRecv(t *testing.T) {
	done := make(chan int)
	ch := make(chan int)
	var mu sync.Mutex
	go func() {
		mu.Lock()
		<-ch // Blocking on chan recv while holding a mutex.
		mu.Unlock()
		done <- 1
	}()
	mu.Lock()
	mu.Unlock()
	ch <- 1 // Unblock the recv
	<-done
}

func TestDeadlockChanClose(t *testing.T) {
	done := make(chan int)
	ch := make(chan int)
	var mu sync.Mutex
	go func() {
		mu.Lock()
		<-ch // Blocking on chan recv while holding a mutex.
		mu.Unlock()
		done <- 1
	}()
	mu.Lock()
	mu.Unlock()
	close(ch) // Unblock the recv
	<-done
}

func TestDeadlockChanRecvBuffered(t *testing.T) {
	done := make(chan int)
	ch := make(chan int, 1)
	var mu sync.Mutex
	go func() {
		mu.Lock()
		<-ch
		mu.Unlock()
		done <- 1
	}()
	mu.Lock()
	mu.Unlock()
	ch <- 1 // Unblock the recv
	<-done
}

// Deadlock free
func TestNoDeadlockChanRecvBuffered(t *testing.T) {
	done := make(chan int)
	ch := make(chan int, 1)
	var mu sync.Mutex
	go func() {
		mu.Lock()
		ch <- 1 // Block here
		mu.Unlock()
		done <- 1
	}()
	mu.Lock()
	mu.Unlock()
	<-ch // Unblock the send
	<-done
}

func TestDeadlockWaitgroup(t *testing.T) {
	done := make(chan int)
	var wg sync.WaitGroup
	var mu sync.Mutex
	wg.Add(1)
	go func() {
		mu.Lock()
		wg.Wait() // Block here
		mu.Unlock()
		done <- 1
	}()
	mu.Lock()
	mu.Unlock()
	wg.Done() // Unblock the wait
	<-done
}

func TestDeadlockChanSendLocked(t *testing.T) {
	done := make(chan int)
	ch := make(chan int, 1)
	var mu sync.Mutex
	go func() {
		mu.Lock()
		<-ch // Block here
		mu.Unlock()
		done <- 1
	}()
	mu.Lock()
	ch <- 1 // Unblock the recv
	mu.Unlock()
	<-done
}

func TestDeadlockWaitgroupDoubleLock(t *testing.T) {
	done := make(chan int)
	var wg sync.WaitGroup
	var m1 sync.Mutex
	var m2 sync.Mutex
	wg.Add(1)
	go func() {
		m1.Lock()
		m2.Lock()
		wg.Wait() // Block
		m2.Unlock()
		m1.Unlock()
		done <- 1
	}()
	m1.Lock()
	m2.Lock()
	wg.Done() // Unblock the wait
	m2.Unlock()
	m1.Unlock()
	<-done
}

// This test is deadlock free
func TestNoDeadlockChanRecvLikeLock(t *testing.T) {
	done := make(chan int)
	ch := make(chan int, 1)
	ch <- 1
	var mu sync.Mutex
	for i := 0; i < 10; i++ {
		go func() {
			mu.Lock()
			<-ch    // Never block
			ch <- 1 // Never block
			mu.Unlock()
			done <- 1
		}()
	}
	mu.Lock()
	<-ch    // Never block
	ch <- 1 // Never block
	mu.Unlock()
	for i := 0; i < 10; i++ {
		<-done
	}
}

// This test is deadlock free
func TestNoDeadlockChanRecvLikeRWLock(t *testing.T) {
	done := make(chan int)
	ch := make(chan int, 1)
	ch <- 1
	var mu sync.RWMutex
	for i := 0; i < 10; i++ {
		go func() {
			mu.RLock()
			<-ch
			ch <- 1
			mu.RUnlock()
			done <- 1
		}()
	}
	mu.RLock()
	<-ch
	ch <- 1
	mu.RUnlock()
	for i := 0; i < 10; i++ {
		<-done
	}
}

// This test is deadlock free
func TestNoDeadlockChanRecvLikeLock2(t *testing.T) {
	done := make(chan int)
	ch := make(chan int, 1)
	ch <- 1
	var mu sync.RWMutex
	for i := 0; i < 10; i++ {
		go func() {
			mu.RLock()
			<-ch
			ch <- 1
			mu.RUnlock()
			done <- 1
		}()
	}
	mu.RLock()
	mu.RUnlock()
	<-ch
	ch <- 1
	for i := 0; i < 10; i++ {
		<-done
	}
}
