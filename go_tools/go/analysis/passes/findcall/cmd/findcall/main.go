// The findcall command runs the findcall analyzer.
package main

import (
	"github.tamu.edu/April1989/go_tools/go/analysis/passes/findcall"
	"github.tamu.edu/April1989/go_tools/go/analysis/singlechecker"
)

func main() { singlechecker.Main(findcall.Analyzer) }
