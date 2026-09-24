package tree

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestKeyboardNavigationUsesArrowsAndPages(t *testing.T) {
	tr := New()
	folder := &Item{Name: "folder", IsFolder: true, Expanded: true}
	child := &Item{Name: "child"}
	folder.AddChild(child)
	tr.root = []*Item{folder, &Item{Name: "chat"}}
	tr.height = 6
	tr.rebuildFlat()
	send := func(key tea.KeyType) { tr.handleKey(tea.KeyMsg{Type: key}) }

	send(tea.KeyDown)
	if got := tr.SelectedItem(); got != folder {
		t.Fatalf("down selected %v, want folder", got)
	}
	send(tea.KeyRight)
	if got := tr.SelectedItem(); got != child {
		t.Fatalf("right selected %v, want child", got)
	}
	send(tea.KeyLeft)
	if got := tr.SelectedItem(); got != folder {
		t.Fatalf("left selected %v, want folder", got)
	}
	send(tea.KeyLeft)
	if folder.Expanded {
		t.Fatal("left did not collapse the selected folder")
	}
	send(tea.KeyRight)
	if !folder.Expanded {
		t.Fatal("right did not expand the selected folder")
	}
	send(tea.KeyEnd)
	if got := tr.SelectedItem(); got == nil || got.Name != "chat" {
		t.Fatalf("end selected %v, want chat", got)
	}
	send(tea.KeyHome)
	if got := tr.SelectedItem(); got != folder {
		t.Fatalf("home selected %v, want folder", got)
	}
}

func TestKeyboardTabDoesNotMoveTreeSelection(t *testing.T) {
	tr := New()
	tr.root = []*Item{{Name: "one"}, {Name: "two"}}
	tr.rebuildFlat()
	tr.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	before := tr.SelectedItem()

	tr.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	if tr.SelectedItem() != before {
		t.Fatalf("tab changed selection from %v to %v", before, tr.SelectedItem())
	}
}

func TestKeyboardHelpOpensAndCloses(t *testing.T) {
	tr := New()
	tr.OpenHelp()
	if !tr.HelpOpen() || !tr.IsModalOpen() {
		t.Fatal("help did not open as a modal")
	}
	tr.handleKey(tea.KeyMsg{Type: tea.KeyF1})
	if tr.HelpOpen() || tr.IsModalOpen() {
		t.Fatal("F1 did not close help")
	}

	tr.OpenHelp()
	tr.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if tr.HelpOpen() {
		t.Fatal("Enter did not close help")
	}
}

func TestRootToolbarAndFooterAreKeyboardAccessibleByMouse(t *testing.T) {
	tr := New()
	tr.width = 40
	tr.height = 10
	tr.View(tr.width, tr.height)

	tr.handleMouse(tea.MouseMsg{
		X:      tr.width - 5,
		Y:      0,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	if tr.popover == nil {
		t.Fatal("root toolbar did not open a popover")
	}
	if len(tr.popover.Items) < 5 || tr.popover.Items[0].Name != "Sort by name" {
		t.Fatalf("root toolbar opened wrong menu: %+v", tr.popover.Items)
	}
	tr.popover = nil

	tr.handleMouse(tea.MouseMsg{
		X:      1,
		Y:      tr.height - 1,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	if !tr.HelpOpen() {
		t.Fatal("footer Help button did not open help")
	}
}

func TestHeaderKeepsToolbarVisibleWithLongProfile(t *testing.T) {
	tr := New()
	tr.Profile = "cue-test-plan"
	tr.NoColor = true
	const width = 30
	view := tr.View(width, 5)
	header := strings.Split(view, "\n")[0]
	if got := ansi.StringWidth(header); got != width {
		t.Fatalf("header width = %d, want %d: %q", got, width, header)
	}
	if !strings.Contains(header, "⋮") || !strings.Contains(header, "+") {
		t.Fatalf("long profile hid toolbar controls: %q", header)
	}

	tr.handleMouse(tea.MouseMsg{
		X:      width - 2,
		Y:      0,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	if tr.popover == nil || len(tr.popover.Items) < 2 || tr.popover.Items[1].Name != "+ Chat" {
		t.Fatalf("visible + toolbar did not open create menu: %+v", tr.popover)
	}
}

func TestRootMenuSortModes(t *testing.T) {
	tr := New()
	items := tr.rootSortMenuItems()
	if len(items) != 2 {
		t.Fatalf("root menu has %d items, want 2", len(items))
	}
	if items[0].Name != "Sort by name" || items[1].Name != "Sort by type" {
		t.Fatalf("root menu names = %q, %q", items[0].Name, items[1].Name)
	}
}

func TestSortRootByNamePreservesArchivedGroupAndSelection(t *testing.T) {
	tr := New()
	chat := &Item{Name: "zulu"}
	folder := &Item{Name: "alpha", IsFolder: true}
	terminal := &Item{Name: "bravo", IsTerminal: true}
	archived := &Item{Name: "aard", IsFolder: true, Archived: true}
	tr.root = []*Item{chat, archived, terminal, folder}
	tr.rebuildFlat()
	tr.selected = 0

	tr.sortRootByName()
	want := []*Item{folder, terminal, chat, archived}
	for i, item := range want {
		if tr.root[i] != item {
			t.Fatalf("root[%d] = %v, want %v", i, tr.root[i], item)
		}
	}
	if tr.SelectedItem() != chat {
		t.Fatalf("selected item changed to %v, want chat", tr.SelectedItem())
	}
}

func TestSortRootByTypeGroupsKindsAndNames(t *testing.T) {
	tr := New()
	tr.root = []*Item{
		{Name: "z-chat"},
		{Name: "b-terminal", IsTerminal: true},
		{Name: "a-folder", IsFolder: true},
		{Name: "a-chat"},
		{Name: "c-folder", IsFolder: true},
	}
	tr.rebuildFlat()

	tr.sortRootByType()
	want := []string{"a-folder", "c-folder", "a-chat", "z-chat", "b-terminal"}
	for i, name := range want {
		if tr.root[i].Name != name {
			t.Fatalf("root[%d] = %q, want %q", i, tr.root[i].Name, name)
		}
	}
}
