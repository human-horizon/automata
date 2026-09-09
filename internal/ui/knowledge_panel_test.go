package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// stubPanel is a minimal warp.Panel for tests.
type stubPanel struct{}

func (stubPanel) View(width, height int) string { return "" }
func (stubPanel) Update(msg tea.Msg) tea.Cmd    { return nil }

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
