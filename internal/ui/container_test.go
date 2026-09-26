package ui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/HumanHorizon/automata/internal/paths"
	"github.com/HumanHorizon/automata/internal/tree"
	tea "github.com/charmbracelet/bubbletea"
)

func TestRefreshKnowledgeCarriesPartialContextAndJobDiagnostics(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	profile := "partial-panel"
	sessionID := "partial-panel__chat"
	sessionDir := paths.SessionDir(profile, sessionID)
	jobsDir := filepath.Join(sessionDir, "jobs")
	validJobDir := filepath.Join(jobsDir, "valid-job")
	invalidJobDir := filepath.Join(jobsDir, "invalid-job")
	for _, dir := range []string{sessionDir, validJobDir, invalidJobDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "plans.json"), []byte(`[{"name":"valid-plan","steps":["retained step"]}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "status.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	validJob := `{"id":"valid-job","command":"echo retained","pid":` + strconv.Itoa(os.Getpid()) + `,"status":"running"}`
	if err := os.WriteFile(filepath.Join(validJobDir, "job.json"), []byte(validJob), 0o644); err != nil {
		t.Fatal(err)
	}
	invalidJobPath := filepath.Join(invalidJobDir, "job.json")
	if err := os.WriteFile(invalidJobPath, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}

	container := NewContainer(&stubPanel{})
	defer container.Close()
	container.profile = profile
	container.knowledgePanel.profile = profile
	container.knowledgePanel.sessionID = sessionID
	result := container.RefreshKnowledgeCmd()()
	msg, ok := result.(KnowledgeRefreshMsg)
	if !ok {
		t.Fatalf("RefreshKnowledgeCmd returned %T, want KnowledgeRefreshMsg", result)
	}
	if msg.Context == nil || msg.ContextError == "" || !strings.Contains(msg.ContextError, "status.json") {
		t.Fatalf("partial context result = data:%#v error:%q", msg.Context, msg.ContextError)
	}
	if got := msg.Context.Plans["valid-plan"]; len(got) != 1 || got[0].Text != "retained step" {
		t.Fatalf("valid context entry was dropped: %#v", got)
	}
	if len(msg.Jobs) != 1 || msg.Jobs[0].ID != "valid-job" || !strings.Contains(msg.JobsError, invalidJobPath) {
		t.Fatalf("partial jobs result = jobs:%#v error:%q", msg.Jobs, msg.JobsError)
	}

	container.ApplyKnowledgeRefresh(msg)
	view := container.knowledgePanel.View(1000, 24)
	for _, expected := range []string{"Context read warning:", "Jobs read warning:", "valid-plan", "retained step", "echo retained"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("Knowledge panel omitted %q: %q", expected, view)
		}
	}
}

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
	container.SetOnTaskAssigned(func(sessionID, taskTitle string) (tea.Cmd, error) {
		assigned <- sessionID + ":" + taskTitle
		return nil, nil
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
	if _, err := container.contextPanel.kanbanPanel.onTaskAssigned("profile__chat", "Task"); err != nil {
		t.Fatal(err)
	}
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

func TestPlanFractionIsBoundedForTinyWidths(t *testing.T) {
	container := NewContainer(nil)
	for _, width := range []int{1, 2, 5, 10, 21, 35, 36, 80} {
		fraction := container.planFraction(width)
		if fraction < 0 || fraction > 1 {
			t.Fatalf("planFraction(%d) = %v, want [0,1]", width, fraction)
		}
	}
}

func TestContainerViewRecomputesInnerSplitWhenViewportChanges(t *testing.T) {
	container := NewContainer(voidPanel{})
	defer container.Close()
	container.SetPlanWidth(40)

	container.View(130, 24)
	before := container.findBorderX()
	container.View(159, 24)
	after := container.findBorderX()
	want := 159 - 1 - container.PlanWidth()
	if after < want-1 || after > want || after <= before {
		t.Fatalf("inner split border after viewport growth = %d, before = %d, want %d±1 with fixed plan width %d", after, before, want, container.PlanWidth())
	}
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
