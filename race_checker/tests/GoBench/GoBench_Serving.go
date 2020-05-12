//TestServing3148
package main

import (
	"sync"
	"testing"
)

type PodAutoscalerInterface interface {
	Create()
}

type PodAutoscalersGetter interface {
	PodAutoscalers() PodAutoscalerInterface
}

type AutoscalingV1alpha1Interface interface {
	PodAutoscalersGetter
}

type clientset_Interface interface {
	AutoscalingV1alpha1() AutoscalingV1alpha1Interface
}

type FakeAutoscalingV1alpha1 struct {
	*Fake
}

func (c *FakeAutoscalingV1alpha1) PodAutoscalers() PodAutoscalerInterface {
	return &FakePodAutoscalers{c}
}

type Clientset struct {
	Fake
}

func (c *Clientset) AutoscalingV1alpha1() AutoscalingV1alpha1Interface {
	return &FakeAutoscalingV1alpha1{Fake: &c.Fake}
}

type FakePodAutoscalers struct {
	Fake *FakeAutoscalingV1alpha1
}

func (c *FakePodAutoscalers) Create() {
	c.Fake.Invokes()
}

type Reconciler struct {
	ServingClientSet clientset_Interface
}

func (c *Reconciler) Reconcile() {
	c.reconcile()
}

func (c *Reconciler) reconcile() {
	phases := []struct {
		name string
		f    func()
	}{{
		name: "KPA",
		f:    c.reconcileKPA,
	}}
	for _, phase := range phases {
		phase.f()
	}
}

func (c *Reconciler) reconcileKPA() {
	c.createKPA()
}

func (c *Reconciler) createKPA() {
	c.ServingClientSet.AutoscalingV1alpha1().PodAutoscalers().Create()
}

type controller_Reconciler interface {
	Reconcile()
}

type Impl struct {
	controller_Reconciler controller_Reconciler
}

func (c *Impl) Run(threadiness int) {
	sg := sync.WaitGroup{}
	defer sg.Wait()

	for i := 0; i < threadiness; i++ {
		sg.Add(1)
		go func() {
			defer sg.Done()
			c.processNextWorkItem()
		}()
	}
}

func (c *Impl) processNextWorkItem() {
	c.controller_Reconciler.Reconcile()
}

func NewImpl(r controller_Reconciler) *Impl {
	return &Impl{
		controller_Reconciler: r,
	}
}

func NewController() *Impl {
	c := &Reconciler{}
	return NewImpl(c)
}

type Group struct {
	wg      sync.WaitGroup
	errOnce sync.Once
}

func (g *Group) Wait() {
	g.wg.Wait()
}

func (g *Group) Go(f func()) {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		f()
	}()
}

type Hooks struct{}

func NewHooks() *Hooks {
	return &Hooks{}
}
func (h *Hooks) OnUpdate(fake *Fake) {
	fake.PrependReactor()
}

type Reactor interface{}

type SimpleReactor struct{}

type Fake struct {
	ReactionChain []Reactor
}

func (c *Fake) Invokes() {
	for _ = range c.ReactionChain {
	} // racy read on ReactionChain field
}

func (c *Fake) PrependReactor() {
	c.ReactionChain = append([]Reactor{&SimpleReactor{}}) // racy write on ReactionChain field
}

func TestServing3148(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		cs := &Clientset{}
		controller := NewController()
		controller.controller_Reconciler.(*Reconciler).ServingClientSet = cs
		eg := &Group{}
		defer func() {
			eg.Wait()
		}()
		eg.Go(func() { controller.Run(1) }) // spawns child goroutine that triggers racy read
		h := NewHooks()
		h.OnUpdate(&cs.Fake) // triggers racy write
	}()
	wg.Wait()
}

func main() {
	var t *testing.T
	TestServing3148(t)
}

