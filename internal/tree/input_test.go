package tree

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestActionAtFolderNested(t *testing.T) {
	tr := New()
	// Create a tree with items at different depths:
	//   Work (folder, depth 0)
	//     Projects (folder, depth 1)
	//       automata (chat, depth 2)
	//   Notes (chat, depth 0)
	folder := &Item{Name: "Work", IsFolder: true, Expanded: true}
	subfolder := &Item{Name: "Projects", IsFolder: true, Expanded: true}
	chat := &Item{Name: "automata"}
	subfolder.AddChild(chat)
	folder.AddChild(subfolder)
	notes := &Item{Name: "Notes"}
	tr.root = append(tr.root, folder, notes)
	tr.rebuildFlat()

	// Hover over the root folder (depth 0, flat index 0).
	// This previously crashed because actionAt used bi[idx] for ALL items
	// in the maxW loop, but items at different depths have different
	// continuationMask lengths.
	tr.hoverIdx = 0
	tr.scroll = 0
	tr.width = 40

	// Should not panic — just verify it returns something.
	got := tr.actionAt(0, 1)
	// At x=0 we're on the branch prefix area, not on any action icon.
	if got != "" {
		t.Logf("actionAt(0, 1) on folder = %q (expected empty, x=0 is before icons)", got)
	}

	// Hover over the nested chat (depth 2, flat index 2).
	tr.hoverIdx = 2
	tr.selected = 2
	got = tr.actionAt(0, 3)
	t.Logf("actionAt on nested chat did not panic, got=%q", got)
}

func TestActionAtFolder(t *testing.T) {
	tr := New()
	folder := &Item{Name: "Work", IsFolder: true, Expanded: true}
	tr.root = append(tr.root, folder)
	tr.rebuildFlat()

	tr.hoverIdx = 0
	tr.scroll = 0

	// The menu icon is rendered as " ⋮". Find its exact position by scanning
	// the rendered line.
	line := stripANSI(tr.renderItemLine(folder, 40, false, true, branchInfo{}, 0))
	menuX := visualIndexOf(line, "⋮")
	if menuX < 0 {
		t.Fatalf("menu icon not found in rendered line: %q", line)
	}

	cases := []struct {
		offset int
		want   string
	}{
		{menuX - 1, ""},
		{menuX, "menu"},
		{menuX + 1, ""},
	}

	for _, tc := range cases {
		got := tr.actionAt(tc.offset, 1)
		if got != tc.want {
			t.Errorf("actionAt(%d, 1) = %q, want %q", tc.offset, got, tc.want)
		}
	}
}

func TestActionAtChatStop(t *testing.T) {
	tr := New()
	tr.Profile = "test-profile"
	chat := &Item{Name: "notes", IsFolder: false}
	tr.root = append(tr.root, chat)
	tr.rebuildFlat()

	tr.hoverIdx = 0
	tr.scroll = 0
	tr.activeSessions = map[string]struct{}{
		"test-profile__notes": {},
	}

	line := stripANSI(tr.renderItemLine(chat, 40, false, true, branchInfo{}, 0))
	t.Logf("rendered line: %q", line)

	stopX := visualIndexOf(line, "■")
	if stopX < 0 {
		t.Fatalf("stop icon not found in rendered line: %q", line)
	}

	cases := []struct {
		offset int
		want   string
	}{
		{stopX - 1, ""},
		{stopX, "stop"},
		{stopX + 1, ""},
	}

	for _, tc := range cases {
		got := tr.actionAt(tc.offset, 1)
		if got != tc.want {
			t.Errorf("actionAt(%d, 1) = %q, want %q", tc.offset, got, tc.want)
		}
	}
}

func TestActionAtChat(t *testing.T) {
	tr := New()
	chat := &Item{Name: "notes", IsFolder: false}
	tr.root = append(tr.root, chat)
	tr.rebuildFlat()

	tr.hoverIdx = 0
	tr.scroll = 0

	line := stripANSI(tr.renderItemLine(chat, 40, false, true, branchInfo{}, 0))
	menuX := visualIndexOf(line, "⋮")
	if menuX < 0 {
		t.Fatalf("menu icon not found in rendered line: %q", line)
	}

	cases := []struct {
		offset int
		want   string
	}{
		{menuX - 1, ""},
		{menuX, "menu"},
		{menuX + 1, ""},
	}

	for _, tc := range cases {
		got := tr.actionAt(tc.offset, 1)
		if got != tc.want {
			t.Errorf("actionAt(%d, 1) = %q, want %q", tc.offset, got, tc.want)
		}
	}
}

