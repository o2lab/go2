// The shadow command runs the shadow analyzer.
package main

import (
	"github.tamu.edu/April1989/go_tools/go/analysis/passes/shadow"
	"github.tamu.edu/April1989/go_tools/go/analysis/singlechecker"
)

func main() { singlechecker.Main(shadow.Analyzer) }
