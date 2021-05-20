// TestKubernetes81091
package gorace_test

import (
	"fmt"
	"sync"
	"testing"
)

type FakeFilterPlugin struct {
	numFilterCalled int
}

func (fp *FakeFilterPlugin) Filter() {
	fp.numFilterCalled /* RACE Read */ /* RACE Write*/ ++ // racy read and write
}

type FilterPlugin interface {
	Filter()
}

type framework struct {
	filterPlugins []FilterPlugin
}

func (f *framework) RunFilterPlugins() {
	for _, pl := range f.filterPlugins {
		pl.Filter() // will lead to racy pair
	}
}

type Framework interface {
	RunFilterPlugins()
}

func NewFramework() Framework {
	f := &framework{}
	f.filterPlugins = append(f.filterPlugins, &FakeFilterPlugin{})
	return f
}

type genericScheduler struct {
	framework Framework
}

func NewGenericScheduler(framework Framework) *genericScheduler {
	return &genericScheduler{
		framework: framework,
	}
}

func (g *genericScheduler) findNodesThatFit() {
	checkNode := func(i int) {
		g.framework.RunFilterPlugins() // will lead to racy pair
	}
	ParallelizeUntil(2, 2, checkNode)
}

func (g *genericScheduler) Schedule() {
	g.findNodesThatFit() // will lead to race
}

type DoWorkPieceFunc func(piece int)

func ParallelizeUntil(workers, pieces int, doWorkPiece DoWorkPieceFunc) {
	var stop <-chan struct{}

	toProcess := make(chan int, pieces)
	for i := 0; i < pieces; i++ {
		toProcess <- i
	}
	close(toProcess)

	if pieces < workers {
		workers = pieces
	}

	wg := sync.WaitGroup{}
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for piece := range toProcess {
				select {
				case <-stop:
					return
				default:
					fmt.Println("check")
					doWorkPiece(piece)
				}
			}
		}()
	}
	wg.Wait()
}

func TestKubernetes81091(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		filterFramework := NewFramework()
		scheduler := NewGenericScheduler(filterFramework)
		scheduler.Schedule() // Will lead to race
	}()
	wg.Wait()
}
func main() {
	var t *testing.T
	TestKubernetes81091(t)
}
