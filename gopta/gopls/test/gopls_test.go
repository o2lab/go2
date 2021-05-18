// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gopls_test

import (
	"os"
	"testing"

	"github.com/april1989/origin-go-tools/go/packages/packagestest"
	"github.com/april1989/origin-go-tools/gopls/internal/hooks"
	cmdtest "github.com/april1989/origin-go-tools/internal/lsp/cmd/test"
	"github.com/april1989/origin-go-tools/internal/lsp/source"
	"github.com/april1989/origin-go-tools/internal/testenv"
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
