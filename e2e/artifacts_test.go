package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/starframe-dev/cue-tty/pkg/cue"
)

func saveTestArtifact(page *cue.Page, name string) error {
	dir := os.Getenv("AUTOMATA_E2E_ARTIFACTS_DIR")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "automata-e2e-artifacts")
	}
	return page.SaveArtifact(dir, name)
}

func requireCue(t *testing.T, action string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", action, err)
	}
}

func closeCueApp(t *testing.T, app *cue.App) {
	t.Helper()
	if err := app.Close(); err != nil {
		t.Errorf("close cue app: %v", err)
	}
}
