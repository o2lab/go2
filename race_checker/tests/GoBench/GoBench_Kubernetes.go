//TestKubernetes79631
//package main
//
//import (
//	"sync"
//	"testing"
//)
//
//type heapData struct {
//	items map[string]struct{}
//}
//
//func (h *heapData) Pop() {
//	delete(h.items, "1")
//}
//
//type Interface interface {
//	Pop()
//}
//
//func Pop(h Interface) {
//	h.Pop()
//}
//
//type Heap struct {
//	data *heapData
//}
//
//func (h *Heap) Pop() {
//	Pop(h.data)
//}
//
//func (h *Heap) GetByKey() {
//	_ = h.data.items["1"]
//}
//
//func (h *Heap) Get() {
//	h.GetByKey()
//}
//
//func NewWithRecorder() *Heap {
//	return &Heap{
//		data: &heapData{
//			items: make(map[string]struct{}),
//		},
//	}
//}
//
//type PriorityQueue struct {
//	stop        chan struct{}
//	lock        sync.RWMutex
//	podBackoffQ *Heap
//}
//
//func (p *PriorityQueue) flushBackoffQCompleted() {
//	p.lock.Lock()
//	defer p.lock.Unlock()
//	p.podBackoffQ.Pop()
//
//}
//
//func NewPriorityQueue() *PriorityQueue {
//	return NewPriorityQueueWithClock()
//}
//
//func NewPriorityQueueWithClock() *PriorityQueue {
//	pg := &PriorityQueue{
//		stop:        make(chan struct{}),
//		podBackoffQ: NewWithRecorder(),
//	}
//	pg.run()
//	return pg
//}
//
//func (p *PriorityQueue) run() {
//	go Until(p.flushBackoffQCompleted, p.stop)
//}
//
//func BackoffUntil(f func(), stopCh <-chan struct{}) {
//	for {
//		select {
//		case <-stopCh:
//			return
//		default:
//		}
//
//		func() {
//			f()
//		}()
//
//		select {
//		case <-stopCh:
//			return
//		}
//	}
//}
//
//func JitterUntil(f func(), stopCh <-chan struct{}) {
//	BackoffUntil(f, stopCh)
//}
//
//func Until(f func(), stopCh <-chan struct{}) {
//	JitterUntil(f, stopCh)
//}
//
//func TestKubernetes79631(t *testing.T) {
//	var wg sync.WaitGroup
//	wg.Add(1)
//	go func() {
//		wg.Done()
//		q := NewPriorityQueue()
//		q.podBackoffQ.Get()
//	}()
//	wg.Wait()
//}
//func main() {
//	var t *testing.T
//	TestKubernetes79631(t)
//}
// ____________________________________________________________________________________
//TestKubernetes80284
//package main
//
//import (
//	"sync"
//	"testing"
//)
//
//type Dialer struct {}
//
//func (d *Dialer) CloseAll() {}
//
//func NewDialer() *Dialer {
//	return &Dialer{}
//}
//
//type Authenticator struct {
//	onRotate func()
//}
//
//func (a *Authenticator) UpdateTransportConfig() {
//	d := NewDialer()
//	a.onRotate = d.CloseAll // repeated write
//}
//
//func newAuthenticator() *Authenticator {
//	return &Authenticator{}
//}
//
//func TestKubernetes80284(t *testing.T) {
//	var wg sync.WaitGroup
//	wg.Add(2)
//	a := newAuthenticator()
//	for i := 0; i < 2; i++ {
//		go func() {
//			defer wg.Done()
//			a.UpdateTransportConfig() // triggers repeated write
//		}()
//	}
//	wg.Wait()
//}
//
//func main() {
//	var t *testing.T
//	TestKubernetes80284(t)
//}
//_________________________________________________________________________________
// TestKubernetes81091
//package main
//
//import (
//	"fmt"
//	"sync"
//	"testing"
//)
//
//type FakeFilterPlugin struct {
//	numFilterCalled int
//}
//
//func (fp *FakeFilterPlugin) Filter() {
//	fp.numFilterCalled++ // racy read and write
//}
//
//type FilterPlugin interface {
//	Filter()
//}
//
//type framework struct {
//	filterPlugins []FilterPlugin
//}
//
//func (f *framework) RunFilterPlugins() {
//	for _, pl := range f.filterPlugins {
//		pl.Filter()
//	}
//}
//
//type Framework interface {
//	RunFilterPlugins()
//}
//
//func NewFramework() Framework {
//	f := &framework{}
//	f.filterPlugins = append(f.filterPlugins, &FakeFilterPlugin{})
//	return f
//}
//
//type genericScheduler struct {
//	framework Framework
//}
//
//func NewGenericScheduler(framework Framework,) *genericScheduler {
//	return &genericScheduler{
//		framework: framework,
//	}
//}
//
//func (g *genericScheduler) findNodesThatFit() {
//	checkNode := func(i int) {
//		g.framework.RunFilterPlugins()
//	}
//	ParallelizeUntil(2,2, checkNode)
//}
//
//func (g *genericScheduler) Schedule() {
//	g.findNodesThatFit()
//}
//
//
//type DoWorkPieceFunc func(piece int)
//
//func ParallelizeUntil(workers, pieces int, doWorkPiece DoWorkPieceFunc) {
//	var stop <-chan struct{}
//
//	toProcess := make(chan int, pieces)
//	for i := 0; i < pieces; i++ {
//		toProcess <- i
//	}
//	close(toProcess)
//
//	if pieces < workers {
//		workers = pieces
//	}
//
//	wg := sync.WaitGroup{}
//	wg.Add(workers)
//	for i := 0; i < workers; i++ {
//		go func() {
//			defer wg.Done()
//			for piece := range toProcess {
//				select {
//				case <-stop:
//					return
//				default:
//					fmt.Println("check")
//					doWorkPiece(piece)
//				}
//			}
//		}()
//	}
//	wg.Wait()
//}
//
//func TestKubernetes81091(t *testing.T) {
//	var wg sync.WaitGroup
//	wg.Add(1)
//	go func() {
//		defer wg.Done()
//		filterFramework := NewFramework()
//		scheduler := NewGenericScheduler(filterFramework)
//		scheduler.Schedule()
//	}()
//	wg.Wait()
//}
//func main() {
//	var t *testing.T
//	TestKubernetes81091(t)
//}
//__________________________________________________________________________________
// TestKubernetes81148
//package main
//
//import (
//	"testing"
//	"sync"
//	"time"
//)
//
//const unschedulableQTimeInterval = 60 * time.Second
//
//type Pod string
//
//type PodInfo struct {
//	Pod Pod
//	Timestamp time.Time
//}
//
//type UnschedulablePodsMap struct {
//	podInfoMap map[string]*PodInfo
//	keyFunc    func(Pod) string
//}
//
//func (u *UnschedulablePodsMap) addOrUpdate(pInfo *PodInfo) {
//	podID := u.keyFunc(pInfo.Pod)
//	u.podInfoMap[podID] = pInfo
//}
//
//func GetPodFullName(pod Pod) string {
//	return string(pod)
//}
//
//func newUnschedulablePodsMap() *UnschedulablePodsMap {
//	return &UnschedulablePodsMap{
//		podInfoMap: make(map[string]*PodInfo),
//		keyFunc: GetPodFullName,
//	}
//}
//
//type PriorityQueue struct {
//	stop  <-chan struct{}
//	lock sync.RWMutex
//	unschedulableQ *UnschedulablePodsMap
//}
//
//func (p *PriorityQueue) flushUnschedulableQLeftover() {
//	p.lock.Lock()
//	defer p.lock.Unlock()
//
//	for _, pInfo := range p.unschedulableQ.podInfoMap {
//		_ = pInfo.Timestamp // racy read
//	}
//}
//
//func (p *PriorityQueue) run() {
//	go Until(p.flushUnschedulableQLeftover, p.stop)
//}
//
//func (p *PriorityQueue) newPodInfo(pod Pod) *PodInfo {
//	return &PodInfo{
//		Pod:       pod,
//		Timestamp: time.Now(),
//	}
//}
//
//func NewPriorityQueueWithClock(stop <-chan struct{}) *PriorityQueue {
//	pq := &PriorityQueue{
//		stop: stop,
//		unschedulableQ:newUnschedulablePodsMap(),
//	}
//	pq.run()
//	return pq
//}
//
//func NewPriorityQueue(stop <-chan struct{}) *PriorityQueue {
//	return NewPriorityQueueWithClock(stop)
//}
//
//func BackoffUntil(f func(), stopCh <-chan struct{}) {
//	for {
//		select {
//		case <-stopCh:
//			return
//		default:
//		}
//
//
//		func() {
//			f()
//		}()
//
//		select {
//		case <-stopCh:
//			return
//		}
//	}
//}
//
//func JitterUntil(f func(), stopCh <-chan struct{}) {
//	BackoffUntil(f, stopCh)
//}
//
//func Until(f func(), stopCh <-chan struct{}) {
//	JitterUntil(f, stopCh)
//}
//
//func addOrUpdateUnschedulablePod(p *PriorityQueue, pod Pod) {
//	p.lock.Lock()
//	defer p.lock.Unlock()
//	p.unschedulableQ.addOrUpdate(p.newPodInfo(pod))
//}
//
//func TestKubernetes81148(t *testing.T) {
//	var wg sync.WaitGroup
//	wg.Add(1)
//	go func() {
//		defer wg.Done()
//		q := NewPriorityQueue(nil) // will trigger child goroutine that leads to racy read
//		highPod := Pod("1")
//		addOrUpdateUnschedulablePod(q, highPod)
//		q.unschedulableQ.podInfoMap[GetPodFullName(highPod)].Timestamp = time.Now().Add(-1 * unschedulableQTimeInterval) // racy write
//	}()
//	wg.Wait()
//}
//func main() {
//	var t *testing.T
//	TestKubernetes81148(t)
//}
//_______________________________________________________________________________________
//TestKubernetes88331
package main

import (
	"sync"
	"testing"
)

type data struct {
	queue []struct{}
}

func (h *data) Pop() {
	h.queue = h.queue[0 : len(h.queue)-1] // racy write on queue
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
	return len(h.data.queue) // racy read on queue
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