//________________________________________________________________________________________
//TestServing4908
//package main
//
//import (
//	"testing"
//	"sync"
//)
//
//type TestingT interface {
//	Logf(string, ...interface{})
//}
//
//type WriteSyncer interface {
//	Write()
//}
//
//type CheckedEntry struct {
//	ErrorOutput WriteSyncer
//	cores       []Core
//}
//
//func (ce *CheckedEntry) Write() {
//	for i := range ce.cores {
//		ce.cores[i].Write()
//	}
//}
//type testingWriter struct {
//	t TestingT
//}
//
//func newTestingWriter(t TestingT) testingWriter {
//	return testingWriter{t:t}
//}
//
//func (w testingWriter) Write() {
//	w.t.Logf("%s", "1")
//}
//
//type Logger struct {
//	core Core
//}
//
//func (log *Logger) clone() *Logger {
//	copy := *log
//	return &copy
//}
//
//func (log *Logger) Check() *CheckedEntry {
//	ent := &CheckedEntry{}
//	ent.cores = append(ent.cores, log.core)
//	return ent
//}
//
//func NewLogger(t TestingT) *Logger {
//	writer := newTestingWriter(t)
//	return New(NewCore(writer))
//}
//
//func New(core Core) *Logger {
//	return &Logger{
//		core: core,
//	}
//}
//
//type Core interface {
//	Write()
//}
//
//type ioCore struct {
//	out WriteSyncer
//}
//
//func (c *ioCore) Write() {
//	c.out.Write()
//}
//
//func NewCore(ws WriteSyncer) Core {
//	return &ioCore{
//		out: ws,
//	}
//}
//
//func testing_TestLogger(t *testing.T) *SugaredLogger {
//	return NewLogger(t).Sugar()
//}
//
//func (log *Logger) Sugar() *SugaredLogger {
//	return &SugaredLogger{log.clone()}
//}
//
//type SugaredLogger struct {
//	base *Logger
//}
//
//func (s *SugaredLogger) log() {
//	ce := s.base.Check()
//	ce.Write()
//}
//
//func (s *SugaredLogger) Info(args ...interface{}) {
//	s.log()
//}
//
//type revisionWatcher struct {
//	logger    *SugaredLogger
//}
//
//func newRevisionWatcher(logger *SugaredLogger) *revisionWatcher {
//	return &revisionWatcher{
//		logger:logger,
//	}
//}
//
//func (rw *revisionWatcher) runWithTickCh() {
//	rw.checkDests()
//}
//
//func (rw *revisionWatcher) checkDests() {
//	go func() {
//		rw.logger.Info("1")
//	}()
//}
//
//func TestServing4908(t *testing.T) {
//	var wg sync.WaitGroup
//	wg.Add(1)
//	go func() {
//		defer wg.Done()
//		t.Run("TestServing4908", func(t *testing.T) {
//			rw := newRevisionWatcher(
//				testing_TestLogger(t),
//			)
//			var _wg sync.WaitGroup
//			_wg.Add(1)
//			go func() {
//				rw.runWithTickCh()
//				_wg.Done()
//			}()
//			_wg.Wait()
//		})
//	}()
//	wg.Wait()
//}
//
//func main() {
//	var t *testing.T
//	TestServing4908(t)
//}
//________________________________________________________________________________________
//TestServing6171
//package main
//
//import (
//	"testing"
//	"sync"
//)
//
//type TestingT interface {
//	Logf(string, ...interface{})
//}
//
//type WriteSyncer interface {
//	Write()
//}
//
//type CheckedEntry struct {
//	ErrorOutput WriteSyncer
//	cores       []Core
//}
//
//func (ce *CheckedEntry) Write() {
//	for i := range ce.cores {
//		ce.cores[i].Write()
//	}
//}
//type testingWriter struct {
//	t TestingT
//}
//
//func newTestingWriter(t TestingT) testingWriter {
//	return testingWriter{t:t}
//}
//
//func (w testingWriter) Write() {
//	w.t.Logf("%s", "1")
//}
//
//type Logger struct {
//	core Core
//}
//
//func (log *Logger) clone() *Logger {
//	copy := *log
//	return &copy
//}
//
//func (log *Logger) Check() *CheckedEntry {
//	ent := &CheckedEntry{}
//	ent.cores = append(ent.cores, log.core)
//	return ent
//}
//
//func NewLogger(t TestingT) *Logger {
//	writer := newTestingWriter(t)
//	return New(NewCore(writer))
//}
//
//func New(core Core) *Logger {
//	return &Logger{
//		core: core,
//	}
//}
//
//type Core interface {
//	Write()
//}
//
//type ioCore struct {
//	out WriteSyncer
//}
//
//func (c *ioCore) Write() {
//	c.out.Write()
//}
//
//func NewCore(ws WriteSyncer) Core {
//	return &ioCore{
//		out: ws,
//	}
//}
//
//func testing_TestLogger(t *testing.T) *SugaredLogger {
//	return NewLogger(t).Sugar()
//}
//
//func (log *Logger) Sugar() *SugaredLogger {
//	return &SugaredLogger{log.clone()}
//}
//
//type SugaredLogger struct {
//	base *Logger
//}
//
//func (s *SugaredLogger) log() {
//	ce := s.base.Check()
//	ce.Write()
//}
//
//func (s *SugaredLogger) Errorw(args ...interface{}) {
//	s.log()
//}
//
//type revisionWatcher struct {
//	logger    *SugaredLogger
//}
//
//func newRevisionWatcher(logger *SugaredLogger) *revisionWatcher {
//	return &revisionWatcher{
//		logger:logger,
//	}
//}
//
//func (rw *revisionWatcher) run() {
//	rw.checkDests()
//}
//
//func (rw *revisionWatcher) checkDests() {
//	go func() {
//		rw.logger.Errorw("1")
//	}()
//}
//
//type revisionBackendsManager struct {
//	logger *SugaredLogger
//}
//
//func (rbm *revisionBackendsManager) getOrCreateRevisionWatcher() {
//	rw := newRevisionWatcher(rbm.logger)
//	go rw.run()
//}
//
//func TestServing6171(t *testing.T) {
//	var wg sync.WaitGroup
//	wg.Add(1)
//	go func() {
//		defer wg.Done()
//		t.Run("TestServing6171", func(t *testing.T){
//			rbm := &revisionBackendsManager{logger:testing_TestLogger(t)}
//			rbm.getOrCreateRevisionWatcher()
//		})
//	}()
//	wg.Wait()
//}
//
//func main() {
//	var t *testing.T
//	TestServing6171(t)
//}
