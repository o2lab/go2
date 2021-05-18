package sortslice_test

import (
	"testing"

	"github.com/april1989/origin-go-tools/go/analysis/analysistest"
	"github.com/april1989/origin-go-tools/go/analysis/passes/sortslice"
)

func Test(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, sortslice.Analyzer, "a")
}
