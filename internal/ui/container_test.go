package ui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/HumanHorizon/automata/internal/paths"
	"github.com/HumanHorizon/automata/internal/tree"
	"github.com/Starframe/portalis"
	tea "github.com/charmbracelet/bubbletea"
	warp "github.com/starframe-dev/warp"
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

func TestContainerActivatesOnlyVisiblePanelWatchers(t *testing.T) {
	profile := "container-lifecycle"
	t.Setenv("AI_DATA_HOME", t.TempDir())
	chatSessionID := profile + "__chat"
	if err := os.MkdirAll(paths.SessionDir(profile, chatSessionID), 0o755); err != nil {
		t.Fatal(err)
	}

	container := NewContainer(nil)
	defer container.Close()
	container.SetProfile(profile)
	chat := NewChatPanel(nil, chatSessionID, profile)
	container.SetChat(chat, chatSessionID)
	container.Activate()
	if !chat.active || chat.familiarWatcher == nil || !container.knowledgePanel.active {
		t.Fatal("chat mode did not activate visible chat and Knowledge panels")
	}
	if container.knowledgePanel.knowledgeWatcher == nil {
		t.Fatal("chat mode did not activate Knowledge watcher")
	}

	container.SetFolder(&tree.Item{Name: "Folder", IsFolder: true})
	container.Activate()
	contextPanel := container.contextPanel
	if chat.active || chat.familiarWatcher != nil || container.knowledgePanel.active || container.knowledgePanel.knowledgeWatcher != nil {
		t.Fatal("folder mode retained hidden chat/Knowledge watchers")
	}
	if !contextPanel.active || contextPanel.notesWatcher == nil || contextPanel.kanbanPanel.watcher != nil {
		t.Fatal("folder mode did not limit watchers to the visible Content tab")
	}

	contextPanel.Update(tea.KeyMsg{Type: tea.KeyRight})
	kanban := contextPanel.kanbanPanel
	oldWatcher := kanban.watcher
	oldGeneration := kanban.generation
	if contextPanel.notesWatcher != nil || !kanban.active || oldWatcher == nil {
		t.Fatal("Kanban tab switch did not transfer watcher ownership")
	}
	kanban.readWarning = "preserved warning"
	contextPanel.scrollOffset = 6
	container.SetChat(NewChatPanel(nil, profile+"__second", profile), profile+"__second")
	if contextPanel.active || kanban.watcher != nil || kanban.active {
		t.Fatal("chat mode retained hidden Context/Kanban watchers")
	}
	kanban.Update(kanbanChangedMsg{generation: oldGeneration, watcher: oldWatcher})
	if kanban.readWarning != "preserved warning" || contextPanel.scrollOffset != 6 || contextPanel.activeTab != 1 {
		t.Fatal("stale Kanban event or deactivation discarded hidden panel state")
	}
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

func TestContainerCoalescesDragResizesAndFlushesLatestAtRelease(t *testing.T) {
	em := portalis.NewEmulator("resize-session", "chat", "/bin/sh", nil)
	chat := NewChatPanel(em, "resize-session", "")
	container := NewContainer(chat)
	t.Cleanup(container.Close)
	container.Update(warp.ResizeMsg{Width: 100, Height: 24})
	container.View(100, 24)

	borderX := container.findBorderX()
	if borderX < 0 {
		t.Fatal("chat/knowledge split border was not found")
	}
	container.handleMouse(tea.MouseMsg{X: borderX, Y: 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if !container.planDragActive {
		t.Fatal("split press did not begin a drag")
	}

	container.handleMouse(tea.MouseMsg{X: borderX - 5, Y: 1, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	firstAppliedWidth := chat.width
	container.lastDragResizeAt = time.Now()
	timer := container.handleMouse(tea.MouseMsg{X: borderX - 10, Y: 1, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	container.handleMouse(tea.MouseMsg{X: borderX - 15, Y: 1, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	if timer == nil || !container.dragResizePending {
		t.Fatal("rapid drag did not schedule a coalesced resize")
	}
	if chat.width != firstAppliedWidth {
		t.Fatalf("terminal resized before the coalescing interval: got %d, want %d", chat.width, firstAppliedWidth)
	}

	timerMessage, ok := timer().(chatResizeDueMsg)
	if !ok {
		t.Fatal("resize timer did not return chatResizeDueMsg")
	}
	container.Update(timerMessage)
	if want := container.terminalPanelWidth(); chat.width != want {
		t.Fatalf("coalesced resize width = %d, want latest width %d", chat.width, want)
	}

	container.handleMouse(tea.MouseMsg{X: borderX - 20, Y: 1, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	if container.planDragActive {
		t.Fatal("mouse release left plan drag active")
	}
	if want := container.terminalPanelWidth(); chat.width != want {
		t.Fatalf("release resize width = %d, want exact final width %d", chat.width, want)
	}
}

func TestContainerReleaseInvalidatesPendingDragResize(t *testing.T) {
	em := portalis.NewEmulator("resize-release", "chat", "/bin/sh", nil)
	chat := NewChatPanel(em, "resize-release", "")
	container := NewContainer(chat)
	t.Cleanup(container.Close)
	container.Update(warp.ResizeMsg{Width: 100, Height: 24})
	container.View(100, 24)

	borderX := container.findBorderX()
	container.handleMouse(tea.MouseMsg{X: borderX, Y: 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	container.lastDragResizeAt = time.Now()
	timer := container.handleMouse(tea.MouseMsg{X: borderX - 8, Y: 1, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	if timer == nil {
		t.Fatal("expected a pending drag resize timer")
	}

	container.handleMouse(tea.MouseMsg{X: borderX - 12, Y: 1, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	finalWidth := chat.width
	staleMessage, ok := timer().(chatResizeDueMsg)
	if !ok {
		t.Fatal("stale timer did not return chatResizeDueMsg")
	}
	container.Update(staleMessage)
	if chat.width != finalWidth {
		t.Fatalf("stale timer changed terminal width to %d, want final %d", chat.width, finalWidth)
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
