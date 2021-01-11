package nodisk

import (
	"github.tamu.edu/April1989/go_tools/internal/lsp/foo"
)

func _() {
	foo.Foo() //@complete("F", Foo, IntFoo, StructFoo)
}
