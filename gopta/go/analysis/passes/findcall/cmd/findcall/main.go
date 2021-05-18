// The findcall command runs the findcall analyzer.
package main

import (
	"github.com/april1989/origin-go-tools/go/analysis/passes/findcall"
	"github.com/april1989/origin-go-tools/go/analysis/singlechecker"
)

func main() { singlechecker.Main(findcall.Analyzer) }