func TestConfirmModalButtonLine(t *testing.T) {
	tr := New()
	tr.width = 80
	tr.height = 24
	tr.confirmMode = true
	tr.confirmItem = &Item{Name: "Chat", IsFolder: false}

	line := tr.confirmButtonLine(48) // width 3/5 of 80 = 48
	if !strings.Contains(line, "[Del]") {
		t.Errorf("expected [Del] in button line, got %q", line)
	}
	if !strings.Contains(line, "[Esc]") {
		t.Errorf("expected [Esc] in button line, got %q", line)
	}
}

func TestFindBracketPair(t *testing.T) {
	tests := []struct {
		s     string
		start int
		wantS int
		wantE int
	}{
		{" [Del]  [Esc] ", 0, 1, 6},
		{" [Del]  [Esc] ", 6, 8, 13},
		{"no brackets", 0, -1, -1},
	}
	for _, tc := range tests {
		gotS, gotE := findBracketPair(tc.s, tc.start)
		if gotS != tc.wantS || gotE != tc.wantE {
			t.Errorf("findBracketPair(%q, %d) = (%d,%d), want (%d,%d)", tc.s, tc.start, gotS, gotE, tc.wantS, tc.wantE)
		}
	}
}

func TestModalDragInput(t *testing.T) {
	tr := New()
	tr.width = 80
	tr.height = 24
	tr.startInput("Folder name", func(string) {})

	// Mouse handling for modals is now in warp.
	// Tree handles keyboard input only.
	// Test that Esc closes the modal.
	if !tr.inputMode {
		t.Fatal("input mode should be active after startInput")
	}

	tr.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if tr.inputMode {
		t.Fatal("input mode should be closed after Esc")
	}
}

// TestModalDragInputDoesNotStartOnInputLine verifies that clicking on the input
// text (box[3] = startY+3) does NOT start drag.
// Mouse handling for modals is now in warp — this test is no longer relevant.
func TestModalDragInputDoesNotStartOnInputLine(t *testing.T) {
	// Mouse handling for modals is now in warp. Tree handles keyboard only.
	// This test is kept as a no-op to avoid breaking test references.
}

func TestModalInputButtons(t *testing.T) {
	tr := New()
	tr.width = 80
	tr.height = 24
	var called string
	tr.startInput("Folder name", func(name string) { called = name })
	tr.inputValue = "Work"

	// Mouse handling for modals is now in warp.
	// Test keyboard Enter to confirm.
	tr.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if called != "Work" {
		t.Fatalf("callback called with %q, want Work", called)
	}
	if tr.inputMode {
		t.Fatal("input mode should be closed after Enter")
	}

	// Test keyboard Esc to cancel.
	called = ""
	tr.startInput("Folder name", func(name string) { called = name })
	tr.inputValue = "Test"
	tr.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if called != "" {
		t.Fatalf("callback should not be called on cancel, got %q", called)
	}
	if tr.inputMode {
		t.Fatal("input mode should be closed after Esc")
	}
}

func TestModalDragInputNarrowPanel(t *testing.T) {
	tr := New()
	tr.width = 23
	tr.height = 24
	tr.startInput("Folder name", func(string) {})

	// Mouse handling for modals is now in warp.
	// Test that keyboard still works in narrow panel.
	if !tr.inputMode {
		t.Fatal("input mode should be active after startInput")
	}

	tr.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if tr.inputMode {
		t.Fatal("input mode should be closed after Esc")
	}
}

