// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package hooks_test

import (
	"testing"

	"github.com/april1989/origin-go-tools/gopls/internal/hooks"
	"github.com/april1989/origin-go-tools/internal/lsp/diff/difftest"
)

func TestDiff(t *testing.T) {
	difftest.DiffTest(t, hooks.ComputeEdits)
}
