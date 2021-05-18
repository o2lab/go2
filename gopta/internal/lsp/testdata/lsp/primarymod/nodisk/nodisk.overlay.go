package nodisk

import (
	"github.com/april1989/origin-go-tools/internal/lsp/foo"
)

func _() {
	foo.Foo() //@complete("F", Foo, IntFoo, StructFoo)
}
