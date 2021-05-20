//TestKubernetes88331
package gorace_test

import (
	"sync"
	"testing"
)

type data struct {
	queue []struct{}
}

func (h *data) Pop() {
	h.queue /* RACE Write */ = h.queue[0 : len(h.queue)-1] // racy write on queue
}

type Interface interface {
	Pop()
}

func Pop(h Interface) {
	h.Pop()
}

type Heap struct {
	data *data
}

func (h *Heap) Pop() {
	Pop(h.data)
}
func (h *Heap) Len() int {
	return len(h.data.queue /* RACE Read */) // racy read on queue
}

func NewWithRecorder() *Heap {
	return &Heap{
		data: &data{
			queue: []struct{}{
				struct{}{},
				struct{}{},
			},
		},
	}
}

type PriorityQueue struct {
	stop        chan struct{}
	lock        sync.RWMutex
	podBackoffQ *Heap
	activeQ     *Heap
}

func (p *PriorityQueue) flushBackoffQCompleted() {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.podBackoffQ.Pop()

}

func NewPriorityQueue() *PriorityQueue {
	return &PriorityQueue{
		stop:        make(chan struct{}),
		activeQ:     NewWithRecorder(),
		podBackoffQ: NewWithRecorder(),
	}
}

func createAndRunPriorityQueue() *PriorityQueue {
	q := NewPriorityQueue()
	q.Run()
	return q
}

func (p *PriorityQueue) Run() {
	go Until(p.flushBackoffQCompleted, p.stop)
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

func TestKubernetes88331(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		wg.Done()
		p := createAndRunPriorityQueue() // triggers child goroutine that leads to racy write
		p.podBackoffQ.Len()              // triggers racy read
	}()
	wg.Wait()
}
func main() {
	var t *testing.T
	TestKubernetes88331(t)
}
