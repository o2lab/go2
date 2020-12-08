package main

import (
	"context"
	"strings"
	"sync"
	"log"
	
)
func Dial(target string, opts ...DialOption) (*ClientConn, error) {
	return DialContext(context.Background(), target, opts...)
}
func (cc *ClientConn) updateResolverState(s resolver.State, err error) error {
	defer cc.firstResolveEvent.Fire()
	cc.mu.Lock()
	// Check if the ClientConn is already closed. Some fields (e.g.
	// balancerWrapper) are set to nil when closing the ClientConn, and could
	// cause nil pointer panic if we don't have this check.
	if cc.conns == nil {
		cc.mu.Unlock()
		return nil
	}

	cc.maybeApplyDefaultServiceConfig(nil)


	var ret error
	return ret
}
func (cc *ClientConn) maybeApplyDefaultServiceConfig(addrs []resolver.Address) {
	cc.applyServiceConfigAndBalancer(emptyServiceConfig, addrs)
}

func (cc *ClientConn) applyServiceConfigAndBalancer(sc *ServiceConfig, addrs []resolver.Address) {
	cc.switchBalancer(newBalancerName)
}
func (cc *ClientConn) switchBalancer(name string) {
	if strings.EqualFold(cc.curBalancerName, name) {
		return
	}

	channelz.Infof(logger, cc.channelzID, "ClientConn switching balancer to %q", name)
	if cc.dopts.balancerBuilder != nil {
		channelz.Info(logger, cc.channelzID, "ignoring balancer switching: Balancer DialOption used instead")
		return
	}
	if cc.balancerWrapper != nil {
		cc.balancerWrapper.close()
	}

	builder := balancer.Get(name)
	if builder == nil {
		channelz.Warningf(logger, cc.channelzID, "Channel switches to new LB policy %q due to fallback from invalid balancer name", PickFirstBalancerName)
		channelz.Infof(logger, cc.channelzID, "failed to get balancer builder for: %v, using pick_first instead", name)
		builder = newPickfirstBuilder()
	} else {
		channelz.Infof(logger, cc.channelzID, "Channel switches to new LB policy %q", name)
	}

	cc.curBalancerName = builder.Name()
	cc.balancerWrapper = newCCBalancerWrapper(cc, builder, cc.balancerBuildOpts)
}
type ccBalancerWrapper struct {
	cc         *ClientConn
	balancerMu sync.Mutex // synchronizes calls to the balancer
	balancer   balancer.Balancer
	scBuffer   *buffer.Unbounded
	done       *grpcsync.Event

	mu       sync.Mutex
	subConns map[*acBalancerWrapper]struct{}
}

func newCCBalancerWrapper(cc *ClientConn, b balancer.Builder, bopts balancer.BuildOptions) *ccBalancerWrapper {
	ccb := &ccBalancerWrapper{
		cc:       cc,
		scBuffer: buffer.NewUnbounded(),
		done:     grpcsync.NewEvent(),
		subConns: make(map[*acBalancerWrapper]struct{}),
	}
	go ccb.watcher()
	ccb.balancer = b.Build(ccb, bopts)
	return ccb
}

// watcher balancer functions sequentially, so the balancer can be implemented
// lock-free.
func (ccb *ccBalancerWrapper) watcher() {
	for {
		select {
		case t := <-ccb.scBuffer.Get():
			ccb.scBuffer.Load()
			if ccb.done.HasFired() {
				break
			}
			ccb.balancerMu.Lock()
			su := t.(*scStateUpdate)
			ccb.balancer.UpdateSubConnState(su.sc, balancer.SubConnState{ConnectivityState: su.state, ConnectionError: su.err})
			ccb.balancerMu.Unlock()
		case <-ccb.done.Done():
		}

		if ccb.done.HasFired() {
			ccb.balancer.Close()
			ccb.mu.Lock()
			scs := ccb.subConns
			ccb.subConns = nil
			ccb.mu.Unlock()
			for acbw := range scs {
				ccb.cc.removeAddrConn(acbw.getAddrConn(), errConnDrain)
			}
			ccb.UpdateState(balancer.State{ConnectivityState: connectivity.Connecting, Picker: nil})
			return
		}
	}
}
func main() {
	conn, err := grpc.Dial(address, grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	conn.Close()
}