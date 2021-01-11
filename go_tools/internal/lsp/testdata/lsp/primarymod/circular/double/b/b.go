package b

import (
	_ "github.tamu.edu/April1989/go_tools/internal/lsp/circular/double/one" //@diag("_ \"github.tamu.edu/April1989/go_tools/internal/lsp/circular/double/one\"", "compiler", "import cycle not allowed", "error"),diag("\"github.tamu.edu/April1989/go_tools/internal/lsp/circular/double/one\"", "compiler", "could not import github.tamu.edu/April1989/go_tools/internal/lsp/circular/double/one (no package for import github.tamu.edu/April1989/go_tools/internal/lsp/circular/double/one)", "error")
)
