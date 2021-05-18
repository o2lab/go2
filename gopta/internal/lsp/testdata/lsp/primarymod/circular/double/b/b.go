package b

import (
	_ "github.com/april1989/origin-go-tools/internal/lsp/circular/double/one" //@diag("_ \"github.com/april1989/origin-go-tools/internal/lsp/circular/double/one\"", "compiler", "import cycle not allowed", "error"),diag("\"github.com/april1989/origin-go-tools/internal/lsp/circular/double/one\"", "compiler", "could not import github.com/april1989/origin-go-tools/internal/lsp/circular/double/one (no package for import github.com/april1989/origin-go-tools/internal/lsp/circular/double/one)", "error")
)
