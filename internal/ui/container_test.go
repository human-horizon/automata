package ui

import (
	"os"
	"testing"
	"time"

	"github.com/HumanHorizon/automata/internal/paths"
	"github.com/HumanHorizon/automata/internal/tree"
	"github.com/Starframe/portalis"
	tea "github.com/charmbracelet/bubbletea"
	warp "github.com/starframe-dev/warp"
)

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

