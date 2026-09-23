package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/HumanHorizon/automata/internal/paths"
	"github.com/HumanHorizon/automata/internal/tree"
	tea "github.com/charmbracelet/bubbletea"
)

func writeContainerMemoryFixture(t *testing.T, profile, domain, title string) {
	t.Helper()
	domainDir := paths.DomainDir(profile, domain)
	if err := os.MkdirAll(domainDir, 0o755); err != nil {
		t.Fatalf("mkdir domain: %v", err)
	}
	if err := os.WriteFile(filepath.Join(domainDir, "notes.json"), []byte(`[{"title":"`+title+`"}]`), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}
}

func TestContainerPreservesChatsAndAssignmentCallbackBeforeLazyContextPanel(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	container := NewContainer(nil)
	chats := []ChatInfo{{Name: "Chat", SessionID: "profile__chat"}}
	assigned := make(chan string, 1)
	container.SetChats(chats)
	container.SetOnTaskAssigned(func(sessionID, taskTitle string) tea.Cmd {
		assigned <- sessionID + ":" + taskTitle
		return nil
	})

	container.SetFolder(&tree.Item{Name: "Projects", IsFolder: true})
	if container.contextPanel == nil || container.contextPanel.kanbanPanel == nil {
		t.Fatal("SetFolder did not create the context/kanban panels")
	}
	if len(container.contextPanel.kanbanPanel.chats) != 1 || container.contextPanel.kanbanPanel.chats[0] != chats[0] {
		t.Fatalf("lazy context panel lost chats: %+v", container.contextPanel.kanbanPanel.chats)
	}
	if container.contextPanel.kanbanPanel.onTaskAssigned == nil {
		t.Fatal("lazy context panel lost assignment callback")
	}
	container.contextPanel.kanbanPanel.onTaskAssigned("profile__chat", "Task")
	select {
	case got := <-assigned:
		if got != "profile__chat:Task" {
			t.Fatalf("assignment callback payload = %q", got)
		}
	default:
		t.Fatal("assignment callback was not invoked")
	}
	container.Close()
}

func TestContainerRefreshKnowledgeCmdReadsCanonicalMemory(t *testing.T) {
	tests := []struct {
		name    string
		profile string
		title   string
	}{
		{name: "default", title: "Default memory"},
		{name: "unicode", profile: "Проект Ω", title: "Unicode memory"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dataHome := t.TempDir()
			t.Setenv("AI_DATA_HOME", dataHome)
			t.Setenv("AI_PROFILE", "")

			container := NewContainer(nil)
			container.SetProfile(tt.profile)
			container.SetFolder(&tree.Item{Name: "Knowledge", IsFolder: true})
			t.Cleanup(func() { container.contextPanel.closeNotesWatcher() })

			writeContainerMemoryFixture(t, tt.profile, container.contextPanel.domain, tt.title)

			result := container.RefreshKnowledgeCmd()()
			msg, ok := result.(KnowledgeRefreshMsg)
			if !ok {
				t.Fatalf("RefreshKnowledgeCmd returned %T, want KnowledgeRefreshMsg", result)
			}
			if msg.Memory == nil || len(msg.Memory.Notes) != 1 || msg.Memory.Notes[0].Title != tt.title {
				t.Fatalf("expected %q memory, got %+v", tt.title, msg.Memory)
			}
		})
	}
}
