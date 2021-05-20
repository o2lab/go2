// TestGrpc3090
package gorace_test

import (
	"sync"
	"testing"
	"time"
)

type resolver_ClientConn interface {
	UpdateState()
}

type resolver_Resolver struct {
	CC resolver_ClientConn
}

func (r *resolver_Resolver) Build(cc resolver_ClientConn) Resolver {
	r.CC = cc
	r.UpdateState()
	return r
}

func (r *resolver_Resolver) ResolveNow() {
}

func (r *resolver_Resolver) UpdateState() {
	r.CC.UpdateState()
}

type Resolver interface {
	ResolveNow()
}

type ccResolverWrapper struct {
	cc       *ClientConn
	resolver Resolver
	mu       sync.Mutex
}

func (ccr *ccResolverWrapper) resolveNow() {
	ccr.mu.Lock()
	resolver := ccr.resolver /* RACE Read */ // racy read on resolver field
	resolver.ResolveNow()
	ccr.mu.Unlock()
}

func (ccr *ccResolverWrapper) poll() {
	ccr.mu.Lock()
	defer ccr.mu.Unlock()
	go func() {
		ccr.resolveNow()
	}()
}

func (ccr *ccResolverWrapper) UpdateState() {
	ccr.poll()
}

func newCCResolverWrapper(cc *ClientConn) {
	rb := cc.dopts.resolverBuilder
	ccr := &ccResolverWrapper{}
	ccr.resolver /* RACE Write */ = rb.Build(ccr) // racy write on resolver field,
	// the Build method will later spawn child goroutine and trigger racy read
}

type Builder interface {
	Build(cc resolver_ClientConn) Resolver
}

type dialOptions struct {
	resolverBuilder Builder
}

type ClientConn struct {
	dopts dialOptions
}

func DialContext() {
	cc := &ClientConn{
		dopts: dialOptions{},
	}
	if cc.dopts.resolverBuilder == nil {
		cc.dopts.resolverBuilder = &resolver_Resolver{}
	}
	newCCResolverWrapper(cc)
}
func Dial() {
	DialContext()
}

func TestGrpc3090(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		Dial() // triggers racy read and write
		time.Sleep(5 * time.Millisecond)
	}()
	wg.Wait()
}

func main() {
	var t *testing.T
	TestGrpc3090(t)
}
