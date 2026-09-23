package status

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HumanHorizon/automata/internal/paths"
)

func TestEmoji(t *testing.T) {
	cases := map[string]string{
		"thinking": "~",
		"read":     "R",
		"write":    "W",
		"grep":     "G",
		"find":     "F",
		"analyze":  "A",
		"wait":     "W",
		"job":      "J",
		"run":      ">",
		"idle":     "",
		"stop":     "X",
		// "active" and "status" are known actions without a dedicated
		// glyph — they must produce "" so the Tree falls back to "○ idle"
		// and the right panel can still render the canonical word through
		// actionIcon.
		"active": "",
		"status": "",
		"":       "",
		// Anything we don't recognise still surfaces as a generic dot so
		// a typo in status.json doesn't render as idle.
		"weird": "•",
	}
	for action, want := range cases {
		if got := Emoji(action); got != want {
			t.Errorf("Emoji(%q) = %q, want %q", action, got, want)
		}
	}
}

func TestWord(t *testing.T) {
	cases := map[string]string{
		"thinking": "thinking",
		"read":     "read",
		"write":    "write",
		"find":     "find",
		"grep":     "grep",
		"analyze":  "analyze",
		"wait":     "wait",
		"job":      "job",
		"run":      "run",
		"idle":     "",
		"stop":     "",
		"active":   "",
		"":         "",
		"weird":    "",
	}
	for action, want := range cases {
		if got := Word(action); got != want {
			t.Errorf("Word(%q) = %q, want %q", action, got, want)
		}
	}
}

func TestReadUsesCanonicalSessionPaths(t *testing.T) {
	dataHome := t.TempDir()
	home := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	t.Setenv("HOME", home)

	tests := []struct {
		name    string
		profile string
		session string
		action  string
	}{
		{
			name:    "default profile",
			profile: "",
			session: "default-session",
			action:  "read",
		},
		{
			name:    "unicode profile slug",
			profile: "Проект Ω",
			session: "unicode-session",
			action:  "write",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := paths.SessionDir(test.profile, test.session)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("MkdirAll canonical session dir: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, "status.json"),
				[]byte(`{"action":"`+test.action+`"}`), 0o644); err != nil {
				t.Fatalf("WriteFile canonical status: %v", err)
			}

			if test.profile == "" {
				legacyDir := filepath.Join(dataHome, "sessions", test.session)
				if err := os.MkdirAll(legacyDir, 0o755); err != nil {
					t.Fatalf("MkdirAll legacy session dir: %v", err)
				}
				if err := os.WriteFile(filepath.Join(legacyDir, "status.json"),
					[]byte(`{"action":"legacy"}`), 0o644); err != nil {
					t.Fatalf("WriteFile legacy status: %v", err)
				}
			}

			if got := Read(test.profile, test.session); got != test.action {
				t.Fatalf("Read = %q, want %q", got, test.action)
			}
			reader := NewCachedReader(test.profile)
			if got := reader.Read(test.session); got != test.action {
				t.Fatalf("CachedReader.Read = %q, want %q", got, test.action)
			}
		})
	}

	homeStatus := filepath.Join(home, ".ai", "automata", "profiles", "default", "sessions", "default-session", "status.json")
	if _, err := os.Stat(homeStatus); !os.IsNotExist(err) {
		t.Fatalf("test must not write status under HOME, stat err: %v", err)
	}
}

func TestCachedReaderNoticesSameMtimeReplacement(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const sessionID = "same-mtime-replacement"
	dir := paths.SessionDir("", sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	statusPath := filepath.Join(dir, "status.json")
	temporaryPath := filepath.Join(dir, "replacement.json")
	mtime := time.Unix(1_700_000_000, 0)
	oldContents := []byte(`{"action":"old"}`)
	newContents := []byte(`{"action":"new"}`)
	if len(oldContents) != len(newContents) {
		t.Fatal("replacement fixtures must have equal sizes")
	}
	if err := os.WriteFile(statusPath, oldContents, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(statusPath, mtime, mtime); err != nil {
		t.Fatal(err)
	}

	reader := NewCachedReader("")
	if got := reader.Read(sessionID); got != "old" {
		t.Fatalf("initial status = %q, want old", got)
	}
	oldInfo, err := os.Stat(statusPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(temporaryPath, newContents, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(temporaryPath, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	replacementInfo, err := os.Stat(temporaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if oldInfo.Size() != replacementInfo.Size() || !oldInfo.ModTime().Equal(replacementInfo.ModTime()) {
		t.Fatal("replacement fixture must preserve file size and mtime")
	}
	if os.SameFile(oldInfo, replacementInfo) {
		t.Fatal("replacement fixture unexpectedly refers to the same file")
	}
	if err := os.Rename(temporaryPath, statusPath); err != nil {
		t.Fatal(err)
	}

	if got := reader.Read(sessionID); got != "new" {
		t.Fatalf("status after same-mtime replacement = %q, want new", got)
	}
}

func TestReadMissingFile(t *testing.T) {
	if got := Read("nope", "nosession"); got != "" {
		t.Errorf("expected empty for missing file, got %q", got)
	}
}

func TestReadEmptySessionID(t *testing.T) {
	if got := Read("profile", ""); got != "" {
		t.Errorf("expected empty for empty sessionID, got %q", got)
	}
}

func TestReadActionFromFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AI_DATA_HOME", home)

	profile := "AI Dev"
	sid := "deadbeef"
	dir := filepath.Join(home, "profiles", "ai-dev", "sessions", sid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "status.json"), []byte(`{"action":"read"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Read(profile, sid); got != "read" {
		t.Errorf("Read = %q, want read", got)
	}
}

func TestReadBadJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AI_DATA_HOME", home)

	sid := "broken"
	dir := filepath.Join(home, "profiles", "default", "sessions", sid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "status.json"), []byte(`not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Read("default", sid); got != "" {
		t.Errorf("expected empty for bad json, got %q", got)
	}
}
