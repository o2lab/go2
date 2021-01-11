// The nilness command applies the github.tamu.edu/April1989/go_tools/go/analysis/passes/nilness
// analysis to the specified packages of Go source code.
package main

import (
	"github.tamu.edu/April1989/go_tools/go/analysis/passes/nilness"
	"github.tamu.edu/April1989/go_tools/go/analysis/singlechecker"
)

func main() { singlechecker.Main(nilness.Analyzer) }
