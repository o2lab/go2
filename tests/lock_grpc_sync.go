package main

import (
	"fmt"
	"sync"
)

// SafeCounter is safe to use concurrently.
type SafeCounter struct {
	mu sync.Mutex
	v  map[string]int
}

// Inc increments the counter for the given key.
func (c *SafeCounter) Inc(key string) {
	// Lock so only one goroutine at a time can access the map c.v.
	c.Plus(key)
}

func (c *SafeCounter) Plus(key string) {
	c.v[key]++
}
// Value returns the current value of the counter for the given key.
func (c *SafeCounter) Value(key string) int {
	c.mu.Lock()
	// Lock so only one goroutine at a time can access the map c.v.
	defer c.mu.Unlock()
	return c.v[key]
}

func (c *SafeCounter) Error() {
	var vi = make(map[string] int)
	c.mu.Lock()
	c.v = vi
	c.mu.Unlock()
}
func main() {
	c := SafeCounter{v: make(map[string]int)}

	go c.Error()
	c.mu.Lock()
	c.Inc("somekey")
	c.mu.Unlock()

	fmt.Println(c.Value("somekey"))
}