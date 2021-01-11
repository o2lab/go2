// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package unreachable_test

import (
	"testing"

	"github.tamu.edu/April1989/go_tools/go/analysis/analysistest"
	"github.tamu.edu/April1989/go_tools/go/analysis/passes/unreachable"
)

func Test(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.RunWithSuggestedFixes(t, testdata, unreachable.Analyzer, "a")
}
