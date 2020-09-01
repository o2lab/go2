//TestEtcd4876 - PASS
package main

import (
	"sync"
	"testing"
	"time"
)

var ProgressReportInterval = 10 * time.Second

type Watcher interface {
	Watch()
}
type ServerStream interface{}

type Watch_WatchServer interface {
	Send()
	ServerStream
}
type watchWatchServer struct {
	ServerStream
}

func (x *watchWatchServer) Send() {}

type WatchServer interface {
	Watch(Watch_WatchServer)
}

type serverWatchStream struct{}

func (sws *serverWatchStream) sendLoop() {
	_ = time.NewTicker(ProgressReportInterval) // racy read on ProgressReportInterval
}

type watchServer struct{}

func (ws *watchServer) Watch(stream Watch_WatchServer) {
	sws := serverWatchStream{}
	go sws.sendLoop()
}

func TestEtcd4876(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		w := &watchServer{}
		go func() {
			defer wg.Done()
			testInterval := 3 * time.Second
			ProgressReportInterval = testInterval // racy write on ProgressReportInterval
		}()
		go func() {
			defer wg.Done()
			w.Watch(&watchWatchServer{}) // spawns child goroutine that later triggers racy read
		}()
	}()
	wg.Wait()
}

func main() {
	var t *testing.T
	TestEtcd4876(t)
}