// The unmarshal command runs the unmarshal analyzer.
package main

import (
	"github.com/april1989/origin-go-tools/go/analysis/passes/unmarshal"
	"github.com/april1989/origin-go-tools/go/analysis/singlechecker"
)

func main() { singlechecker.Main(unmarshal.Analyzer) }
