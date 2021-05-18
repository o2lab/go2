// TestKubernetes80284
package main

import (
	"sync"
	"testing"
)

type Dialer struct{}

func (d *Dialer) CloseAll() {}

func NewDialer() *Dialer {
	return &Dialer{}
}

type Authenticator struct {
	onRotate func()
}

func (a *Authenticator) UpdateTransportConfig() {
	d := NewDialer()
	a.onRotate /* RACE Write */ /* RACE Write */ = d.CloseAll // repeated write to onRotate
}

func newAuthenticator() *Authenticator {
	return &Authenticator{}
}

func TestKubernetes80284(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(2)
	a := newAuthenticator()
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			a.UpdateTransportConfig() // triggers repeated write
		}()
	}
	wg.Wait()
}

func main() {
	var t *testing.T
	TestKubernetes80284(t)
}
