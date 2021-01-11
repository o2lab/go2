// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lsp

import (
	"context"

	"github.tamu.edu/April1989/go_tools/internal/event"
	"github.tamu.edu/April1989/go_tools/internal/lsp/protocol"
	"github.tamu.edu/April1989/go_tools/internal/lsp/source"
)

func (s *Server) symbol(ctx context.Context, params *protocol.WorkspaceSymbolParams) ([]protocol.SymbolInformation, error) {
	ctx, done := event.Start(ctx, "lsp.Server.symbol")
	defer done()

	views := s.session.Views()
	matcher := s.session.Options().SymbolMatcher
	style := s.session.Options().SymbolStyle
	return source.WorkspaceSymbols(ctx, matcher, style, views, params.Query)
}
