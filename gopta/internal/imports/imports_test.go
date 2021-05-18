package imports

import (
	"os"
	"testing"

	"github.com/april1989/origin-go-tools/internal/testenv"
)

func TestMain(m *testing.M) {
	testenv.ExitIfSmallMachine()
	os.Exit(m.Run())
}
