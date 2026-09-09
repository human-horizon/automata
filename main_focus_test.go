package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestAppCyclesFocusAcrossPanels(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := newApp("focus-test", "")
	if app.sm.tab.Focus() != app.tree {
		t.Fatal("tree should have initial focus")
	}

	app.Update(tea.KeyMsg{Type: tea.KeyF7})
	if app.sm.tab.Focus() != app.container || app.container.Focused() != app.container.Active() {
		t.Fatal("F7 did not focus the primary panel")
	}

	app.Update(tea.KeyMsg{Type: tea.KeyF7})
	if app.sm.tab.Focus() != app.container || app.container.Focused() != app.container.Knowledge() {
		t.Fatal("F7 did not focus the knowledge panel")
	}

	app.Update(tea.KeyMsg{Type: tea.KeyF7})
	if app.sm.tab.Focus() != app.tree {
		t.Fatal("F7 did not wrap focus to the tree")
	}

	app.Update(tea.KeyMsg{Type: tea.KeyF5})
	if app.sm.tab.Focus() != app.container || app.container.Focused() != app.container.Knowledge() {
		t.Fatal("F5 did not move focus backwards")
	}
}

func TestAppF1TogglesFullScreenHelp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := newApp("help-test", "")
	app.warp.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	app.Update(tea.KeyMsg{Type: tea.KeyF7})
	app.Update(tea.KeyMsg{Type: tea.KeyF1})
	if app.appModal == nil {
		t.Fatal("F1 did not open the root Help overlay")
	}
	app.View()
	if app.appModal.BoxWidth() <= app.tree.TreeWidth() {
		t.Fatalf("Help overlay width = %d, want larger than Tree width %d", app.appModal.BoxWidth(), app.tree.TreeWidth())
	}

	app.Update(tea.KeyMsg{Type: tea.KeyF1})
	if app.appModal != nil {
		t.Fatal("F1 did not close the root Help overlay")
	}
}

func TestAppSettingsUsesRootOverlay(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := newApp("settings-test", "")
	app.warp.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	app.Update(tea.KeyMsg{Type: tea.KeyF7})
	app.openSettingsOverlay()
	if app.appPopover == nil {
		t.Fatal("Settings did not open the root popover")
	}
	if app.appPopover.X+app.appPopover.Width <= app.tree.TreeWidth() {
		t.Fatalf("Settings overlay range = %d..%d, want outside the Tree panel", app.appPopover.X, app.appPopover.X+app.appPopover.Width)
	}

	app.View()
	app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if app.appPopover != nil {
		t.Fatal("selecting Settings item did not close the root popover")
	}
}

func TestAppCtrlCQuitsFromEveryFocusArea(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := newApp("ctrl-c-test", "")

	for _, test := range []struct {
		name  string
		setup func()
	}{
		{name: "tree", setup: func() {}},
		{name: "chat", setup: func() { app.Update(tea.KeyMsg{Type: tea.KeyF7}) }},
		{name: "help overlay", setup: func() { app.openHelpOverlay() }},
		{name: "settings overlay", setup: func() { app.openSettingsOverlay() }},
	} {
		t.Run(test.name, func(t *testing.T) {
			app.closeAppOverlay()
			test.setup()
			_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
			if cmd == nil {
				t.Fatal("Ctrl+C returned no quit command")
			}
			msg := cmd()
			if _, ok := msg.(tea.QuitMsg); !ok {
				t.Fatalf("Ctrl+C command returned %T, want tea.QuitMsg", msg)
			}
		})
	}
}
