package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/HumanHorizon/automata/internal/ai-knowledge/memory"
	"github.com/HumanHorizon/automata/internal/ai-knowledge/notesactualize"
	"github.com/HumanHorizon/automata/internal/paths"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

type testNotesActualizer struct {
	profile        string
	domain         string
	options        notesactualize.RunOptions
	actualizeCalls int
	undoCalls      int
	result         notesactualize.Result
	actualizeErr   error
	undoErr        error
	settingsErr    error
}

func (a *testNotesActualizer) LoadModelSettings() (notesactualize.Settings, error) {
	if a.settingsErr != nil {
		return notesactualize.Settings{}, a.settingsErr
	}
	return notesactualize.Settings{
		Models: []string{"provider/default", "provider/alternative"}, DefaultModel: "provider/default",
		ThinkingLevels: []string{"off", "low", "high"}, DefaultThinkingLevel: "low",
	}, nil
}

func (a *testNotesActualizer) ActualizeWithOptions(_ context.Context, profile, domain string, options notesactualize.RunOptions) (notesactualize.Result, error) {
	a.profile, a.domain = profile, domain
	a.options = options
	a.actualizeCalls++
	return a.result, a.actualizeErr
}

func startConfiguredActualize(panel *ContextPanel) tea.Cmd {
	if panel.notesActualizeConfig == nil {
		panel.openNotesActualizeConfig()
	}
	panel.notesActualizeConfig.focused = actualizeFocusRun
	return panel.handleNotesActualizeConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
}

func (a *testNotesActualizer) UndoLast(profile, domain string) (string, error) {
	a.profile, a.domain = profile, domain
	a.undoCalls++
	if a.undoErr != nil {
		return "", a.undoErr
	}
	return "backup-id", nil
}

func TestContextPanelActualizeToolbarRunsProjectScopedAsyncCommand(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const profile, domain = "actualize-ui-profile", "actualize-ui-profile__repo"
	panel := NewContextPanel(profile)
	defer panel.Close()
	panel.SetDomain(domain)
	actualizer := &testNotesActualizer{result: notesactualize.Result{Committed: true, BeforeCount: 4, AfterCount: 2, UndoID: "undo-id"}}
	panel.SetNotesActualizer(actualizer)

	view := panel.View(15, 8)
	if action := panel.notesToolbarActionAt(6); action != notesClipboardActualize {
		t.Fatalf("compact Actualize hit target = %q, view:\n%s", action, view)
	}
	key := notesProjectKey(profile, domain)
	if cmd := panel.beginNotesClipboard(notesClipboardActualize); cmd != nil || panel.notesActualizeConfig == nil {
		t.Fatal("Actualize click did not open model settings")
	}
	cmd := startConfiguredActualize(panel)
	if cmd == nil || !panel.notesActualizeRunning[key] {
		t.Fatal("confirmed Actualize did not schedule a command and mark the project running")
	}
	if actualizer.actualizeCalls != 0 {
		t.Fatal("AI service ran synchronously before Bubble Tea executed the command")
	}
	panel.Update(cmd())
	if actualizer.actualizeCalls != 1 || actualizer.profile != profile || actualizer.domain != domain {
		t.Fatalf("actualizer scope/calls = %q/%q/%d", actualizer.profile, actualizer.domain, actualizer.actualizeCalls)
	}
	if actualizer.options != (notesactualize.RunOptions{Model: "provider/default", ThinkingLevel: "low"}) {
		t.Fatalf("actualizer options = %+v", actualizer.options)
	}
	if panel.notesStatus != "✓ Actualized 4 → 2" || panel.notesUndoIDs[key] != "undo-id" || panel.notesActualizeRunning[key] {
		t.Fatalf("completion state: status=%q undo=%q running=%v", panel.notesStatus, panel.notesUndoIDs[key], panel.notesActualizeRunning[key])
	}
}

