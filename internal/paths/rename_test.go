package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenameDirectoryRejectsExistingTarget(t *testing.T) {
	base := t.TempDir()
	oldPath := filepath.Join(base, "old")
	newPath := filepath.Join(base, "new")
	if err := os.MkdirAll(oldPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(newPath, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := RenameDirectory(oldPath, newPath); err == nil {
		t.Fatal("expected existing target error")
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("source was changed after conflict: %v", err)
	}
}

func TestMigrateSessionJSONLPreservesHistory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	agentDir := filepath.Join(home, ".ai", "just", "pi")
	oldID := "profile__folder.old-chat"
	newID := "profile__folder.new-chat"
	cwd := "/work"
	dir := filepath.Join(agentDir, "sessions", EncodeCwdDir(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "session.jsonl")
	rest := "\n{\"type\":\"message\",\"text\":\"keep\"}\n"
	content := "{\"type\":\"session\",\"id\":\"" + oldID + "\",\"custom\":\"keep\"}" + rest
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	gotPath, err := MigrateSessionJSONL(oldID, newID, cwd, agentDir)
	if err != nil {
		t.Fatalf("MigrateSessionJSONL: %v", err)
	}
	if gotPath != path {
		t.Fatalf("path changed: want %q got %q", path, gotPath)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), `"id":"`+newID+`"`) {
		t.Fatalf("new id missing: %s", updated)
	}
	if !strings.HasSuffix(string(updated), rest) {
		t.Fatalf("history changed: want suffix %q got %q", rest, string(updated))
	}
}

func TestMigrateSessionJSONLRejectsTargetHistory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	agentDir := filepath.Join(home, ".ai", "just", "pi")
	cwd := "/work"
	dir := filepath.Join(agentDir, "sessions", EncodeCwdDir(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldID := "profile__old"
	newID := "profile__new"
	for name, id := range map[string]string{"old.jsonl": oldID, "new.jsonl": newID} {
		data := []byte(`{"type":"session","id":"` + id + `"}` + "\n")
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := MigrateSessionJSONL(oldID, newID, cwd, agentDir); err == nil {
		t.Fatal("expected target JSONL conflict")
	}
}

func TestReadFamiliarsRejectsUnownedOrUnsafeSessionIDs(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{name: "foreign owner", data: `[{"id":"expert","sessionId":"other__expert"}]`},
		{name: "path-like suffix", data: `[{"id":"expert","sessionId":"profile__chat__../outside"}]`},
		{name: "owner as child", data: `[{"id":"expert","sessionId":"profile__chat"}]`},
		{name: "duplicate session", data: `[{"id":"expert","sessionId":"profile__chat__one"},{"id":"helper","sessionId":"profile__chat__one"}]`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("AI_DATA_HOME", t.TempDir())
			const owner = "profile__chat"
			path := FamiliarsJSONLPath("test", owner)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(test.data), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadFamiliars("test", owner); err == nil {
				t.Fatal("ReadFamiliars accepted invalid ownership metadata")
			}
		})
	}
}

func TestMigrateSessionJSONLRequiresExplicitAgentDir(t *testing.T) {
	if _, err := MigrateSessionJSONL("old", "new", "/work", ""); err == nil {
		t.Fatal("empty agentDir was accepted for destructive session migration")
	}
}

func TestMigrateSessionJSONLRejectsMalformedSourceHeader(t *testing.T) {
	home := t.TempDir()
	agentDir := filepath.Join(home, ".ai", "just", "pi")
	cwd := "/work"
	dir := filepath.Join(agentDir, "sessions", EncodeCwdDir(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "broken.jsonl")
	original := []byte("not-json\nmessage\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateSessionJSONL("old", "new", cwd, agentDir); err == nil {
		t.Fatal("malformed source JSONL was treated as missing")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("malformed source changed: %q", got)
	}
}

func TestRewriteFamiliarSessionIDsRejectsMalformedChildBeforeWrite(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const (
		profile  = "test"
		oldOwner = "profile__old-chat"
		newOwner = "profile__new-chat"
	)
	path := FamiliarsJSONLPath(profile, newOwner)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte(`[{"id":"expert","sessionId":"profile__old-chat__../outside","extra":true}]`)
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RewriteFamiliarSessionIDs(profile, oldOwner, newOwner); err == nil {
		t.Fatal("RewriteFamiliarSessionIDs accepted path-like familiar ID")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(original) {
		t.Fatalf("malformed familiar registry changed: data=%q err=%v", got, err)
	}
}

func TestRewriteFamiliarSessionIDsPreservesUnknownFields(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	profile := "HumanHorizon"
	oldOwner := "humanhorizon__folder.chat"
	newOwner := "humanhorizon__renamed.chat"
	oldDir := SessionDir(profile, oldOwner)
	newDir := SessionDir(profile, newOwner)
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldFamiliar := oldOwner + "__expert"
	path := filepath.Join(oldDir, "familiars.json")
	content := `[{"id":"expert","sessionId":"` + oldFamiliar + `","created":"now","extra":{"keep":true}}]`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RenameDirectory(oldDir, newDir); err != nil {
		t.Fatal(err)
	}

	mapping, err := RewriteFamiliarSessionIDs(profile, oldOwner, newOwner)
	if err != nil {
		t.Fatalf("RewriteFamiliarSessionIDs: %v", err)
	}
	newFamiliar := newOwner + "__expert"
	if mapping[oldFamiliar] != newFamiliar {
		t.Fatalf("mapping mismatch: %#v", mapping)
	}
	updated, err := os.ReadFile(filepath.Join(newDir, "familiars.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), newFamiliar) || !strings.Contains(string(updated), `"extra"`) {
		t.Fatalf("familiar data was not preserved: %s", updated)
	}
}
