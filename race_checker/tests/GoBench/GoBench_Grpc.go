//TestGrpc1748 - PASS
package main

import (
	"sync"
	"testing"
	"time"
)

var minConnectTimeout = 10 * time.Second

var balanceMutex sync.Mutex // We add this for avoiding other data race

type Balancer interface {
	HandleResolvedAddrs()
}

type Builder interface {
	Build(cc balancer_ClientConn) Balancer
}

func newPickfirstBuilder() Builder {
	return &pickfirstBuilder{}
}

type pickfirstBuilder struct{}

func (*pickfirstBuilder) Build(cc balancer_ClientConn) Balancer {
	return &pickfirstBalancer{cc: cc}
}

type SubConn interface {
	Connect()
}

type balancer_ClientConn interface {
	NewSubConn() SubConn
}

type pickfirstBalancer struct {
	cc balancer_ClientConn
	sc SubConn
}

func (b *pickfirstBalancer) HandleResolvedAddrs() {
	b.sc = b.cc.NewSubConn()
	b.sc.Connect()
}

type pickerWrapper struct {
	mu sync.Mutex
}

type acBalancerWrapper struct {
	mu sync.Mutex
	ac *addrConn
}

type addrConn struct {
	cc   *ClientConn
	acbw SubConn
	mu   sync.Mutex
}

func (ac *addrConn) resetTransport() {
	_ = minConnectTimeout // racy read on minConnectTimeout
}

func (ac *addrConn) transportMonitor() {
	ac.resetTransport()
}

func (ac *addrConn) connect() {
	go func() {
		ac.transportMonitor()
	}()
}

func (acbw *acBalancerWrapper) Connect() {
	acbw.mu.Lock()
	defer acbw.mu.Unlock()
	acbw.ac.connect()
}

func newPickerWrapper() *pickerWrapper {
	return &pickerWrapper{}
}

type ClientConn struct {
	mu sync.Mutex
}

func (cc *ClientConn) switchBalancer() {
	builder := newPickfirstBuilder()
	newCCBalancerWrapper(cc, builder)
}

func (cc *ClientConn) newAddrConn() *addrConn {
	return &addrConn{cc: cc}
}

type ccBalancerWrapper struct {
	cc       *ClientConn
	balancer Balancer
}

func (ccb *ccBalancerWrapper) watcher() {
	for i := 0; i < 10; i++ {
		balanceMutex.Lock()
		if ccb.balancer != nil {
			balanceMutex.Unlock()
			ccb.balancer.HandleResolvedAddrs()
		} else {
			balanceMutex.Unlock()
		}
	}
}

func (ccb *ccBalancerWrapper) NewSubConn() SubConn {
	ac := ccb.cc.newAddrConn()
	acbw := &acBalancerWrapper{ac: ac}
	acbw.ac.mu.Lock()
	ac.acbw = acbw
	acbw.ac.mu.Unlock()
	return acbw
}

func newCCBalancerWrapper(cc *ClientConn, b Builder) {
	ccb := &ccBalancerWrapper{cc: cc}
	go ccb.watcher()
	balanceMutex.Lock()
	defer balanceMutex.Unlock()
	ccb.balancer = b.Build(ccb)
}

func TestGrpc1748(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		mctBkp := minConnectTimeout
		// Call this only after transportMonitor goroutine has ended.
		defer func() {
			minConnectTimeout = mctBkp // racy write on minConnectTimeout
		}()
		cc := &ClientConn{}
		cc.switchBalancer() // spawns child goroutine that later triggers racy read
	}()
	wg.Wait()
}

func main() {
	var t *testing.T
	TestGrpc1748(t)
}

//___________________________________________________________________________________________
//TestGrpc1862
//package main
//
//import (
//	"testing"
//	"sync"
//	"time"
//)
//
//func TestGrpc1862(t *testing.T) {
//	var wg sync.WaitGroup
//	wg.Add(1)
//	go func() {
//		defer wg.Done()
//		abort := false
//		time.AfterFunc(time.Nanosecond, func() { abort = true }) // spawns child goroutine in time package triggering racy write on abort
//		if abort {} // racy read on abort
//	}()
//	wg.Wait()
//}
//
//func main() {
//	var t *testing.T
//	TestGrpc1862(t)
//}
//__________________________________________________________________________________________________
//TestGrpc3090
//package main
//
//import (
//	"sync"
//	"testing"
//	"time"
//)
//
//type resolver_ClientConn interface {
//	UpdateState()
//}
//
//type resolver_Resolver struct {
//	CC resolver_ClientConn
//}
//
//func (r *resolver_Resolver) Build(cc resolver_ClientConn) Resolver {
//	r.CC = cc
//	r.UpdateState()
//	return r
//}
//
//func (r *resolver_Resolver) ResolveNow() {
//}
//
//func (r *resolver_Resolver) UpdateState() {
//	r.CC.UpdateState()
//}
//
//type Resolver interface {
//	ResolveNow()
//}
//
//type ccResolverWrapper struct {
//	cc       *ClientConn
//	resolver Resolver
//	mu       sync.Mutex
//}
//
//func (ccr *ccResolverWrapper) resolveNow() {
//	ccr.mu.Lock()
//	ccr.resolver.ResolveNow() // racy read on resolver field
//	ccr.mu.Unlock()
//}
//
//func (ccr *ccResolverWrapper) poll() {
//	ccr.mu.Lock()
//	defer ccr.mu.Unlock()
//	go func() {
//		ccr.resolveNow()
//	}()
//}
//
//func (ccr *ccResolverWrapper) UpdateState() {
//	ccr.poll()
//}
//
//func newCCResolverWrapper(cc *ClientConn) {
//	rb := cc.dopts.resolverBuilder
//	ccr := &ccResolverWrapper{}
//	ccr.resolver = rb.Build(ccr) // racy write on resolver field,
//	// the Build method will later spawn child goroutine and trigger racy read
//}
//
//type Builder interface {
//	Build(cc resolver_ClientConn) Resolver
//}
//
//type dialOptions struct {
//	resolverBuilder Builder
//}
//
//type ClientConn struct {
//	dopts dialOptions
//}
//
//func DialContext() {
//	cc := &ClientConn{
//		dopts: dialOptions{},
//	}
//	if cc.dopts.resolverBuilder == nil {
//		cc.dopts.resolverBuilder = &resolver_Resolver{}
//	}
//	newCCResolverWrapper(cc)
//}
//func Dial() {
//	DialContext()
//}
//
//func TestGrpc3090(t *testing.T) {
//	var wg sync.WaitGroup
//	wg.Add(1)
//	go func() {
//		defer wg.Done()
//		Dial() // triggers racy read and write
//		time.Sleep(5 * time.Millisecond)
//	}()
//	wg.Wait()
//}
//
//func main() {
//	var t *testing.T
//	TestGrpc3090(t)
//}
