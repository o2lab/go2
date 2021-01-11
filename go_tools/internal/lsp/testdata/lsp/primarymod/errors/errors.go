package errors

import (
	"github.tamu.edu/April1989/go_tools/internal/lsp/types"
)

func _() {
	bob.Bob() //@complete(".")
	types.b //@complete(" //", Bob_interface)
}
