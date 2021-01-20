// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main_test

import (
	"testing"
)

type P struct {
	x, y int
}

type S struct {
	s1, s2 P
}

func TestNoRaceComp(t *testing.T) {
	c := make(chan bool, 1)
	var s S
	go func() {
		s.s2.x = 1
		c <- true
	}()
	s.s2.y = 2
	<-c
}

func TestNoRaceComp2(t *testing.T) {
	c := make(chan bool, 1)
	var s S
	go func() {
		s.s1.x = 1
		c <- true
	}()
	s.s1.y = 2
	<-c
}

func TestRaceComp(t *testing.T) {
	c := make(chan bool, 1)
	var s S
	go func() {
		s.s2.y = 1 // want `Write`
		c <- true
	}()
	s.s2.y = 2 // want `Write`
	<-c
}

func TestRaceComp2(t *testing.T) {
	c := make(chan bool, 1)
	var s S
	go func() {
		s.s1.x = 1 // want `Write`
		c <- true
	}()
	s = S{} // want `Write`
	<-c
}

func TestRaceComp3(t *testing.T) {
	c := make(chan bool, 1)
	var s S
	go func() {
		s.s2.y = 1 // want `Write`
		c <- true
	}()
	s = S{} // want `Write`
	<-c
}

func TestRaceCompArray(t *testing.T) {
	c := make(chan bool, 1)
	s := make([]S, 10)
	x := 4
	go func() {
		s[x].s2.y = 1 // want `Read`
		c <- true
	}()
	x = 5 // want `Write`
	<-c
}

type P2 P
type S2 S

func TestRaceConv1(t *testing.T) {
	c := make(chan bool, 1)
	var p P2
	go func() {
		p.x = 1 // want `Write`
		c <- true
	}()
	_ = P(p).x // want `Read`
	<-c
}

func TestRaceConv2(t *testing.T) {
	c := make(chan bool, 1)
	var p P2
	go func() {
		p.x = 1 // want `Write`
		c <- true
	}()
	ptr := &p
	_ = P(*ptr).x // want `Read`
	<-c
}

func TestRaceConv3(t *testing.T) {
	c := make(chan bool, 1)
	var s S2
	go func() {
		s.s1.x = 1 // want `Write`
		c <- true
	}()
	_ = P2(S(s).s1).x // want `Read`
	<-c
}

type X struct {
	V [4]P
}

type X2 X

func TestRaceConv4(t *testing.T) {
	c := make(chan bool, 1)
	var x X2
	go func() {
		x.V[1].x = 1 // want `Write`
		c <- true
	}()
	_ = P2(X(x).V[1]).x // want `Read`
	<-c
}

type Ptr struct {
	s1, s2 *P
}

func TestNoRaceCompPtr(t *testing.T) {
	c := make(chan bool, 1)
	p := Ptr{&P{}, &P{}}
	go func() {
		p.s1.x = 1
		c <- true
	}()
	p.s1.y = 2
	<-c
}

func TestNoRaceCompPtr2(t *testing.T) {
	c := make(chan bool, 1)
	p := Ptr{&P{}, &P{}}
	go func() {
		p.s1.x = 1
		c <- true
	}()
	_ = p
	<-c
}

func TestRaceCompPtr(t *testing.T) {
	c := make(chan bool, 1)
	p := Ptr{&P{}, &P{}}
	go func() {
		p.s2.x = 1 // want `Write`
		c <- true
	}()
	p.s2.x = 2 // want `Write`
	<-c
}

func TestRaceCompPtr2(t *testing.T) {
	c := make(chan bool, 1)
	p := Ptr{&P{}, &P{}}
	go func() {
		p.s2.x = 1 // want `Read`
		c <- true
	}()
	p.s2 = &P{} // want `Write`
	<-c
}
