package gorace_test

import (
	"fmt"
	"sync"
)

type Group struct {
	wg sync.WaitGroup
	x int
}

func main() {
	group := Group{
		x:  0,
	}
	group.Go()
	group.Waiting()
	fmt.Println(group.x)
}

func (g *Group) Waiting() {
	g.wg.Wait()
	g.x = 2
}
func (g *Group) Go() {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		g.x = 1
	}()
}