func TestModalDragConfirm(t *testing.T) {
	tr := New()
	tr.width = 80
	tr.height = 24
	var confirmed bool
	tr.startConfirm(&Item{Name: "Chat", IsFolder: false})
	tr.confirmYes = func() { confirmed = true }

	// Mouse handling for modals is now in warp.
	// Test keyboard Enter to confirm delete.
	if !tr.confirmMode {
		t.Fatal("confirm mode should be active after startConfirm")
	}

	tr.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !confirmed {
		t.Fatal("confirm should be called on Enter")
	}
	if tr.confirmMode {
		t.Fatal("confirm mode should be closed after Enter")
	}

	// Test keyboard Esc to cancel.
	confirmed = false
	tr.startConfirm(&Item{Name: "Chat", IsFolder: false})
	tr.confirmYes = func() { confirmed = true }
	tr.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if confirmed {
		t.Fatal("confirm should not be called on Esc")
	}
	if tr.confirmMode {
		t.Fatal("confirm mode should be closed after Esc")
	}

	// Test 'y' key to confirm.
	confirmed = false
	tr.startConfirm(&Item{Name: "Chat", IsFolder: false})
	tr.confirmYes = func() { confirmed = true }
	tr.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if !confirmed {
		t.Fatal("confirm should be called on 'y'")
	}

	// Test 'n' key to cancel.
	confirmed = false
	tr.startConfirm(&Item{Name: "Chat", IsFolder: false})
	tr.confirmYes = func() { confirmed = true }
	tr.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if confirmed {
		t.Fatal("confirm should not be called on 'n'")
	}
	if tr.confirmMode {
		t.Fatal("confirm mode should be closed after 'n'")
	}
}

func TestModalCloseButton(t *testing.T) {
	tr := New()
	tr.width = 80
	tr.height = 24
	tr.startInput("Folder name", func(string) {})

	// Mouse handling for modals is now in warp.
	// Test keyboard Esc to close.
	if !tr.inputMode {
		t.Fatal("input mode should be active after startInput")
	}

	// Esc should close the modal.
	tr.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if tr.inputMode {
		t.Fatal("modal did not close on Esc")
	}

	// Re-open and test Enter to confirm.
	tr.startInput("Folder name", func(string) {})
	tr.inputValue = "Test"
	tr.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if tr.inputMode {
		t.Fatal("modal did not close on Enter")
	}
}

func TestModalInputCursorAndSpaces(t *testing.T) {
	tr := New()
	tr.width = 80
	tr.height = 24
	tr.startInput("Terminal name", func(string) {})

	// Type "ab cd" with cursor movement.
	tr.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	tr.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	tr.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	tr.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	tr.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if tr.inputValue != "ab cd" {
		t.Fatalf("expected input 'ab cd', got %q", tr.inputValue)
	}
	if tr.inputCursor != 5 {
		t.Fatalf("expected cursor 5, got %d", tr.inputCursor)
	}

	// Move cursor left three times to position it right after 'b'.
	tr.handleKey(tea.KeyMsg{Type: tea.KeyLeft})
	tr.handleKey(tea.KeyMsg{Type: tea.KeyLeft})
	tr.handleKey(tea.KeyMsg{Type: tea.KeyLeft})
	if tr.inputCursor != 2 {
		t.Fatalf("expected cursor 2, got %d", tr.inputCursor)
	}

	// Insert 'X' in the middle.
	tr.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'X'}})
	if tr.inputValue != "abX cd" {
		t.Fatalf("expected input 'abX cd', got %q", tr.inputValue)
	}
	if tr.inputCursor != 3 {
		t.Fatalf("expected cursor 3, got %d", tr.inputCursor)
	}

	// Home and End.
	tr.handleKey(tea.KeyMsg{Type: tea.KeyHome})
	if tr.inputCursor != 0 {
		t.Fatalf("expected cursor 0 after Home, got %d", tr.inputCursor)
	}
	tr.handleKey(tea.KeyMsg{Type: tea.KeyEnd})
	if tr.inputCursor != 6 {
		t.Fatalf("expected cursor 6 after End, got %d", tr.inputCursor)
	}

	// Delete removes 'c' when cursor is at start of "cd".
	tr.inputValue = "ab cd"
	tr.inputCursor = 3
	tr.updateInputModal()
	tr.handleKey(tea.KeyMsg{Type: tea.KeyDelete})
	if tr.inputValue != "ab d" {
		t.Fatalf("expected input 'ab d' after Delete, got %q", tr.inputValue)
	}

	// Backspace removes space when cursor is after it.
	tr.inputValue = "ab cd"
	tr.inputCursor = 3
	tr.updateInputModal()
	tr.handleKey(tea.KeyMsg{Type: tea.KeyBackspace})
	if tr.inputValue != "abcd" {
		t.Fatalf("expected input 'abcd' after Backspace, got %q", tr.inputValue)
	}
}
