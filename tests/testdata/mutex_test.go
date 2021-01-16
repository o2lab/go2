package main_test

import (
	"runtime/debug"
	"testing"
)

func TestRace2(t *testing.T) {
	debug.PrintStack()
	x := 0
	go func() {
		x = 1
	}()
	_ = x
}
//
//func TestNoRaceMutex(t *testing.T) {
//	var mu sync.Mutex
//	var x int16 = 0
//	_ = x
//	ch := make(chan bool, 2)
//	go func() {
//		mu.Lock()
//		defer mu.Unlock()
//		x = 1
//		ch <- true
//	}()
//	go func() {
//		mu.Lock()
//		x = 2
//		mu.Unlock()
//		ch <- true
//	}()
//	<-ch
//	<-ch
//}
//
//func TestRaceMutex(t *testing.T) {
//	var mu sync.Mutex
//	var x int16 = 0
//	_ = x
//	ch := make(chan bool, 2)
//	go func() {
//		x = 1
//		mu.Lock()
//		defer mu.Unlock()
//		ch <- true
//	}()
//	go func() {
//		x = 2
//		mu.Lock()
//		mu.Unlock()
//		ch <- true
//	}()
//	<-ch
//	<-ch
//}
//
//func TestRaceMutex2(t *testing.T) {
//	var mu1 sync.Mutex
//	var mu2 sync.Mutex
//	var x int8 = 0
//	_ = x
//	ch := make(chan bool, 2)
//	go func() {
//		mu1.Lock()
//		defer mu1.Unlock()
//		x = 1
//		ch <- true
//	}()
//	go func() {
//		mu2.Lock()
//		x = 2
//		mu2.Unlock()
//		ch <- true
//	}()
//	<-ch
//	<-ch
//}
//
//func TestNoRaceMutexSemaphore(t *testing.T) {
//	var mu sync.Mutex
//	ch := make(chan bool, 2)
//	x := 0
//	_ = x
//	runtime.RaceUseHBLockBegin()
//	mu.Lock()
//	runtime.RaceUseHBLockEnd()
//	go func() {
//		x = 1
//		mu.Unlock()
//		ch <- true
//	}()
//	go func() {
//		mu.Lock()
//		x = 2
//		mu.Unlock()
//		ch <- true
//	}()
//	<-ch
//	<-ch
//}
//
//// from doc/go_mem.html
//func TestNoRaceMutexExampleFromHtml(t *testing.T) {
//	var l sync.Mutex
//	a := ""
//	runtime.RaceUseHBLockBegin()
//	l.Lock()
//	runtime.RaceUseHBLockEnd()
//	go func() {
//		a = "hello, world"
//		l.Unlock()
//	}()
//	l.Lock()
//	_ = a
//}
//
//func TestRaceMutexOverwrite(t *testing.T) {
//	c := make(chan bool, 1)
//	var mu sync.Mutex
//	go func() {
//		mu = sync.Mutex{}
//		c <- true
//	}()
//	mu.Lock()
//	<-c
//}
//
//func TestNoRaceRWLockConflict(t *testing.T) {
//	var mu sync.RWMutex
//	var x = map[int]int{}
//	const N = 5
//	for j := 0; j < N; j++ {
//		go func() {
//			mu.Lock()
//			defer mu.Unlock()
//			for i := 0; i < 10; i++ {
//				_ = x[i]
//			}
//		}()
//	}
//	for i := 0; i < N; i++ {
//		mu.Lock()
//		x[i] = 1
//		mu.Unlock()
//	}
//}
//
//func TestNoRaceUnlockNotOwned(t *testing.T) {
//	done := make(chan int)
//	var mu sync.RWMutex
//	x := 1
//	runtime.RaceUseHBLockBegin()
//	mu.RLock()
//	runtime.RaceUseHBLockEnd()
//	go func() {
//		_ = x
//		mu.RUnlock()
//		mu.RLock()
//		mu.RUnlock()
//		done <- 1
//	}()
//	mu.Lock()
//	x = 1
//	mu.Unlock()
//	<-done
//}
