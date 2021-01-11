// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gopls_test

import (
	"os"
	"testing"

	"github.tamu.edu/April1989/go_tools/go/packages/packagestest"
	"github.tamu.edu/April1989/go_tools/gopls/internal/hooks"
	cmdtest "github.tamu.edu/April1989/go_tools/internal/lsp/cmd/test"
	"github.tamu.edu/April1989/go_tools/internal/lsp/source"
	"github.tamu.edu/April1989/go_tools/internal/testenv"
)

func TestMain(m *testing.M) {
	testenv.ExitIfSmallMachine()
	os.Exit(m.Run())
}

func TestCommandLine(t *testing.T) {
	packagestest.TestAll(t,
		cmdtest.TestCommandLine(
			"../../internal/lsp/testdata",
			commandLineOptions,
		),
	)
}

func commandLineOptions(options *source.Options) {
	options.StaticCheck = true
	options.GoDiff = false
	hooks.Options(options)
}
