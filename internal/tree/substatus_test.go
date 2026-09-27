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
	solo := tr.AllItems()[0]
	key := tr.SessionKeyOf(solo)
	tr.SetActiveSessionsInMemory(map[string]struct{}{key: {}})
	if badge != "" {
		tr.SetStatusBadges(map[string]string{key: badge})
	}
	return tr.renderItemLine(solo, 120, false, false, branchInfo{}, 0)
}

func TestStatusBadgeWords(t *testing.T) {
	cases := map[string]string{
		"~": "thinking",
		"R": "read",
		"W": "write",
		"w": "wait",
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

func TestUnknownStatusRemainsVisible(t *testing.T) {
	out := renderWithBadge(t, "?")
	if !strings.Contains(out, "● unknown") {
		t.Errorf("unknown status should remain visible, got %q", out)
	}
}

func TestActiveWithoutSubstatusRendersIdle(t *testing.T) {
	out := renderWithBadge(t, "")
	if strings.Contains(out, "active") {
		t.Errorf("rendered line must not contain 'active', got %q", out)
	}
	if !strings.Contains(out, "idle") {
		t.Errorf("active session without substatus should render idle, got %q", out)
	}
}
