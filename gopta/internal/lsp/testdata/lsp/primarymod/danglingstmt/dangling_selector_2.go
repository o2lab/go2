package danglingstmt

import "github.com/april1989/origin-go-tools/internal/lsp/foo"

func _() {
	foo. //@rank(" //", Foo)
	var _ = []string{foo.} //@rank("}", Foo)
}
