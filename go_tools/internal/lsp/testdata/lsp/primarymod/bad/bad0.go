// +build go1.11

package bad

import _ "github.tamu.edu/April1989/go_tools/internal/lsp/assign/internal/secret" //@diag("\"github.tamu.edu/April1989/go_tools/internal/lsp/assign/internal/secret\"", "compiler", "could not import github.tamu.edu/April1989/go_tools/internal/lsp/assign/internal/secret (invalid use of internal package github.tamu.edu/April1989/go_tools/internal/lsp/assign/internal/secret)", "error")

func stuff() { //@item(stuff, "stuff", "func()", "func")
	x := "heeeeyyyy"
	random2(x) //@diag("x", "compiler", "cannot use x (variable of type string) as int value in argument to random2", "error")
	random2(1) //@complete("dom", random, random2, random3)
	y := 3     //@diag("y", "compiler", "y declared but not used", "error")
}

type bob struct { //@item(bob, "bob", "struct{...}", "struct")
	x int
}

func _() {
	var q int
	_ = &bob{
		f: q, //@diag("f", "compiler", "unknown field f in struct literal", "error")
	}
}
