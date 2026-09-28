package tree

import (
	"os"
	"testing"

	"github.com/HumanHorizon/automata/internal/paths"
)

func TestSaveStateUsesPrivatePermissions(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	tr := New()
	tr.Profile = "private-state"
	if _, err := tr.CreateTerminal("shell"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(paths.StatePath(tr.Profile))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != paths.PrivateFileMode {
		t.Fatalf("state mode = %o, want %o", got, paths.PrivateFileMode)
	}
}
