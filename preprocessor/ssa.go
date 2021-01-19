package preprocessor

import (
	"github.com/o2lab/go2/go/ssa"
)

// SyntheticDeferred represents a manually injected ssa.Deferred instruction in
// the preprocessing phase. Data flow analysis then treats a SyntheticDeferred as
// a normal ssa.Call.
type SyntheticDeferred struct {
	*ssa.Defer
}
