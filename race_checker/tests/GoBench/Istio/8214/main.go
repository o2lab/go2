// TestIstio8214
package main

import (
	"sync"
	"sync/atomic"
	"testing"
)

type internal_Cache interface {
	Set()
	Stats() Stats
}

type ExpiringCache interface {
	internal_Cache
	SetWithExpiration()
}

type Cache struct {
	cache ExpiringCache
}

func (cc *Cache) Set() {
	cc.cache.SetWithExpiration() // will trigger racy write
	cc.recordStats()             // will trigger racy read
}

func (cc *Cache) recordStats() {
	cc.cache.Stats() // will trigger racy read
}

type Stats struct {
	Writes uint64
}

type lruCache struct {
	stats Stats
}

func (c *lruCache) Stats() Stats {
	return c.stats /* RACE Read */
}

func (c *lruCache) Set() {
	c.SetWithExpiration()
}

func (c *lruCache) SetWithExpiration() {
	atomic.AddUint64 /* RACE Write */ (&c.stats.Writes, 1) // racy write on stats field
}

type grpcServer struct {
	cache *Cache
}

func (s *grpcServer) check() {
	if s.cache != nil {
		s.cache.Set() // will trigger racy read and write
	}
}

func (s *grpcServer) Check() {
	s.check()
}

func TestIstio8214(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		s := &grpcServer{
			cache: &Cache{
				cache: &lruCache{},
			},
		}
		go func() {
			defer wg.Done()
			s.Check() // triggers racy read
		}()
		go func() {
			defer wg.Done()
			s.Check() // triggers racy write
		}()
	}()
	wg.Wait()
}

func main() {
	var t *testing.T
	TestIstio8214(t)
}
