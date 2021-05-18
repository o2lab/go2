package links

import (
	"fmt" //@link(`fmt`,"https://pkg.go.dev/fmt")
	errors "golang.org/x/xerrors"

	"github.com/april1989/origin-go-tools/internal/lsp/testdata/lsp/primarymod/foo" //@link(`github.com/april1989/origin-go-tools/internal/lsp/foo`,`https://pkg.go.dev/github.com/april1989/origin-go-tools/internal/lsp/foo`)

	_ "database/sql" //@link(`database/sql`, `https://pkg.go.dev/database/sql`)

	//bz: this is missing
	//_ "example.com/extramodule/pkg" //@link(`example.com/extramodule/pkg`,`https://pkg.go.dev/example.com/extramodule@v1.0.0/pkg`)//bz: does not matter, comment off
)

var (
	_ fmt.Formatter
	_ foo.StructFoo
	_ errors.Formatter
)

// Foo function
func Foo() string {
	/*https://example.com/comment */ //@link("https://example.com/comment","https://example.com/comment")

	url := "https://example.com/string_literal" //@link("https://example.com/string_literal","https://example.com/string_literal")
	return url

	// TODO(golang/go#1234): Link the relevant issue. //@link("golang/go#1234", "https://github.com/golang/go/issues/1234")
	// TODO(microsoft/vscode-go#12): Another issue. //@link("microsoft/vscode-go#12", "https://github.com/microsoft/vscode-go/issues/12")
}
