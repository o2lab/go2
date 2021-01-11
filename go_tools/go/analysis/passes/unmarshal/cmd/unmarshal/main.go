// The unmarshal command runs the unmarshal analyzer.
package main

import (
	"github.tamu.edu/April1989/go_tools/go/analysis/passes/unmarshal"
	"github.tamu.edu/April1989/go_tools/go/analysis/singlechecker"
)

func main() { singlechecker.Main(unmarshal.Analyzer) }