func TestContextPanelActualizeSettingsDropdownUsesEnabledItemsAndCancelIsFree(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	panel := NewContextPanel("dropdown-profile")
	defer panel.Close()
	panel.SetDomain("dropdown-profile__repo")
	actualizer := &testNotesActualizer{}
	panel.SetNotesActualizer(actualizer)
	panel.View(70, 24)
	panel.openNotesActualizeConfig()
	if panel.notesActualizeConfig == nil {
		t.Fatal("missing model settings dropdown")
	}
	config := panel.notesActualizeConfig
	if config.options.Model != "provider/default" || config.options.ThinkingLevel != "low" {
		t.Fatalf("initial selection = %+v", config.options)
	}
	config.modelMenu.Open = true
	config.modelMenu.Hovered = 0
	panel.View(70, 24)
	menuRow := config.modelRow + 2
	panel.Update(tea.MouseMsg{X: 1, Y: menuRow, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if config.options.Model != "provider/alternative" {
		t.Fatalf("model dropdown selected %q", config.options.Model)
	}
	panel.View(70, 24)
	panel.Update(tea.MouseMsg{X: 22, Y: config.runRow, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if panel.notesActualizeConfig != nil || actualizer.actualizeCalls != 0 {
		t.Fatal("Cancel started Actualize or left the selector open")
	}
}

func TestContextPanelActualizeSettingsFailureDoesNotStartRunner(t *testing.T) {
	panel := NewContextPanel("settings-error-profile")
	defer panel.Close()
	panel.SetDomain("settings-error-profile__repo")
	actualizer := &testNotesActualizer{settingsErr: errors.New("invalid model settings")}
	panel.SetNotesActualizer(actualizer)
	panel.openNotesActualizeConfig()
	if panel.notesActualizeConfig != nil || actualizer.actualizeCalls != 0 {
		t.Fatal("invalid settings allowed a model run")
	}
	if panel.notesStatus != "✗ Model settings: invalid model settings" {
		t.Fatalf("settings failure status = %q", panel.notesStatus)
	}
}

func TestContextPanelActualizeDropdownPassesChosenModelAndThinking(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	panel := NewContextPanel("selector-profile")
	defer panel.Close()
	panel.SetDomain("selector-profile__repo")
	actualizer := &testNotesActualizer{}
	panel.SetNotesActualizer(actualizer)
	panel.openNotesActualizeConfig()
	config := panel.notesActualizeConfig
	if config == nil {
		t.Fatal("model settings did not open")
	}
	config.modelMenu.Open = true
	config.modelMenu.Hovered = 0
	config.modelMenu.Update(tea.KeyMsg{Type: tea.KeyDown})
	config.modelMenu.Update(tea.KeyMsg{Type: tea.KeyEnter})
	config.focused = actualizeFocusThinking
	config.thinkingMenu.Open = true
	config.thinkingMenu.Hovered = 1
	config.thinkingMenu.Update(tea.KeyMsg{Type: tea.KeyDown})
	config.thinkingMenu.Update(tea.KeyMsg{Type: tea.KeyEnter})
	config.focused = actualizeFocusRun
	cmd := panel.handleNotesActualizeConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Run did not schedule the configured Actualize")
	}
	panel.Update(cmd())
	if actualizer.options != (notesactualize.RunOptions{Model: "provider/alternative", ThinkingLevel: "high"}) {
		t.Fatalf("selected options = %+v", actualizer.options)
	}
}

func TestContextPanelActualizePreventsDuplicateAndStaleProjectUpdates(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const profile, firstDomain, secondDomain = "stale-ui-profile", "stale-ui-profile__first", "stale-ui-profile__second"
	panel := NewContextPanel(profile)
	defer panel.Close()
	panel.SetDomain(firstDomain)
	actualizer := &testNotesActualizer{result: notesactualize.Result{Committed: true, BeforeCount: 1, AfterCount: 1, UndoID: "first-undo"}}
	panel.SetNotesActualizer(actualizer)
	firstKey := notesProjectKey(profile, firstDomain)
	panel.openNotesActualizeConfig()
	panel.notesActualizeConfig.focused = actualizeFocusRun
	cmd := panel.handleNotesActualizeConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || panel.beginNotesActualize(false) != nil {
		t.Fatal("duplicate Actualize for one domain was not rejected")
	}
	message := cmd().(notesActualizeMsg)
	panel.SetDomain(secondDomain)
	panel.notesStatus = "new project status"
	panel.Update(message)
	if panel.notesStatus != "new project status" {
		t.Fatalf("stale completion changed active project's status: %q", panel.notesStatus)
	}
	if panel.notesUndoIDs[firstKey] != "first-undo" {
		t.Fatal("stale completion failed to retain the original project's Undo state")
	}
	if actualizer.actualizeCalls != 1 {
		t.Fatalf("duplicate guard allowed %d calls", actualizer.actualizeCalls)
	}
}

func TestContextPanelRefreshesWhenStaleCompletionReturnsToSameProject(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const profile, firstDomain, secondDomain = "return-ui-profile", "return-ui-profile__first", "return-ui-profile__second"
	if err := memory.Write(profile, firstDomain, []memory.NoteSummary{{Title: "Before"}}); err != nil {
		t.Fatal(err)
	}
	panel := NewContextPanel(profile)
	defer panel.Close()
	panel.SetDomain(firstDomain)
	panel.SetNotesActualizer(&testNotesActualizer{result: notesactualize.Result{Committed: true, BeforeCount: 1, AfterCount: 1, UndoID: "undo"}})
	cmd := startConfiguredActualize(panel)
	message := cmd().(notesActualizeMsg)
	if err := memory.Write(profile, firstDomain, []memory.NoteSummary{{Title: "Actualized"}}); err != nil {
		t.Fatal(err)
	}
	panel.SetDomain(secondDomain)
	panel.SetDomain(firstDomain)
	panel.notesStatus = "newer operation status"
	panel.Update(message)
	if panel.data == nil || panel.data.Notes[0].Title != "Actualized" {
		t.Fatalf("stale completion did not refresh the original current project: %+v", panel.data)
	}
	if panel.notesStatus != "newer operation status" {
		t.Fatalf("stale completion overwrote newer status: %q", panel.notesStatus)
	}
}

func TestContextPanelUndoIsExplicitAndFailureKeepsUndoAvailable(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const profile, domain = "undo-ui-profile", "undo-ui-profile__repo"
	panel := NewContextPanel(profile)
	defer panel.Close()
	panel.SetDomain(domain)
	key := notesProjectKey(profile, domain)
	panel.notesUndoIDs[key] = "undo-id"
	actualizer := &testNotesActualizer{undoErr: errors.New("notes changed")}
	panel.SetNotesActualizer(actualizer)

	cmd := panel.beginNotesClipboard(notesClipboardUndo)
	if cmd == nil || panel.notesStatus != "Undoing…" {
		t.Fatal("Undo did not schedule asynchronously")
	}
	panel.Update(cmd())
	if actualizer.undoCalls != 1 || actualizer.profile != profile || actualizer.domain != domain {
		t.Fatalf("Undo scope/calls = %q/%q/%d", actualizer.profile, actualizer.domain, actualizer.undoCalls)
	}
	if panel.notesUndoIDs[key] != "undo-id" || panel.notesStatus != "✗ Undo failed: notes changed" {
		t.Fatalf("failed Undo state: id=%q status=%q", panel.notesUndoIDs[key], panel.notesStatus)
	}
	if action := panel.notesToolbarActionAt(panel.width - 1); action == notesClipboardUndo {
		t.Fatal("Undo hit target unexpectedly moved to last cell")
	}
	actualizer.undoErr = nil
	cmd = panel.beginNotesClipboard(notesClipboardUndo)
	panel.Update(cmd())
	if panel.notesUndoIDs[key] != "" || panel.notesStatus != "✓ Undo complete" {
		t.Fatalf("successful Undo state: id=%q status=%q", panel.notesUndoIDs[key], panel.notesStatus)
	}
}

func TestContextPanelActualizeErrorDoesNotTouchProjectNotes(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const profile, domain = "actualize-error-profile", "actualize-error-profile__repo"
	panel := NewContextPanel(profile)
	defer panel.Close()
	panel.SetDomain(domain)
	panel.SetNotesActualizer(&testNotesActualizer{actualizeErr: errors.New("AI audit rejected")})
	cmd := startConfiguredActualize(panel)
	panel.Update(cmd())
	if panel.notesStatus != "✗ Actualize failed: AI audit rejected" {
		t.Fatalf("failure status = %q", panel.notesStatus)
	}
	if _, err := os.Stat(filepath.Join(paths.DomainDir(profile, domain), "notes.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed actualization wrote Notes: %v", err)
	}
}

func TestContextPanelCloseCancelsRunningActualize(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const profile, domain = "cancel-ui-profile", "cancel-ui-profile__repo"
	panel := NewContextPanel(profile)
	panel.SetDomain(domain)
	panel.SetNotesActualizer(&testNotesActualizer{})
	key := notesProjectKey(profile, domain)
	cmd := startConfiguredActualize(panel)
	if cmd == nil || !panel.notesActualizeRunning[key] {
		t.Fatal("setup did not start actualization")
	}
	panel.Close()
	if panel.notesActualizeRunning[key] || len(panel.notesActualizeCancel) != 0 {
		t.Fatal("Close retained actualization state")
	}
}

func TestNotesToolbarActionsFitAvailableWidth(t *testing.T) {
	for _, width := range []int{15, 22, 23, 31, 32, 34, 35, 36, 43, 44, 50, 51, 80} {
		for _, canUndo := range []bool{false, true} {
			panel := NewContextPanel("profile")
			panel.width = width
			if canUndo {
				panel.notesUndoIDs[notesProjectKey(panel.profile, panel.domain)] = "undo"
			}
			line := panel.renderNotesToolbar(width)
			if got := ansi.StringWidth(ansi.Strip(line)); got > width {
				t.Errorf("toolbar width %d (Undo=%v) uses %d cells: %q", width, canUndo, got, line)
			}
			actionFound := false
			for x := 0; x < width; x++ {
				if panel.notesToolbarActionAt(x) == notesClipboardActualize {
					actionFound = true
					break
				}
			}
			if !actionFound {
				t.Errorf("Actualize is not hit-testable at width %d", width)
			}
			panel.Close()
		}
	}
}
