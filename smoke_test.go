package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/HumanHorizon/automata/internal/slug"
	"github.com/HumanHorizon/automata/internal/status"
	"github.com/HumanHorizon/automata/internal/tree"
)

// TestSmokeStatusBadges is a manual-style smoke that exercises the
// status-reading + badge-mapping path the new fsnotify watcher drives.
// It writes one status.json per known action, prints the resulting
// (action, emoji, word) table, and verifies a few invariants:
//
//   - the cached reader picks up mtime changes (no 5s tick needed);
//   - missing status.json maps to action="", emoji="" (idle fallback);
//   - every action in the canonical set maps to a non-empty glyph that
//     matches what the right panel now renders.
//
// The test never touches a real TTY or a real fsnotify channel — it just
// proves the data path the watcher feeds is correct.
func TestSmokeStatusBadges(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	profile := "smoke"

	tr := tree.New()
	tr.Profile = profile

	cases := []struct {
		chat, action, desc string
	}{
		{"alpha", "thinking", "deep thoughts"},
		{"beta", "read", "/Users/a/beta.go"},
		{"gamma", "write", "/Users/a/gamma.go"},
		{"delta", "run", "go test ./..."},
		{"epsilon", "grep", "TODO"},
		{"zeta", "idle", "/Users/a/stale.go"},
		{"eta", "find", "name=foo"},
		{"theta", "stop", "signal"},
		{"iota", "analyze", "diagram"},
		{"kappa", "job", "build"},
		{"lambda", "active", "main"},
		{"mu", "wait", "deps"},
		{"nu", "status", "diagnostic"},
	}

	sessions := filepath.Join(tmp, ".ai", "automata", "profiles", slug.Slug(profile), "sessions")
	for _, c := range cases {
		tr.AddChat(c.chat)
		key := tr.SessionKeyOf(tr.AllItems()[len(tr.AllItems())-1])
		dir := filepath.Join(sessions, key)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		payload := fmt.Sprintf(`{"action":%q,"description":%q}`, c.action, c.desc)
		if err := os.WriteFile(filepath.Join(dir, "status.json"), []byte(payload), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	reader := status.NewCachedReader(profile)
	t.Logf("=== status.json badge map (right panel & Tree shared) ===")
	keys := make([]string, 0, len(tr.AllItems()))
	for _, it := range tr.AllItems() {
		if it == nil || it.IsFolder || it.IsTerminal {
			continue
		}
		keys = append(keys, tr.SessionKeyOf(it))
	}
	sort.Strings(keys)

	// Build a reverse map (action → key) so we can assert invariants.
	actionOfKey := make(map[string]string, len(keys))
	for i, c := range cases {
		actionOfKey[keys[i]] = c.action
	}

	for _, k := range keys {
		a := reader.Read(k)
		glyph := status.Emoji(a)
		word := status.Word(a)
		t.Logf("  %-40s action=%-9s emoji=%-3q word=%q", k, a, glyph, word)
	}

	// Invariant 1: idle/active/status yield no glyph (Tree falls back to
	// "○ idle" in all three); every other action yields a non-empty glyph.
	wantEmpty := map[string]bool{"idle": true, "active": true, "status": true}
	for _, k := range keys {
		a := actionOfKey[k]
		glyph := status.Emoji(a)
		if wantEmpty[a] {
			if glyph != "" {
				t.Errorf("action=%s should map to empty glyph, got %q", a, glyph)
			}
		} else if glyph == "" {
			t.Errorf("action=%s should map to non-empty glyph, got empty", a)
		}
	}

	// Invariant 2: cached reader picks up mtime changes without restart.
	alphaKey := keys[0]
	alphaPath := filepath.Join(sessions, alphaKey, "status.json")
	before := reader.Read(alphaKey)
	time.Sleep(20 * time.Millisecond)
	if err := os.WriteFile(alphaPath, []byte(`{"action":"run","description":"pnpm build"}`), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	after := reader.Read(alphaKey)
	if before == after {
		t.Errorf("cached reader did not pick up mtime change (before=%q after=%q)", before, after)
	}

	// Invariant 3: missing status.json is treated as idle.
	betaPath := filepath.Join(sessions, keys[1], "status.json")
	if err := os.Remove(betaPath); err != nil {
		t.Fatalf("remove: %v", err)
	}
	got := reader.Read(keys[1])
	if got != "" {
		t.Errorf("missing status.json should be treated as idle (action=\"\"), got %q", got)
	}
	if glyph := status.Emoji(got); glyph != "" {
		t.Errorf("missing status.json should have empty glyph, got %q", glyph)
	}

	t.Logf("=== smoke complete: 13 actions, mtime invalidation, missing-file fallback ===")
}
