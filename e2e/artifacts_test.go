package e2e

import (
	"os"
	"path/filepath"

	"github.com/starframe-dev/cue-tty/pkg/cue"
)

func saveTestArtifact(page *cue.Page, name string) error {
	dir := os.Getenv("AUTOMATA_E2E_ARTIFACTS_DIR")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "automata-e2e-artifacts")
	}
	return page.SaveArtifact(dir, name)
}
