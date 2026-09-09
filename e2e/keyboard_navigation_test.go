package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestKeyboardHelpOpens(t *testing.T) {
	profile := "cue-test-keyboard-help"
	writeSessionFixture(t, profile, nil)
	app, page := launchAutomataForSession(t, profile)
	defer app.Close()

	page.Press("F1")
	page.WaitStable(200 * time.Millisecond)
	text, err := page.Text()
	if err != nil {
		t.Fatalf("read help screen: %v", err)
	}
	if !strings.Contains(text, "Keyboard help") || !strings.Contains(text, "F5 / F7") {
		t.Fatalf("keyboard help is missing expected content:\n%s", text)
	}
	lines, err := page.Lines()
	if err != nil {
		t.Fatalf("read help lines: %v", err)
	}
	helpColumn := -1
	for _, line := range lines {
		if column := strings.Index(line, "Keyboard help"); column >= 0 {
			helpColumn = column
			break
		}
	}
	if helpColumn <= 30 {
		t.Fatalf("Help is still inside the Tree panel: column %d\n%s", helpColumn, text)
	}

	page.Press("Escape")
	page.WaitStable(100 * time.Millisecond)
	text, err = page.Text()
	if err != nil {
		t.Fatalf("read screen after closing help: %v", err)
	}
	if strings.Contains(text, "Keyboard help") {
		t.Fatal("keyboard help remained open after Escape")
	}
}

func TestSettingsMenuOpensAndPersistsTheme(t *testing.T) {
	profile := "cue-test-settings-theme"
	profileDir := writeSessionFixture(t, profile, nil)
	app, page := launchAutomataForSession(t, profile)
	defer app.Close()

	lines, err := page.Lines()
	if err != nil {
		t.Fatalf("read footer: %v", err)
	}
	footerRow := -1
	settingsColumn := -1
	for row, line := range lines {
		if column := strings.Index(line, "Settings"); column >= 0 {
			footerRow = row
			settingsColumn = column
			break
		}
	}
	if footerRow < 0 {
		t.Fatalf("Settings button is missing from footer:\n%s", strings.Join(lines, "\n"))
	}
	page.MouseClick(settingsColumn, footerRow)
	page.WaitStable(150 * time.Millisecond)
	text, err := page.Text()
	if err != nil {
		t.Fatalf("read settings menu: %v", err)
	}
	if !strings.Contains(text, "Dinosaur Earth") {
		t.Fatalf("settings menu is missing the first theme:\n%s", text)
	}
	themeColumn := strings.Index(text, "Dinosaur Earth")
	if themeColumn <= 30 {
		t.Fatalf("Settings is still inside the Tree panel: column %d\n%s", themeColumn, text)
	}

	page.Press("Enter")
	page.WaitStable(150 * time.Millisecond)
	data, err := os.ReadFile(filepath.Join(profileDir, "state.json"))
	if err != nil {
		t.Fatalf("read themed state: %v", err)
	}
	if !strings.Contains(string(data), `"theme": "dinosaur-earth-sunny-grassy"`) {
		t.Fatalf("theme selection was not persisted:\n%s", data)
	}
}

func TestCtrlCExitsFromNonTreeFocus(t *testing.T) {
	profile := "cue-test-ctrl-c"
	writeSessionFixture(t, profile, nil)
	app, page := launchAutomataForSession(t, profile)

	if err := page.Press("F7"); err != nil {
		_ = app.Close()
		t.Fatalf("focus chat panel: %v", err)
	}
	if err := page.Press("Ctrl+C"); err != nil {
		_ = app.Close()
		t.Fatalf("send Ctrl+C: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- app.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Automata exited with error after Ctrl+C: %v", err)
		}
	case <-time.After(2 * time.Second):
		_ = app.Close()
		t.Fatal("Ctrl+C did not close Automata from non-tree focus")
	}
}

func TestRootMenuSortsByName(t *testing.T) {
	profile := "cue-test-root-sort"
	profileDir := writeSessionFixture(t, profile, []sessionStateItem{
		{Name: "zulu"},
		{Name: "alpha", IsFolder: true, Expanded: true},
		{Name: "bravo"},
	})
	app, page := launchAutomataForSession(t, profile)
	defer app.Close()

	page.Press("F10")
	page.WaitStable(100 * time.Millisecond)
	page.Press("Enter")
	page.WaitStable(300 * time.Millisecond)

	data, err := os.ReadFile(filepath.Join(profileDir, "state.json"))
	if err != nil {
		t.Fatalf("read sorted state: %v", err)
	}
	var state sessionState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("decode sorted state: %v", err)
	}
	want := []string{"alpha", "bravo", "zulu"}
	if len(state.Items) != len(want) {
		t.Fatalf("sorted item count = %d, want %d", len(state.Items), len(want))
	}
	for i, name := range want {
		if state.Items[i].Name != name {
			t.Fatalf("state item %d = %q, want %q", i, state.Items[i].Name, name)
		}
	}
}
