package c

import "github.com/april1989/origin-go-tools/internal/lsp/rename/b"

func _() {
	b.Hello() //@rename("Hello", "Goodbye")
}
