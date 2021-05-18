package errors

import (
	"github.com/april1989/origin-go-tools/internal/lsp/types"
)

func _() {
	bob.Bob() //@complete(".")
	types.b //@complete(" //", Bob_interface)
}
