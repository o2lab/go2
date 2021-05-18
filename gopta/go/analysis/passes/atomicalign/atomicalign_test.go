// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package atomicalign_test

import (
	"testing"

	"github.com/april1989/origin-go-tools/go/analysis/analysistest"
	"github.com/april1989/origin-go-tools/go/analysis/passes/atomicalign"
)

func Test(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, atomicalign.Analyzer, "a", "b")
}
