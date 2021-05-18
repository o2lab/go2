// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package incoming

import "github.com/april1989/origin-go-tools/internal/lsp/testdata/lsp/primarymod/callhierarchy"

// A is exported to test incoming calls across packages
func A() { //@mark(incomingA, "A")
	callhierarchy.D()
}
