// The shadow command runs the shadow analyzer.
package main

import (
	"github.com/april1989/origin-go-tools/go/analysis/passes/shadow"
	"github.com/april1989/origin-go-tools/go/analysis/singlechecker"
)

func main() { singlechecker.Main(shadow.Analyzer) }
