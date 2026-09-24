package ui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/HumanHorizon/automata/internal/kanban"
	"github.com/charmbracelet/lipgloss"
)

// stripAnsi removes ANSI escape sequences (CSI + OSC) for easier substring
// assertions in tests. Handles the OSC 8 hyperlink form used for file://
// links: ESC ] 8 ; ; URL ESC \ TEXT ESC ] 8 ; ; ESC \
func stripAnsi(s string) string {
	var b strings.Builder
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r != 0x1b {
			b.WriteRune(r)
			continue
		}
		// CSI: ESC [ ... letter
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
		// OSC: ESC ] ... ESC \  (or BEL terminator)
		if i+1 < len(runes) && runes[i+1] == ']' {
			i += 2
			for i < len(runes) {
				c := runes[i]
				i++
				if c == 0x07 { // BEL
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
		// Standalone escape — drop
	}
	return b.String()
}

// TestRenderCardHasBorders verifies each rendered card starts with ┌ and
// ends with └ so the card visually looks like a boxed element.
func TestRenderCardHasBorders(t *testing.T) {
	c := newKanbanColPanel(0)
	c.width = 24
	c.parent = &KanbanPanel{sessionNames: map[string]string{}}
	task := kanban.Task{Title: "Hello", Status: "todo", Path: "/tmp/x.md"}

	lines := c.renderCard(task, false)
	if len(lines) == 0 {
		t.Fatalf("renderCard returned no lines")
	}
	first := stripAnsi(lines[0])
	last := stripAnsi(lines[len(lines)-1])
	if !strings.HasPrefix(first, "┌") {
		t.Errorf("first line should start with ┌, got %q", first)
	}
	if !strings.HasSuffix(last, "┘") {
		t.Errorf("last line should end with ┘, got %q", last)
	}
	for i, ln := range lines[1 : len(lines)-1] {
		stripped := stripAnsi(ln)
		if !strings.HasPrefix(stripped, "│") {
			t.Errorf("content line %d should start with │, got %q", i+1, stripped)
		}
		if !strings.HasSuffix(stripped, "│") {
			t.Errorf("content line %d should end with │, got %q", i+1, stripped)
		}
	}
}

// TestRenderCardWidthRespected verifies the rendered card fits the requested
// width — borders and content must not overflow the column.
func TestRenderCardWidthRespected(t *testing.T) {
	c := newKanbanColPanel(0)
	c.width = 24
	c.parent = &KanbanPanel{sessionNames: map[string]string{}}
	task := kanban.Task{Title: "Tight column", Status: "todo", AssignedTo: "s1", Path: "/tmp/x.md"}

	lines := c.renderCard(task, false)
	for i, ln := range lines {
		if w := lipgloss.Width(ln); w != 24 {
			t.Errorf("line %d width = %d, want 24 (line=%q)", i, w, stripAnsi(ln))
		}
	}
}

func TestRenderCardUnicodeTruncationPreservesUTF8AndWidth(t *testing.T) {
	c := newKanbanColPanel(0)
	c.width = 12
	c.parent = &KanbanPanel{sessionNames: map[string]string{"owner": "非常に長い名前"}}
	task := kanban.Task{
		Title:      "非常に長いタイトル🙂",
		Status:     "progress",
		AssignedTo: "owner",
		Substatus:  "длинный статус",
		Path:       "/tmp/x.md",
	}
	for i, line := range c.renderCard(task, false) {
		if !utf8.ValidString(line) {
			t.Fatalf("line %d is invalid UTF-8: %q", i, line)
		}
		if width := lipgloss.Width(line); width != c.width {
			t.Fatalf("line %d width = %d, want %d: %q", i, width, c.width, stripAnsi(line))
		}
	}
}

// TestRenderCardIncludesTransitions verifies transition buttons are rendered
// inside the card even without hover.
func TestRenderCardIncludesTransitions(t *testing.T) {
	c := newKanbanColPanel(0)
	c.width = 30
	c.parent = &KanbanPanel{sessionNames: map[string]string{}}
	task := kanban.Task{Title: "Todo task", Status: "todo", Path: "/tmp/x.md"}

	lines := c.renderCard(task, false)
	joined := stripAnsi(strings.Join(lines, "\n"))
	if !strings.Contains(joined, "→ Pending") {
		t.Errorf("expected '→ Pending' button inside the card, got:\n%s", joined)
	}
	if !strings.Contains(joined, "×") {
		t.Errorf("expected delete marker × inside the card, got:\n%s", joined)
	}
}

// TestHitTestCoversCardBorders verifies hitTest returns the right card index
// when the cursor is on a card border. Layout for status=todo (1 transition,
// no assignment):
//
//	y=0: column header (no card)
//	y=1: top border of card 0
//	y=2: title of card 0
//	y=3: transition button
//	y=4: file link
//	y=5: bottom border
//	y=6: gap (1 blank line between cards)
//	y=7: top border of card 1
//	y=8: title
//	y=9: transition
//	y=10: file
//	y=11: bottom
func TestHitTestCoversCardBorders(t *testing.T) {
	c := newKanbanColPanel(0)
	c.width = 24
	c.parent = &KanbanPanel{sessionNames: map[string]string{}}
	c.tasks = []kanban.Task{
		{Title: "Alpha", Status: "todo", Path: "/tmp/a.md"},
		{Title: "Beta", Status: "todo", Path: "/tmp/b.md"},
	}

	for _, y := range []int{1, 2, 3, 4, 5} {
		row, _ := c.hitTest(y, 5)
		if row != 0 {
			t.Errorf("card 0 at line %d: expected row=0, got %d", y, row)
		}
	}
	for _, y := range []int{7, 8, 9, 10, 11} {
		row, _ := c.hitTest(y, 5)
		if row != 1 {
			t.Errorf("card 1 at line %d: expected row=1, got %d", y, row)
		}
	}
}

// TestHitTestDeleteXInTopRight verifies clicking on × (right side of the
// title line) returns the right card and "delete" button. Layout for
// status=todo (1 transition, no assignment):
//
//	y=0: column header
//	y=1: top border
//	y=2: title
//	y=3: transition button
//	y=4: file link
//	y=5: bottom border
func TestHitTestDeleteXInTopRight(t *testing.T) {
	c := newKanbanColPanel(0)
	c.width = 24
	c.parent = &KanbanPanel{sessionNames: map[string]string{}}
	c.tasks = []kanban.Task{
		{Title: "Alpha", Status: "todo", Path: "/tmp/a.md"},
	}

	// Title is at y=2. × is at x = width-2 = 22 (inner-right edge).
	row, btn := c.hitTest(2, c.width-2)
	if row != 0 || btn != "delete" {
		t.Errorf("× click: expected (0,delete), got (%d,%q)", row, btn)
	}

	// Click further left on the title should not trigger delete.
	row, btn = c.hitTest(2, 5)
	if row != 0 || btn == "delete" {
		t.Errorf("left-side title click: expected row=0, btn!=\"delete\", got (%d,%q)", row, btn)
	}
}
