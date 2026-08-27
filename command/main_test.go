package command

import (
	"os"
	"testing"

	"github.com/alvnukov/cozy-tools/testhome"
)

// The helper falls back to $HOME for command history and its project store, so
// without this the suite would read and prune the real one; see internal/testhome.
func TestMain(m *testing.M) {
	os.Exit(testhome.Use(m))
}
