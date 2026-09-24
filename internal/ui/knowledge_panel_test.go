package ui

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HumanHorizon/automata/internal/kanban"
	"github.com/HumanHorizon/automata/internal/paths"
	tea "github.com/charmbracelet/bubbletea"
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
	oldKnowledge := k.knowledgeWatcher
	oldJobs := k.jobsWatcher
	if oldKnowledge == nil || oldJobs == nil {
		t.Fatal("test setup did not create both watchers")
	}

	knowledgeCmd := k.watchKnowledgeCmd()
	if knowledgeCmd == nil {
		t.Fatal("knowledge watcher command is nil")
	}
	knowledgeMessages := make(chan tea.Msg, 1)
	go func() { knowledgeMessages <- knowledgeCmd() }()
	go func() { oldKnowledge.Errors <- errors.New("synthetic knowledge watcher error") }()
	select {
	case msg := <-knowledgeMessages:
		errorMsg, ok := msg.(knowledgeWatcherErrorMsg)
		if !ok || errorMsg.err == nil {
			t.Fatalf("knowledge watcher error message = %#v", msg)
		}
		k.Update(msg)
	case <-time.After(2 * time.Second):
		t.Fatal("closed knowledge watcher did not report recovery")
	}
	if k.knowledgeWatcher == nil || k.knowledgeWatcher == oldKnowledge {
		t.Fatal("knowledge watcher was not recreated")
	}

	jobsCmd := k.watchJobsCmd()
	if jobsCmd == nil {
		t.Fatal("jobs watcher command is nil")
	}
	jobsMessages := make(chan tea.Msg, 1)
	go func() { jobsMessages <- jobsCmd() }()
	go func() { oldJobs.Errors <- errors.New("synthetic jobs watcher error") }()
	select {
	case msg := <-jobsMessages:
		errorMsg, ok := msg.(jobsWatcherErrorMsg)
		if !ok || errorMsg.err == nil {
			t.Fatalf("jobs watcher error message = %#v", msg)
		}
		k.Update(msg)
	case <-time.After(2 * time.Second):
		t.Fatal("closed jobs watcher did not report recovery")
	}
	if k.jobsWatcher == nil || k.jobsWatcher == oldJobs {
		t.Fatal("jobs watcher was not recreated")
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
	k.handleMouse(tea.MouseMsg{X: 12, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
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
	k.handleMouse(tea.MouseMsg{X: 12, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
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
	k.handleMouse(tea.MouseMsg{X: 12, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
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
	k.handleMouse(tea.MouseMsg{X: 22, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
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
