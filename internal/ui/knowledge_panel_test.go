package ui

import (
	"os"
	"path/filepath"
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
