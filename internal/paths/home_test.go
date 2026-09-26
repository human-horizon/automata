package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHomeDirUsesEnvironmentHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := HomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != home {
		t.Fatalf("HomeDir() = %q, want %q", got, home)
	}
}

func TestDeleteSessionJSONLIfPresentDistinguishesMissingFromUnreadable(t *testing.T) {
	agentDir := t.TempDir()
	if deleted, err := DeleteSessionJSONLIfPresent("missing-session", "", agentDir); err != nil || deleted != "" {
		t.Fatalf("missing optional JSONL = (%q, %v), want empty path and no error", deleted, err)
	}

	brokenDir := filepath.Join(agentDir, "sessions", "other-cwd")
	if err := os.MkdirAll(brokenDir, 0o755); err != nil {
		t.Fatal(err)
	}
	brokenPath := filepath.Join(brokenDir, "broken.jsonl")
	if err := os.WriteFile(brokenPath, []byte("not-json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := DeleteSessionJSONLIfPresent("target-session", "", agentDir); err == nil {
		t.Fatal("unreadable candidate header was treated as an absent JSONL")
	}
	if _, err := os.Stat(brokenPath); err != nil {
		t.Fatalf("failed preflight removed candidate file: %v", err)
	}
}

func TestBaseDirUsesAIDataHomeWithoutResolvingHome(t *testing.T) {
	dataHome := filepath.Join(t.TempDir(), "automata-data")
	t.Setenv("AI_DATA_HOME", dataHome)
	t.Setenv("HOME", "")

	if got := BaseDir(); got != dataHome {
		t.Fatalf("BaseDir() = %q, want %q", got, dataHome)
	}
}
