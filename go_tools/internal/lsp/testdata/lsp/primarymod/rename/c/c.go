package c

import "github.tamu.edu/April1989/go_tools/internal/lsp/rename/b"

func _() {
	b.Hello() //@rename("Hello", "Goodbye")
}
