// The lostcancel command applies the github.tamu.edu/April1989/go_tools/go/analysis/passes/lostcancel
// analysis to the specified packages of Go source code.
package main

import (
	"github.tamu.edu/April1989/go_tools/go/analysis/passes/lostcancel"
	"github.tamu.edu/April1989/go_tools/go/analysis/singlechecker"
)

func main() { singlechecker.Main(lostcancel.Analyzer) }
