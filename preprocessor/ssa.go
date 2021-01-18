package preprocessor

import (
	"golang.org/x/tools/go/ssa"
)

// SyntheticDeferred represents a manually injected ssa.Deferred instruction in
// the preprocessing phase. Data flow analysis then treats a SyntheticDeferred as
// a normal ssa.Call.
type SyntheticDeferred struct {
	*ssa.Defer
}

