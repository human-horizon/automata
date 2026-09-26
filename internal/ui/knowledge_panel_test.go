package ui

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	akcontext "github.com/HumanHorizon/automata/internal/ai-knowledge/context"
	akjobs "github.com/HumanHorizon/automata/internal/ai-knowledge/jobs"
	"github.com/HumanHorizon/automata/internal/kanban"
	"github.com/HumanHorizon/automata/internal/paths"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// stubPanel is a minimal warp.Panel for tests.
type stubPanel struct{}

func (stubPanel) View(width, height int) string { return "" }
func (stubPanel) Update(msg tea.Msg) tea.Cmd    { return nil }

func TestKnowledgePanelLateAttachesSessionAndJobsWatchersWithoutPolling(t *testing.T) {
	profile := "Late Knowledge"
	sessionID := "late-knowledge__chat"
	t.Setenv("AI_DATA_HOME", t.TempDir())
	k := NewKnowledgePanel()
	defer k.Close()
	k.SetProfile(profile)
	k.SetSession(sessionID)
	k.Activate()

	if k.knowledgeWatcher == nil || k.knowledgeWatcherPath != paths.SessionsDir(profile) {
		t.Fatalf("missing-session watcher = %q, want sessions parent %q", k.knowledgeWatcherPath, paths.SessionsDir(profile))
	}
	watchCmd := k.watchKnowledgeCmd()
	if watchCmd == nil {
		t.Fatal("missing-session watch command is nil")
	}
	messages := make(chan tea.Msg, 1)
	go func() { messages <- watchCmd() }()
	if err := os.MkdirAll(filepath.Join(paths.SessionsDir(profile), sessionID), 0o755); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-messages:
		if _, ok := msg.(knowledgeChangedMsg); !ok {
			t.Fatalf("session creation message = %T", msg)
		}
		k.Update(msg)
	case <-time.After(2 * time.Second):
		t.Fatal("session parent watcher did not observe late session creation")
	}
	if k.knowledgeWatcherPath != paths.SessionDir(profile, sessionID) {
		t.Fatalf("session watcher path = %q, want %q", k.knowledgeWatcherPath, paths.SessionDir(profile, sessionID))
	}
	if k.jobsWatcher == nil || k.jobsWatcherPath != paths.SessionDir(profile, sessionID) {
		t.Fatalf("late jobs watcher = %q, want session directory", k.jobsWatcherPath)
	}

	jobsCmd := k.watchJobsCmd()
	if jobsCmd == nil {
		t.Fatal("late jobs watch command is nil")
	}
	go func() { messages <- jobsCmd() }()
	if err := os.MkdirAll(filepath.Join(paths.SessionDir(profile, sessionID), "jobs"), 0o755); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-messages:
		if _, ok := msg.(jobsChangedMsg); !ok {
			t.Fatalf("jobs creation message = %T", msg)
		}
		k.Update(msg)
	case <-time.After(2 * time.Second):
		t.Fatal("session watcher did not observe late jobs creation")
	}
	if k.jobsWatcherPath != filepath.Join(paths.SessionDir(profile, sessionID), "jobs") {
		t.Fatalf("jobs watcher path = %q, want jobs directory", k.jobsWatcherPath)
	}
}

func TestKnowledgePanelRecoversClosedWatchers(t *testing.T) {
	profile := "watcher-recovery"
	sessionID := "watcher-recovery__chat"
	t.Setenv("AI_DATA_HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(paths.SessionDir(profile, sessionID), "jobs"), 0o755); err != nil {
		t.Fatal(err)
	}

	k := NewKnowledgePanel()
	defer k.Close()
	k.SetProfile(profile)
	k.SetSession(sessionID)
	k.Activate()
	oldKnowledge := k.knowledgeWatcher
	oldJobs := k.jobsWatcher
	if oldKnowledge == nil || oldJobs == nil {
		t.Fatal("test setup did not create both watchers")
	}

	k.Update(knowledgeWatcherErrorMsg{generation: k.knowledgeGeneration, watcher: oldKnowledge, err: errors.New("synthetic knowledge watcher error")})
	if k.knowledgeWatcher == nil || k.knowledgeWatcher == oldKnowledge {
		t.Fatal("knowledge watcher was not recreated")
	}

	k.Update(jobsWatcherErrorMsg{generation: k.jobsGeneration, watcher: oldJobs, err: errors.New("synthetic jobs watcher error")})
	if k.jobsWatcher == nil || k.jobsWatcher == oldJobs {
		t.Fatal("jobs watcher was not recreated")
	}
}

