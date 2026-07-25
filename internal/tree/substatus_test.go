package tree

import (
	"strings"
	"testing"
)

// renderWithBadge is a tiny helper that builds a tree with one chat and
// the given status badge glyph, then returns the rendered row text.
func renderWithBadge(t *testing.T, badge string) string {
	t.Helper()
	tr := New()
	tr.AddChat("agent")
	if badge != "" {
		tr.SetStatusBadges(map[string]string{tr.SessionKeyOf(tr.AllItems()[0]): badge})
	}
	solo := tr.AllItems()[0]
	return tr.renderItemLine(solo, 120, false, false, branchInfo{}, 0)
}

func TestStatusBadgeWords(t *testing.T) {
	cases := map[string]string{
		"~": "thinking",
		"R": "read",
		"W": "write",
		"G": "grep",
		"F": "find",
		"A": "analyze",
		"J": "job",
		">": "run",
		"X": "stopped",
		"":  "idle",
	}
	for badge, want := range cases {
		out := renderWithBadge(t, badge)
		// "○ idle" for the empty/idle case, "● <word>" otherwise.
		if badge == "" {
			if !strings.Contains(out, "○ idle") {
				t.Errorf("badge=%q: expected ○ idle, got %q", badge, out)
			}
		} else {
			if !strings.Contains(out, "● "+want) {
				t.Errorf("badge=%q: expected ● %s, got %q", badge, want, out)
			}
		}
	}
}

// TestActiveBadgeNeverShown is the regression test for the spec: a session
// running without a recorded substatus must display as idle, NOT as
// "● active". The emoji map no longer contains "active" so this is enforced
// by the empty-string fallback.
func TestActiveBadgeNeverShown(t *testing.T) {
	out := renderWithBadge(t, "•") // unknown glyph used to map to "active"
	if strings.Contains(out, "active") {
		t.Errorf("rendered line must not contain 'active', got %q", out)
	}
	if !strings.Contains(out, "idle") {
		t.Errorf("unknown badge should fall back to idle, got %q", out)
	}
}
