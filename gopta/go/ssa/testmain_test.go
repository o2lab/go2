// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssa_test

// Tests of FindTests.  CreateTestMainPackage is tested via the interpreter.
// TODO(adonovan): test the 'pkgs' result from FindTests.

import (
	"fmt"
	"sort"
	"testing"

	"github.com/april1989/origin-go-tools/go/loader"
	"github.com/april1989/origin-go-tools/go/ssa"
	"github.com/april1989/origin-go-tools/go/ssa/ssautil"
)

func create(t *testing.T, content string) *ssa.Package {
	var conf loader.Config
	f, err := conf.ParseFile("foo_test.go", content)
	if err != nil {
		t.Fatal(err)
	}
	conf.CreateFromFiles("foo", f)

	lprog, err := conf.Load()
	if err != nil {
		t.Fatal(err)
	}

	// We needn't call Build.
	foo := lprog.Package("foo").Pkg
	return ssautil.CreateProgram(lprog, ssa.SanityCheckFunctions).Package(foo)
}

func TestFindTests(t *testing.T) {
	test := `
package foo

import "golibexec_testing"

type T int

// Tests:
func Test(t *golibexec_testing.T) {}
func TestA(t *golibexec_testing.T) {}
func TestB(t *golibexec_testing.T) {}

// Not tests:
func testC(t *golibexec_testing.T) {}
func TestD() {}
func testE(t *golibexec_testing.T) int { return 0 }
func (T) Test(t *golibexec_testing.T) {}

// Benchmarks:
func Benchmark(*golibexec_testing.B) {}
func BenchmarkA(b *golibexec_testing.B) {}
func BenchmarkB(*golibexec_testing.B) {}

// Not benchmarks:
func benchmarkC(t *golibexec_testing.T) {}
func BenchmarkD() {}
func benchmarkE(t *golibexec_testing.T) int { return 0 }
func (T) Benchmark(t *golibexec_testing.T) {}

// Examples:
func Example() {}
func ExampleA() {}

// Not examples:
func exampleC() {}
func ExampleD(t *golibexec_testing.T) {}
func exampleE() int { return 0 }
func (T) Example() {}
`
	pkg := create(t, test)
	tests, benchmarks, examples, _ := ssa.FindTests(pkg)

	sort.Sort(funcsByPos(tests))
	if got, want := fmt.Sprint(tests), "[foo.Test foo.TestA foo.TestB]"; got != want {
		t.Errorf("FindTests.tests = %s, want %s", got, want)
	}

	sort.Sort(funcsByPos(benchmarks))
	if got, want := fmt.Sprint(benchmarks), "[foo.Benchmark foo.BenchmarkA foo.BenchmarkB]"; got != want {
		t.Errorf("FindTests.benchmarks = %s, want %s", got, want)
	}

	sort.Sort(funcsByPos(examples))
	if got, want := fmt.Sprint(examples), "[foo.Example foo.ExampleA]"; got != want {
		t.Errorf("FindTests examples = %s, want %s", got, want)
	}
}

func TestFindTestsTesting(t *testing.T) {
	test := `
package foo

// foo does not import "golibexec_testing", but defines Examples.

func Example() {}
func ExampleA() {}
`
	pkg := create(t, test)
	tests, benchmarks, examples, _ := ssa.FindTests(pkg)
	if len(tests) > 0 {
		t.Errorf("FindTests.tests = %s, want none", tests)
	}
	if len(benchmarks) > 0 {
		t.Errorf("FindTests.benchmarks = %s, want none", benchmarks)
	}
	sort.Sort(funcsByPos(examples))
	if got, want := fmt.Sprint(examples), "[foo.Example foo.ExampleA]"; got != want {
		t.Errorf("FindTests examples = %s, want %s", got, want)
	}
}

type funcsByPos []*ssa.Function

func (p funcsByPos) Len() int           { return len(p) }
func (p funcsByPos) Less(i, j int) bool { return p[i].Pos() < p[j].Pos() }
func (p funcsByPos) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }
