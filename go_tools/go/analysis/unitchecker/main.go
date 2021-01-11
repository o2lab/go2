// +build ignore

// This file provides an example command for static checkers
// conforming to the github.tamu.edu/April1989/go_tools/go/analysis API.
// It serves as a model for the behavior of the cmd/vet tool in $GOROOT.
// Being based on the unitchecker driver, it must be run by go vet:
//
//   $ go build -o unitchecker main.go
//   $ go vet -vettool=unitchecker my/project/...
//
// For a checker also capable of running standalone, use multichecker.
package main

import (
	"github.tamu.edu/April1989/go_tools/go/analysis/unitchecker"

	"github.tamu.edu/April1989/go_tools/go/analysis/passes/asmdecl"
	"github.tamu.edu/April1989/go_tools/go/analysis/passes/assign"
	"github.tamu.edu/April1989/go_tools/go/analysis/passes/atomic"
	"github.tamu.edu/April1989/go_tools/go/analysis/passes/bools"
	"github.tamu.edu/April1989/go_tools/go/analysis/passes/buildtag"
	"github.tamu.edu/April1989/go_tools/go/analysis/passes/cgocall"
	"github.tamu.edu/April1989/go_tools/go/analysis/passes/composite"
	"github.tamu.edu/April1989/go_tools/go/analysis/passes/copylock"
	"github.tamu.edu/April1989/go_tools/go/analysis/passes/errorsas"
	"github.tamu.edu/April1989/go_tools/go/analysis/passes/httpresponse"
	"github.tamu.edu/April1989/go_tools/go/analysis/passes/loopclosure"
	"github.tamu.edu/April1989/go_tools/go/analysis/passes/lostcancel"
	"github.tamu.edu/April1989/go_tools/go/analysis/passes/nilfunc"
	"github.tamu.edu/April1989/go_tools/go/analysis/passes/printf"
	"github.tamu.edu/April1989/go_tools/go/analysis/passes/shift"
	"github.tamu.edu/April1989/go_tools/go/analysis/passes/stdmethods"
	"github.tamu.edu/April1989/go_tools/go/analysis/passes/structtag"
	"github.tamu.edu/April1989/go_tools/go/analysis/passes/tests"
	"github.tamu.edu/April1989/go_tools/go/analysis/passes/unmarshal"
	"github.tamu.edu/April1989/go_tools/go/analysis/passes/unreachable"
	"github.tamu.edu/April1989/go_tools/go/analysis/passes/unsafeptr"
	"github.tamu.edu/April1989/go_tools/go/analysis/passes/unusedresult"
)

func main() {
	unitchecker.Main(
		asmdecl.Analyzer,
		assign.Analyzer,
		atomic.Analyzer,
		bools.Analyzer,
		buildtag.Analyzer,
		cgocall.Analyzer,
		composite.Analyzer,
		copylock.Analyzer,
		errorsas.Analyzer,
		httpresponse.Analyzer,
		loopclosure.Analyzer,
		lostcancel.Analyzer,
		nilfunc.Analyzer,
		printf.Analyzer,
		shift.Analyzer,
		stdmethods.Analyzer,
		structtag.Analyzer,
		tests.Analyzer,
		unmarshal.Analyzer,
		unreachable.Analyzer,
		unsafeptr.Analyzer,
		unusedresult.Analyzer,
	)
}
