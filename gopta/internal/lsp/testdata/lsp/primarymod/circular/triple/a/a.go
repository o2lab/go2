package a

import (
	_ "github.com/april1989/origin-go-tools/internal/lsp/testdata/lsp/primarymod/circular/triple/b" //@diag("_ \"github.com/april1989/origin-go-tools/internal/lsp/circular/triple/b\"", "compiler", "import cycle not allowed", "error")
)
