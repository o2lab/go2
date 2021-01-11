// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lsp

import (
	"context"

	"github.tamu.edu/April1989/go_tools/internal/event"
	"github.tamu.edu/April1989/go_tools/internal/lsp/debug/tag"
	"github.tamu.edu/April1989/go_tools/internal/lsp/protocol"
	"github.tamu.edu/April1989/go_tools/internal/lsp/source"
)

func (s *Server) documentHighlight(ctx context.Context, params *protocol.DocumentHighlightParams) ([]protocol.DocumentHighlight, error) {
	snapshot, fh, ok, release, err := s.beginFileRequest(ctx, params.TextDocument.URI, source.Go)
	defer release()
	if !ok {
		return nil, err
	}
	rngs, err := source.Highlight(ctx, snapshot, fh, params.Position)
	if err != nil {
		event.Error(ctx, "no highlight", err, tag.URI.Of(params.TextDocument.URI))
	}
	return toProtocolHighlight(rngs), nil
}

func toProtocolHighlight(rngs []protocol.Range) []protocol.DocumentHighlight {
	result := make([]protocol.DocumentHighlight, 0, len(rngs))
	kind := protocol.Text
	for _, rng := range rngs {
		result = append(result, protocol.DocumentHighlight{
			Kind:  kind,
			Range: rng,
		})
	}
	return result
}
