// Package packagesinternal exposes internal-only fields from go/packages.
package packagesinternal

import (
	"github.tamu.edu/April1989/go_tools/internal/gocommand"
)

var GetForTest = func(p interface{}) string { return "" }

var GetGoCmdRunner = func(config interface{}) *gocommand.Runner { return nil }

var SetGoCmdRunner = func(config interface{}, runner *gocommand.Runner) {}

var TypecheckCgo int