func TestKnowledgePanelDeactivateRejectsStaleEventsAndPreservesUIState(t *testing.T) {
	profile := "knowledge-lifecycle"
	sessionID := "knowledge-lifecycle__chat"
	t.Setenv("AI_DATA_HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(paths.SessionDir(profile, sessionID), "jobs"), 0o755); err != nil {
		t.Fatal(err)
	}

	panel := NewKnowledgePanel()
	defer panel.Close()
	panel.SetProfile(profile)
	panel.SetSession(sessionID)
	panel.Activate()
	panel.scrollOffset = 7
	panel.plansCollapsed = true
	panel.jobsCollapsed = true
	panel.currentTask = "kept task"
	oldKnowledge := panel.knowledgeWatcher
	oldJobs := panel.jobsWatcher
	oldKnowledgeGeneration := panel.knowledgeGeneration
	oldJobsGeneration := panel.jobsGeneration
	if oldKnowledge == nil || oldJobs == nil {
		t.Fatal("Activate did not create both Knowledge watchers")
	}

	panel.Deactivate()
	if panel.active || panel.knowledgeWatcher != nil || panel.jobsWatcher != nil {
		t.Fatalf("Deactivate retained resources: active=%v knowledge=%v jobs=%v", panel.active, panel.knowledgeWatcher, panel.jobsWatcher)
	}
	panel.Update(knowledgeChangedMsg{generation: oldKnowledgeGeneration, watcher: oldKnowledge})
	panel.Update(jobsChangedMsg{generation: oldJobsGeneration, watcher: oldJobs})
	if panel.scrollOffset != 7 || !panel.plansCollapsed || !panel.jobsCollapsed || panel.currentTask != "kept task" {
		t.Fatalf("deactivation or stale event discarded UI state: scroll=%d plansCollapsed=%v jobsCollapsed=%v task=%q", panel.scrollOffset, panel.plansCollapsed, panel.jobsCollapsed, panel.currentTask)
	}

	panel.Activate()
	if !panel.active || panel.knowledgeWatcher == nil || panel.jobsWatcher == nil || panel.knowledgeWatcher == oldKnowledge || panel.jobsWatcher == oldJobs {
		t.Fatal("reactivation did not create fresh Knowledge watchers")
	}
	if panel.scrollOffset != 7 || !panel.plansCollapsed || !panel.jobsCollapsed {
		t.Fatal("reactivation discarded Knowledge view state")
	}
}

