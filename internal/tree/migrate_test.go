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
	if _, err := os.Stat(filepath.Join(profileDir, migrationMarkerName)); err != nil {
		t.Fatalf("migration marker missing: %v", err)
	}
}

func TestMigrateLegacyDataResumesPartialProfileWithoutOverwritingDestination(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	tmpAI := t.TempDir()
	t.Setenv("AI_DATA_HOME", tmpAI)

	legacyDir := filepath.Join(tmpHome, ".automata")
	if err := os.MkdirAll(filepath.Join(legacyDir, "sessions", "chat"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "state.json"), []byte(`{"version":1,"items":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "sessions", "chat", "plans.json"), []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}

	defaultDir := paths.ProfileDir("")
	if err := os.MkdirAll(filepath.Join(defaultDir, "sessions", "chat"), 0o755); err != nil {
		t.Fatal(err)
	}
	stateConflict := filepath.Join(defaultDir, "state.json")
	if err := os.Mkdir(stateConflict, 0o755); err != nil {
		t.Fatal(err)
	}
	userFile := filepath.Join(defaultDir, "sessions", "chat", "plans.json")
	if err := os.WriteFile(userFile, []byte(`{"user":"preserve"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := migrateLegacyData(""); err == nil {
		t.Fatal("migration unexpectedly succeeded with destination type conflict")
	}
	if _, err := os.Stat(filepath.Join(defaultDir, migrationMarkerName)); !os.IsNotExist(err) {
		t.Fatalf("partial migration wrote marker: %v", err)
	}
	if err := os.Remove(stateConflict); err != nil {
		t.Fatal(err)
	}

	if err := migrateLegacyData(""); err != nil {
		t.Fatalf("resumed migration failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(defaultDir, migrationMarkerName)); err != nil {
		t.Fatalf("resumed migration marker missing: %v", err)
	}
	state, err := os.ReadFile(filepath.Join(defaultDir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(state) != `{"version":1,"items":[]}` {
		t.Fatalf("migrated state = %q", state)
	}
	preserved, err := os.ReadFile(userFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(preserved) != `{"user":"preserve"}` {
		t.Fatalf("destination user file was overwritten: %q", preserved)
	}
}
