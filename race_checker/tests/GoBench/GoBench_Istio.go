//TestIstio8144
//package main
//
//import (
//	"sync"
//	"testing"
//)
//
//type EvictionCallback func()
//
//type callbackRecorder struct {
//	callbacks int
//}
//
//func (c *callbackRecorder) callback() {
//	c.callbacks++ // racy write on callbacks field
//}
//
//type ttlCache struct {
//	entries sync.Map
//	callback func()
//}
//
//func (c *ttlCache) evicter() {
//	c.evictExpired() // will trigger racy write
//}
//
//func (c *ttlCache) evictExpired() {
//	c.entries.Range(func(key interface{}, value interface{}) bool {
//		c.callback() // calls local method
//		return true
//	})
//}
//
//func (c *ttlCache) SetWithExpiration(key interface{}, value interface{}) {
//	c.entries.Store(key, value)
//}
//
//func NewTTLWithCallback(callback EvictionCallback) *ttlCache{
//	c := &ttlCache{
//		callback: callback,
//	}
//	go c.evicter() // goroutine that triggers racy write
//	return c
//}
//
//func TestIstio8144(t *testing.T) {
//	var wg sync.WaitGroup
//	wg.Add(1)
//	go func() {
//		defer wg.Done()
//		c := &callbackRecorder{callbacks: 0}
//		ttl := NewTTLWithCallback(c.callback) // spawns child goroutine that triggers racy write
//		ttl.SetWithExpiration(1,1)
//		if c.callbacks != 1 {} // racy read on callbacks field
//	}()
//	wg.Wait()
//}
//
//func main() {
//	var t *testing.T
//	TestIstio8144(t)
//}
//_________________________________________________________________________________________________
// TestIstio8214
//package main
//
//import (
//	"sync"
//	"sync/atomic"
//	"testing"
//)
//
//type internal_Cache interface {
//	Set()
//	Stats() Stats
//}
//
//type ExpiringCache interface {
//	internal_Cache
//	SetWithExpiration()
//}
//
//type Cache struct {
//	cache ExpiringCache
//}
//
//func (cc *Cache) Set() {
//	cc.cache.SetWithExpiration()
//	cc.recordStats()
//}
//
//func (cc *Cache) recordStats() {
//	cc.cache.Stats()
//}
//
//type Stats struct {
//	Writes uint64
//}
//
//type lruCache struct {
//	stats Stats
//}
//
//func (c *lruCache) Stats() Stats {
//	return c.stats // racy read on stats field
//}
//
//func (c *lruCache) Set() {
//	c.SetWithExpiration()
//}
//
//func (c *lruCache) SetWithExpiration() {
//	atomic.AddUint64(&c.stats.Writes, 1) // racy write on stats field
//}
//
//type grpcServer struct {
//	cache *Cache
//}
//
//func (s *grpcServer) check() {
//	if s.cache != nil {
//		s.cache.Set()
//	}
//}
//
//func (s *grpcServer) Check() {
//	s.check()
//}
//
//func TestIstio8214(t *testing.T) {
//	var wg sync.WaitGroup
//	wg.Add(3)
//	go func() {
//		defer wg.Done()
//		s := &grpcServer{
//			cache:&Cache{
//				cache: &lruCache{},
//			},
//		}
//		go func() {
//			defer wg.Done()
//			s.Check() // triggers racy read
//		}()
//		go func() {
//			defer wg.Done()
//			s.Check() // triggers racy write
//		}()
//	}()
//	wg.Wait()
//}
//
//func main() {
//	var t *testing.T
//	TestIstio8214(t)
//}
//_________________________________________________________________________________________
//TestIstio8967 - PASS
package main

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
			case <-s.donec: // racy read on donec field
				return
			}
		}
	}()
}

func (s *fsSource) Stop() {
	close(s.donec)
	s.donec = nil // racy write on donec field
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

//___________________________________________________________________________________________
//TestIstio16742
//package main
//
//import (
//	"testing"
//	"sync"
//)
//
//var (
//	adsClients      = map[string]*XdsConnection{}
//	adsClientsMutex sync.RWMutex
//)
//
//type Collection []struct{}
//
//func BuildSidecarVirtualHostsFromConfigAndRegistry(proxyLabels Collection) {}
//
//type ConfigGenerator interface {
//	BuildHTTPRoutes(node *Proxy)
//}
//
//type ConfigGeneratorImpl struct {}
//
//func (configgen *ConfigGeneratorImpl) BuildHTTPRoutes(node *Proxy) {
//	configgen.buildSidecarOutboundHTTPRouteConfig(node)
//}
//
//func (configgen *ConfigGeneratorImpl) buildSidecarOutboundHTTPRouteConfig(node *Proxy) {
//	BuildSidecarVirtualHostsFromConfigAndRegistry(node.WorkloadLabels) // racy read on WorkloadLabels
//}
//
//type Proxy struct {
//	WorkloadLabels Collection
//}
//
//type XdsConnection struct {
//	modelNode *Proxy
//}
//
//func newXdsConnection() *XdsConnection {
//	return &XdsConnection{
//		modelNode: &Proxy{},
//	}
//}
//
//type DiscoveryServer struct {
//	ConfigGenerator ConfigGenerator
//}
//
//func (s *DiscoveryServer) addCon(con *XdsConnection) {
//	adsClientsMutex.Lock()
//	defer adsClientsMutex.Unlock()
//	adsClients["1"] = con
//}
//
//func (s *DiscoveryServer) StreamAggregatedResources() {
//	con := newXdsConnection()
//	s.addCon(con)
//	s.pushRoute(con)
//}
//
//func (s *DiscoveryServer) generateRawRoutes(con *XdsConnection) {
//	s.ConfigGenerator.BuildHTTPRoutes(con.modelNode)
//}
//
//func (s *DiscoveryServer) pushRoute(con *XdsConnection) {
//	s.generateRawRoutes(con)
//}
//
//func (s *DiscoveryServer) WorkloadUpdate() {
//	adsClientsMutex.RLock()
//	for _, connection := range adsClients {
//		connection.modelNode.WorkloadLabels = nil // racy write on WorkloadLabels
//	}
//	adsClientsMutex.RUnlock()
//}
//
//type XDSUpdater interface {
//	WorkloadUpdate()
//}
//
//type MemServiceDiscovery struct {
//	EDSUpdater XDSUpdater
//}
//
//func (sd *MemServiceDiscovery) AddWorkload() {
//	sd.EDSUpdater.WorkloadUpdate()
//}
//
//func TestIstio16742(t *testing.T) {
//	var wg sync.WaitGroup
//	wg.Add(3)
//	go func() {
//		defer wg.Done()
//		registry := &MemServiceDiscovery{
//			EDSUpdater:&DiscoveryServer{
//				ConfigGenerator: &ConfigGeneratorImpl{},
//			},
//		}
//		go func() {
//			defer wg.Done()
//			registry.EDSUpdater.(*DiscoveryServer).StreamAggregatedResources() // triggers racy read
//		}()
//		go func() {
//			defer wg.Done()
//			registry.AddWorkload() // triggers racy write
//		}()
//	}()
//	wg.Wait()
//}
//
//func main() {
//	var t *testing.T
//	TestIstio16742(t)
//}
