package status

import (
	"os"
	"path/filepath"
	"testing"
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
		// "active" is intentionally NOT a recognized substatus — it must
		// not produce a glyph. The session should display as idle.
		"active": "•",
		"":       "",
		"weird":  "•",
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

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"":       "",
		"hello":  "hello",
		"Hello":  "hello",
		"my-pro": "my-pro",
		"AI Dev": "ai-dev",
		"a__b!c": "a-b-c",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
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