func TestKnowledgePanelSettingsDoNotLeakBetweenSessions(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	profile := "settings-profile"
	firstSession := "settings-profile__first"
	secondSession := "settings-profile__second"
	firstDir := paths.SessionDir(profile, firstSession)
	secondDir := paths.SessionDir(profile, secondSession)
	if err := os.MkdirAll(firstDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(secondDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(firstDir, "settings.json"), []byte(`{"autoContinue":true,"dual":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secondDir, "settings.json"), []byte(`{"autoContinue":`), 0o644); err != nil {
		t.Fatal(err)
	}

	k := NewKnowledgePanel()
	defer k.Close()
	k.SetProfile(profile)
	k.SetSession(firstSession)
	if !k.autoContinue || !k.dual {
		t.Fatalf("first session settings = auto=%v dual=%v", k.autoContinue, k.dual)
	}
	k.SetSession(secondSession)
	if k.autoContinue || k.dual {
		t.Fatalf("invalid second-session settings leaked first session: auto=%v dual=%v", k.autoContinue, k.dual)
	}
	k.SetSession("")
	if k.autoContinue || k.dual {
		t.Fatalf("empty session retained settings: auto=%v dual=%v", k.autoContinue, k.dual)
	}
}

func TestKnowledgeSettingsPreserveUnknownNestedKeys(t *testing.T) {
	profile := "settings-preserve"
	sessionID := "settings-preserve__chat"
	t.Setenv("AI_DATA_HOME", t.TempDir())
	settingsPath := filepath.Join(paths.SessionDir(profile, sessionID), "settings.json")
	original := []byte(`{"future":{"enabled":true,"labels":["one","two"]},"autoContinue":false}`)
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	k := NewKnowledgePanel()
	k.profile, k.sessionID = profile, sessionID
	k.readSettings()
	k.handleMouse(tea.MouseMsg{X: 1, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if !k.autoContinue || k.dual || k.settingsError != "" {
		t.Fatalf("settings after toggle: auto=%v dual=%v error=%q", k.autoContinue, k.dual, k.settingsError)
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	var future map[string]any
	if err := json.Unmarshal(got["future"], &future); err != nil {
		t.Fatalf("unknown nested key lost: %v", err)
	}
	if future["enabled"] != true || len(future["labels"].([]any)) != 2 {
		t.Fatalf("unknown settings changed: %#v", future)
	}
	var autoContinue bool
	if err := json.Unmarshal(got["autoContinue"], &autoContinue); err != nil || !autoContinue {
		t.Fatalf("autoContinue = %v, error = %v", autoContinue, err)
	}
}

func TestKnowledgeSettingsFailuresPreserveFileAndToggleState(t *testing.T) {
	profile := "settings-errors"
	sessionID := "settings-errors__chat"
	t.Setenv("AI_DATA_HOME", t.TempDir())
	settingsPath := filepath.Join(paths.SessionDir(profile, sessionID), "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	invalid := []byte(`{"autoContinue":`)
	if err := os.WriteFile(settingsPath, invalid, 0o644); err != nil {
		t.Fatal(err)
	}
	k := NewKnowledgePanel()
	k.profile, k.sessionID = profile, sessionID
	k.readSettings()
	if k.settingsError == "" {
		t.Fatal("malformed settings did not produce a visible diagnostic")
	}
	k.handleMouse(tea.MouseMsg{X: 1, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if k.autoContinue || k.dual {
		t.Fatalf("failed update changed displayed toggles: auto=%v dual=%v", k.autoContinue, k.dual)
	}
	got, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(invalid) {
		t.Fatalf("malformed settings were overwritten: %s", got)
	}
	if !strings.Contains(k.View(80, 4), "Settings error:") {
		t.Fatal("settings failure is not visible in panel")
	}
}

func TestKnowledgeSettingsAtomicWriterFailureRollsBackToggle(t *testing.T) {
	profile := "settings-atomic-fail"
	sessionID := "settings-atomic-fail__chat"
	t.Setenv("AI_DATA_HOME", t.TempDir())
	settingsPath := filepath.Join(paths.SessionDir(profile, sessionID), "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"autoContinue":false,"unknown":"keep"}`)
	if err := os.WriteFile(settingsPath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	k := NewKnowledgePanel()
	k.profile, k.sessionID = profile, sessionID
	k.readSettings()
	k.settingsWriter = func(string, []byte) error { return errors.New("injected atomic write failure") }
	k.handleMouse(tea.MouseMsg{X: 1, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if k.autoContinue || k.dual || !strings.Contains(k.settingsError, "injected atomic write failure") {
		t.Fatalf("atomic failure state: auto=%v dual=%v error=%q", k.autoContinue, k.dual, k.settingsError)
	}
	got, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("atomic failure changed existing file: %s", got)
	}
}

func TestKnowledgeSettingsNewFileAndMutuallyExclusiveToggle(t *testing.T) {
	profile := "settings-new"
	sessionID := "settings-new__chat"
	t.Setenv("AI_DATA_HOME", t.TempDir())
	k := NewKnowledgePanel()
	k.profile, k.sessionID = profile, sessionID
	k.readSettings()
	k.handleMouse(tea.MouseMsg{X: 11, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if k.dual == false || k.autoContinue || k.settingsError != "" {
		t.Fatalf("new settings toggle = auto=%v dual=%v error=%q", k.autoContinue, k.dual, k.settingsError)
	}
	data, err := os.ReadFile(k.settingsPath())
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]bool
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	if settings["dual"] != true || settings["autoContinue"] {
		t.Fatalf("new settings = %#v", settings)
	}
}

func TestKnowledgeHeaderHitboxesMatchRenderedCells(t *testing.T) {
	for _, test := range []struct {
		name  string
		width int
	}{
		{name: "partial auto", width: 4},
		{name: "auto only", width: 8},
		{name: "separator", width: 9},
		{name: "partial dual", width: 13},
		{name: "full header", width: 17},
		{name: "wide header", width: 80},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("AI_DATA_HOME", t.TempDir())
			k := NewKnowledgePanel()
			k.profile, k.sessionID = "header-test", "header-test__chat"
			layout := k.headerLayout(test.width)
			view := k.View(test.width, 2)
			header := strings.SplitN(view, "\n", 2)[0]
			if header != layout.rendered {
				t.Fatalf("rendered header does not match hitbox layout: got %q want %q", header, layout.rendered)
			}
			if got := ansi.StringWidth(header); got > test.width {
				t.Fatalf("header width = %d, viewport = %d", got, test.width)
			}
			if strings.Contains(header, "Knowledge") {
				t.Fatalf("removed panel title is still visible: %q", header)
			}
			for name, hitbox := range map[string]terminalCellRange{
				"auto":      layout.auto,
				"separator": layout.separator, "dual": layout.dual,
			} {
				if hitbox.start < 0 || hitbox.end < hitbox.start || hitbox.end > test.width {
					t.Errorf("%s hitbox is outside viewport: %#v width=%d", name, hitbox, test.width)
				}
			}

			click := func(x int) {
				k.handleMouse(tea.MouseMsg{X: x, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
			}
			for _, x := range []int{layout.auto.start, layout.auto.end - 1} {
				if x >= layout.auto.start && x < layout.auto.end {
					k.autoContinue, k.dual = false, false
					click(x)
					if !k.autoContinue || k.dual {
						t.Fatalf("Auto click at cell %d toggled wrong setting: auto=%v dual=%v", x, k.autoContinue, k.dual)
					}
				}
			}
			for _, x := range []int{layout.dual.start, layout.dual.end - 1} {
				if x >= layout.dual.start && x < layout.dual.end {
					k.autoContinue, k.dual = false, false
					click(x)
					if k.autoContinue || !k.dual {
						t.Fatalf("Dual click at cell %d toggled wrong setting: auto=%v dual=%v", x, k.autoContinue, k.dual)
					}
				}
			}
			if layout.separator.start < layout.separator.end {
				k.autoContinue, k.dual = false, false
				click(layout.separator.start)
				if k.autoContinue || k.dual {
					t.Fatalf("separator click toggled a setting: auto=%v dual=%v", k.autoContinue, k.dual)
				}
			}
		})
	}
}

func TestKnowledgePanelCollapsibleSectionsAndScrolling(t *testing.T) {
	k := NewKnowledgePanel()
	k.data = &akcontext.Data{Plans: map[string][]akcontext.PlanStep{
		"Build": {{Text: "first step"}, {Text: "second step"}},
	}}
	k.jobs = []akjobs.Job{{ID: "job-1", Command: "build artifacts", Running: true}}

	plain := ansi.Strip(k.View(80, 30))
	if strings.Contains(plain, "Knowledge") {
		t.Fatal("panel title should not be displayed")
	}
	if k.plansHeaderY < 0 || k.jobsHeaderY < 0 {
		t.Fatalf("section headers are not hit-testable: plans=%d jobs=%d", k.plansHeaderY, k.jobsHeaderY)
	}
	if !strings.Contains(plain, "── ▾ Plans") || !strings.Contains(plain, "── ▾ Jobs") {
		t.Fatalf("expanded section indicators missing: %s", plain)
	}

	k.handleMouse(tea.MouseMsg{Y: k.plansHeaderY, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	plain = ansi.Strip(k.View(80, 30))
	if !k.plansCollapsed || strings.Contains(plain, "first step") || !strings.Contains(plain, "── ▸ Plans") {
		t.Fatalf("Plans did not collapse: %s", plain)
	}
	k.handleMouse(tea.MouseMsg{Y: k.jobsHeaderY, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	plain = ansi.Strip(k.View(80, 30))
	if !k.jobsCollapsed || strings.Contains(plain, "build artifacts") || !strings.Contains(plain, "── ▸ Jobs") {
		t.Fatalf("Jobs did not collapse: %s", plain)
	}

	steps := make([]akcontext.PlanStep, 20)
	for index := range steps {
		steps[index] = akcontext.PlanStep{Text: "long step " + strings.Repeat("detail ", 3)}
	}
	k.data.Plans["Build"] = steps
	k.plansCollapsed = false
	k.jobsCollapsed = false
	k.scrollOffset = 0
	k.View(80, 8)
	k.handleMouse(tea.MouseMsg{Button: tea.MouseButtonWheelDown})
	k.View(80, 8)
	if k.scrollOffset == 0 {
		t.Fatal("mouse wheel did not scroll the full section content")
	}
	k.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("pgdown")})
	k.View(80, 8)
	if k.scrollOffset == 0 {
		t.Fatal("Page Down did not preserve scrolling")
	}
	k.handleKey(tea.KeyMsg{Type: tea.KeyUp})
	if k.scrollOffset < 0 {
		t.Fatalf("scroll offset became negative: %d", k.scrollOffset)
	}
}

func TestKnowledgePanelUsesExplicitCanonicalDomainForTasks(t *testing.T) {
	profile := "Domain Profile"
	sessionID := "domain-profile__source.chat"
	domain := "domain-profile__target"
	t.Setenv("AI_DATA_HOME", t.TempDir())
	taskDir := kanban.KanbanDir(domain, profile)
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	taskPath := filepath.Join(taskDir, "task.md")
	contents := "---\ntitle: Target task\nstatus: progress\nassigned_to: " + sessionID + "\n---\nbody\n"
	if err := os.WriteFile(taskPath, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}

	k := NewKnowledgePanel()
	defer k.Close()
	k.SetProfile(profile)
	k.SetSession(sessionID)
	k.SetDomain(domain)
	if k.currentTask != "Target task" {
		t.Fatalf("current task = %q, want explicit target-domain task", k.currentTask)
	}
}

// TestKnowledgePanelEmpty ensures an uninitialised panel renders without crashing.
func TestKnowledgePanelEmpty(t *testing.T) {
	k := NewKnowledgePanel()
	out := k.View(80, 24)
	if out == "" {
		t.Fatalf("View returned empty string")
	}
}

// TestKnowledgePanelViewSize ensures View updates cached size when given one.
func TestKnowledgePanelViewSize(t *testing.T) {
	k := NewKnowledgePanel()
	k.SetSession("s1")
	k.View(50, 20)
	if k.width != 50 || k.height != 20 {
		t.Fatalf("size not stored: w=%d h=%d", k.width, k.height)
	}
}

// TestKnowledgePanelUpdateWindowSize covers the WindowSizeMsg handler.
func TestKnowledgePanelUpdateWindowSize(t *testing.T) {
	k := NewKnowledgePanel()
	cmd := k.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if cmd != nil {
		t.Fatalf("expected nil cmd, got %v", cmd)
	}
	if k.width != 100 || k.height != 30 {
		t.Fatalf("size not updated: w=%d h=%d", k.width, k.height)
	}
}

// TestContainerChatMode creates a chat panel with the knowledge side panel.
func TestContainerChatMode(t *testing.T) {
	c := NewContainer(stubPanel{})
	c.SetChat(stubPanel{}, "session-a")
	if c.mode != ChatMode {
		t.Fatalf("expected ChatMode, got %v", c.mode)
	}
	if c.knowledgePanel == nil {
		t.Fatalf("expected knowledgePanel to be created")
	}
	if c.knowledgePanel.sessionID != "session-a" {
		t.Fatalf("expected sessionID=session-a, got %q", c.knowledgePanel.sessionID)
	}
	c.RefreshKnowledge()
}

// TestContainerRefreshKnowledgeNoPanel covers the empty-panel branch.
func TestContainerRefreshKnowledgeNoPanel(t *testing.T) {
	c := NewContainer(nil)
	c.RefreshKnowledge()
}
