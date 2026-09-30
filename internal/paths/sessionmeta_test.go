package paths

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureSessionDirCreatesVersionOneManifest(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const profile = "versioned"
	const sessionID = "versioned__chat"

	if err := EnsureSessionDir(profile, sessionID); err != nil {
		t.Fatal(err)
	}
	version, err := ReadSessionSchemaVersion(profile, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if version != CurrentSessionSchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, CurrentSessionSchemaVersion)
	}

	manifestPath := filepath.Join(SessionDir(profile, sessionID), "session.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest SessionManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != CurrentSessionSchemaVersion {
		t.Fatalf("manifest = %+v", manifest)
	}
	info, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != PrivateFileMode {
		t.Fatalf("manifest mode = %04o, want %04o", got, PrivateFileMode)
	}
}

func TestEnsureSessionDirPreservesLegacyDirectoryWithoutManifest(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const profile = "legacy"
	const sessionID = "legacy__chat"
	dir := SessionDir(profile, sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(dir, "notes.json")
	legacyData := []byte(`[{"title":"preserved"}]`)
	if err := os.WriteFile(legacyPath, legacyData, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := EnsureSessionDir(profile, sessionID); err != nil {
		t.Fatal(err)
	}
	version, err := ReadSessionSchemaVersion(profile, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if version != 0 {
		t.Fatalf("legacy schema version = %d, want 0", version)
	}
	if _, err := os.Stat(filepath.Join(dir, "session.json")); !os.IsNotExist(err) {
		t.Fatalf("EnsureSessionDir created a manifest for a legacy session: %v", err)
	}
	got, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(legacyData) {
		t.Fatalf("legacy data changed: %q", got)
	}
}

func TestEnsureSessionDirRejectsCorruptAndFutureManifestsWithoutOverwriting(t *testing.T) {
	for _, test := range []struct {
		name string
		data string
		want string
	}{
		{name: "corrupt", data: "{", want: "decode session manifest"},
		{name: "future", data: `{"schemaVersion":2}`, want: "unsupported session schema version 2"},
		{name: "invalid zero", data: `{"schemaVersion":0}`, want: "unsupported session schema version 0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("AI_DATA_HOME", t.TempDir())
			const profile = "manifest-errors"
			const sessionID = "manifest-errors__chat"
			dir := SessionDir(profile, sessionID)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			manifestPath := filepath.Join(dir, "session.json")
			if err := os.WriteFile(manifestPath, []byte(test.data), 0o644); err != nil {
				t.Fatal(err)
			}

			err := EnsureSessionDir(profile, sessionID)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("EnsureSessionDir error = %v, want text %q", err, test.want)
			}
			got, readErr := os.ReadFile(manifestPath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(got) != test.data {
				t.Fatalf("manifest was overwritten: %q", got)
			}
		})
	}
}

func TestReadSessionSchemaVersionRejectsMissingDirectory(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	if _, err := ReadSessionSchemaVersion("missing", "missing__chat"); err == nil {
		t.Fatal("missing session directory was treated as legacy v0")
	}
}
