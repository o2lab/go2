// TestKubernetes79631
package main

import (
	"sync"
	"testing"
)

type heapData struct {
	items map[string]struct{}
}

func (h *heapData) Pop() {
	delete(h.items /* RACE Write */, "1") // racy write on items
}

type Interface interface {
	Pop()
}

func Pop(h Interface) {
	h.Pop()
}

type Heap struct {
	data *heapData
}

func (h *Heap) Pop() {
	Pop(h.data)
}

func (h *Heap) GetByKey() {
	_ = h.data.items /* RACE Read */ ["1"] // racy read on items
}

func (h *Heap) Get() {
	h.GetByKey()
}

func NewWithRecorder() *Heap {
	return &Heap{
		data: &heapData{
			items: make(map[string]struct{}),
		},
	}
}

type PriorityQueue struct {
	stop        chan struct{}
	lock        sync.RWMutex
	podBackoffQ *Heap
}

func (p *PriorityQueue) flushBackoffQCompleted() {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.podBackoffQ.Pop() // will trigger racy write

}

func NewPriorityQueue() *PriorityQueue {
	return NewPriorityQueueWithClock()
}

func NewPriorityQueueWithClock() *PriorityQueue {
	pg := &PriorityQueue{
		stop:        make(chan struct{}),
		podBackoffQ: NewWithRecorder(),
	}
	pg.run()
	return pg
}

func (p *PriorityQueue) run() {
	go Until(p.flushBackoffQCompleted, p.stop) // will trigger racy write
}

func BackoffUntil(f func(), stopCh <-chan struct{}) {
	for {
		select {
		case <-stopCh:
			return
		default:
		}

		func() {
			f() // will trigger racy write
		}()

		select {
		case <-stopCh:
			return
		}
	}
}

func JitterUntil(f func(), stopCh <-chan struct{}) {
	BackoffUntil(f, stopCh)
}

func Until(f func(), stopCh <-chan struct{}) {
	JitterUntil(f, stopCh)
}

func TestKubernetes79631(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		wg.Done()
		q := NewPriorityQueue()
		q.podBackoffQ.Get()
	}()
	wg.Wait()
}
func main() {
	var t *testing.T
	TestKubernetes79631(t)
}
