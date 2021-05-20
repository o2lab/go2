//TestIstio8967 - PASS
package gorace_test

import (
	"sync"
	"testing"
	"time"
)

type Source interface {
	Start()
	Stop()
}

type fsSource struct {
	donec chan struct{}
}

func (s *fsSource) Start() {
	go func() {
		for {
			select {
			case <-s.donec /* RACE Read */ : // racy read on donec field
				return
			}
		}
	}()
}

func (s *fsSource) Stop() {
	close(s.donec)
	s.donec /* RACE Write */ = nil // racy write on donec field
}

func newFsSource() *fsSource {
	return &fsSource{
		donec: make(chan struct{}),
	}
}

func New() Source {
	return newFsSource()
}

func TestIstio8967(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s := New()
		s.Start() // spawns child goroutine that later triggers racy read
		s.Stop()  // triggers racy write
		time.Sleep(5 * time.Millisecond)
	}()
	wg.Wait()
}

func main() {
	var t *testing.T
	TestIstio8967(t)
}
