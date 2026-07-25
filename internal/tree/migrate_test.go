package tree

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/HumanHorizon/automata/internal/paths"
)

func TestMigrateLegacyData(t *testing.T) {
	// Use a temporary home to avoid touching real ~/.automata.
	tmpHome := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", oldHome)

	// Use a temporary AI_DATA_HOME to isolate the new layout.
	tmpAI := t.TempDir()
	os.Setenv("AI_DATA_HOME", tmpAI)
	defer os.Unsetenv("AI_DATA_HOME")

	// Create legacy data.
	legacyDir := filepath.Join(tmpHome, ".automata")
	if err := os.MkdirAll(filepath.Join(legacyDir, "sessions", "s1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "state.json"), []byte(`{"version":1,"items":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "sessions", "s1", "settings.json"), []byte(`{"domain":"space"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Trigger migration.
	if err := migrateLegacyData(""); err != nil {
		t.Fatalf("migrateLegacyData failed: %v", err)
	}

	// Verify new layout.
	profileDir := paths.ProfileDir("")
	if _, err := os.Stat(profileDir); os.IsNotExist(err) {
		t.Fatalf("profile dir was not created: %s", profileDir)
	}
	statePath := paths.StatePath("")
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		t.Fatalf("state.json was not migrated: %s", statePath)
	}
	settingsPath := filepath.Join(paths.SessionDir("", "s1"), "settings.json")
	if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
		t.Fatalf("session settings were not migrated: %s", settingsPath)
	}

	// Running migration again should be a no-op.
	if err := migrateLegacyData(""); err != nil {
		t.Fatalf("second migrateLegacyData failed: %v", err)
	}
}
