package a

import (
	_ "github.tamu.edu/April1989/go_tools/internal/lsp/circular/triple/b" //@diag("_ \"github.tamu.edu/April1989/go_tools/internal/lsp/circular/triple/b\"", "compiler", "import cycle not allowed", "error")
)
