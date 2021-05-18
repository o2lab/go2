// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package hooks

import (
	"github.com/april1989/origin-go-tools/internal/lsp/diff"
	"github.com/april1989/origin-go-tools/internal/span"
	"github.com/sergi/go-diff/diffmatchpatch"
)

func ComputeEdits(uri span.URI, before, after string) []diff.TextEdit {
	diffs := diffmatchpatch.New().DiffMain(before, after, true)
	edits := make([]diff.TextEdit, 0, len(diffs))
	offset := 0
	for _, d := range diffs {
		start := span.NewPoint(0, 0, offset)
		switch d.Type {
		case diffmatchpatch.DiffDelete:
			offset += len(d.Text)
			edits = append(edits, diff.TextEdit{Span: span.New(uri, start, span.NewPoint(0, 0, offset))})
		case diffmatchpatch.DiffEqual:
			offset += len(d.Text)
		case diffmatchpatch.DiffInsert:
			edits = append(edits, diff.TextEdit{Span: span.New(uri, start, span.Point{}), NewText: d.Text})
		}
	}
	return edits
}
