package imports

import (
	"os"
	"testing"

	"github.tamu.edu/April1989/go_tools/internal/testenv"
)

func TestMain(m *testing.M) {
	testenv.ExitIfSmallMachine()
	os.Exit(m.Run())
}
