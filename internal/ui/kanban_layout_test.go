package ui

import (
	"strings"
	"testing"

	"github.com/HumanHorizon/automata/internal/kanban"
)

// TestKanbanViewTopPadding verifies the View() now reserves one blank line
// above the button and one blank line between the button and the board.
// Layout: y=0 blank, y=1 button, y=2 blank, y=3 column headers start.
func TestKanbanViewTopPadding(t *testing.T) {
	k := NewKanbanPanel("default")
	k.SetDomain("layout-test")
	k.tasks = []kanban.Task{
		{Title: "Sample", Status: "todo", Path: "/tmp/sample.md"},
	}
	k.distribute()

	view := k.View(80, 20)
	lines := strings.Split(view, "\n")
	if len(lines) < 4 {
		t.Fatalf("expected at least 4 lines, got %d", len(lines))
	}

	// y=0: blank padding above the button.
	if blank := strings.TrimSpace(stripAnsiFn(lines[0])); blank != "" {
		t.Errorf("line 0 should be blank (top padding), got: %q", blank)
	}
	// y=1: button row.
	if !strings.Contains(stripAnsiFn(lines[1]), "+ Новая задача") {
		t.Errorf("line 1 should contain the button label, got: %q", stripAnsiFn(lines[1]))
	}
	// y=2: blank padding above the board.
	if blank := strings.TrimSpace(stripAnsiFn(lines[2])); blank != "" {
		t.Errorf("line 2 should be blank (board top padding), got: %q", blank)
	}
	// y=3: column header row.
	if !strings.Contains(stripAnsiFn(lines[3]), "Todo") {
		t.Errorf("line 3 should contain a column header, got: %q", stripAnsiFn(lines[3]))
	}
	// y=3 must not be a full-width horizontal border.
	stripped := stripAnsiFn(lines[3])
	if strings.Trim(stripped, "─") == "" && len(stripped) > 10 {
		t.Errorf("line 3 should not be a horizontal border, got: %q", stripped)
	}
}

// TestKanbanViewButtonRowIsFirst verifies the button row is rendered on y=1
// (y=0 is now the top blank padding line).
func TestKanbanViewButtonRowIsFirst(t *testing.T) {
	k := NewKanbanPanel("default")
	k.SetDomain("btn-test")
	view := k.View(80, 20)
	lines := strings.Split(view, "\n")
	if len(lines) < 4 {
		t.Fatalf("expected at least 4 lines, got %d", len(lines))
	}
	if first := strings.TrimSpace(stripAnsiFn(lines[0])); first != "" {
		t.Errorf("line 0 should be blank, got: %q", first)
	}
	if !strings.Contains(stripAnsiFn(lines[1]), "+ Новая задача") {
		t.Errorf("line 1 should contain the button label, got: %q", stripAnsiFn(lines[1]))
	}
}

// TestKanbanViewNoInterColumnBorders verifies that warp-style │ borders
// no longer appear as a dedicated column between kanban columns. (The
// │ characters that are part of a card box are expected; what we
// explicitly check is that the *first* cell of a column is the column's
// own header text, not a stray │ from a wrap.)
func TestKanbanViewNoInterColumnBorders(t *testing.T) {
	k := NewKanbanPanel("default")
	k.SetDomain("noborder-test")
	k.tasks = []kanban.Task{
		{Title: "T1", Status: "todo", Path: "/tmp/t1.md"},
		{Title: "T2", Status: "pending", Path: "/tmp/t2.md"},
		{Title: "T3", Status: "progress", Path: "/tmp/t3.md"},
		{Title: "T4", Status: "done", Path: "/tmp/t4.md"},
	}
	k.distribute()

	view := k.View(80, 20)
	colW := 80 / len(kanbanColumns)
	lines := strings.Split(view, "\n")
	// Row 3 is the column header row. Each column's first cell should
	// be a space (from " Label (n) "), not a warp border │.
	if len(lines) < 4 {
		t.Fatalf("expected at least 4 lines, got %d", len(lines))
	}
	headerLine := stripAnsiFn(lines[3])
	runes := []rune(headerLine)
	for i := 0; i < len(kanbanColumns)-1; i++ {
		x := (i + 1) * colW
		if x >= len(runes) {
			continue
		}
		if runes[x] == '│' {
			t.Errorf("row 1, between column %d and %d: found │ at x=%d (warp border not removed)", i, i+1, x)
		}
	}
}

// TestKanbanViewButtonHasLeftPadding verifies the button has horizontal
// padding so it doesn't sit flush against the left edge.
func TestKanbanViewButtonHasLeftPadding(t *testing.T) {
	k := NewKanbanPanel("default")
	k.SetDomain("padding-test")
	view := k.View(80, 20)
	first := stripAnsiFn(strings.Split(view, "\n")[1])
	// The button label should not start at column 0.
	idx := strings.Index(first, "+ Новая задача")
	if idx <= 0 {
		t.Errorf("button should have left padding, got idx=%d in %q", idx, first)
	}
	if idx > 5 {
		t.Errorf("button left padding too large: idx=%d", idx)
	}
}

// TestKanbanViewColumnFillsHeight verifies columns span the full remaining
// height after the button row.
func TestKanbanViewColumnFillsHeight(t *testing.T) {
	k := NewKanbanPanel("default")
	k.SetDomain("height-test")
	k.tasks = []kanban.Task{
		{Title: "X", Status: "todo", Path: "/tmp/x.md"},
	}
	k.distribute()

	height := 20
	view := k.View(80, height)
	lines := strings.Split(view, "\n")
	if len(lines) != height {
		t.Errorf("expected %d lines, got %d", height, len(lines))
	}
	// The button row must still exist somewhere (y=1) inside the
	// truncated render.
	foundBtn := false
	for i, ln := range lines {
		if strings.Contains(stripAnsiFn(ln), "+ Новая задача") {
			foundBtn = true
			if i != 1 {
				t.Errorf("button should be on y=1, got y=%d", i)
			}
			break
		}
	}
	if !foundBtn {
		t.Errorf("button row missing from view: %q", view)
	}
}

// stripAnsiFn is a small helper kept local to this test file to avoid
// pulling the kanban_card_test helper into the public surface.
func stripAnsiFn(s string) string {
	var b strings.Builder
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r != 0x1b {
			b.WriteRune(r)
			continue
		}
		if i+1 < len(runes) && runes[i+1] == '[' {
			i += 2
			for i < len(runes) {
				c := runes[i]
				i++
				if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
					break
				}
			}
			i--
			continue
		}
		if i+1 < len(runes) && runes[i+1] == ']' {
			i += 2
			for i < len(runes) {
				c := runes[i]
				i++
				if c == 0x07 {
					break
				}
				if c == 0x1b && i < len(runes) && runes[i] == '\\' {
					i++
					break
				}
			}
			i--
			continue
		}
	}
	return b.String()
}

// silence unused import if lipgloss is no longer referenced.
