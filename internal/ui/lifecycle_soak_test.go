package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HumanHorizon/automata/internal/paths"
	"github.com/Starframe/portalis"
	"github.com/fsnotify/fsnotify"
)

func TestKnowledgeAndContextWatcherLifecycleSoak(t *testing.T) {
	profile := "lifecycle-soak"
	sessionID := "lifecycle-soak__chat"
	domain := "lifecycle-soak__folder"
	t.Setenv("AI_DATA_HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(paths.SessionDir(profile, sessionID), "jobs"), 0o755); err != nil {
		t.Fatal(err)
	}

	knowledge := NewKnowledgePanel()
	knowledge.SetProfile(profile)
	knowledge.SetSession(sessionID)
	knowledge.scrollOffset = 9
	knowledge.plansCollapsed = true
	defer knowledge.Close()

	context := NewContextPanel(profile)
	context.SetDomain(domain)
	context.scrollOffset = 6
	context.expandedNotes["preserved"] = true
	defer context.Close()

	for cycle := range 24 {
		knowledge.Activate()
		context.Activate()
		activeNotesWatcher := context.notesWatcher
		context.activeTab = 1
		context.syncActiveWatchers()
		activeKanbanWatcher := context.kanbanPanel.watcher
		if context.notesWatcher != nil || activeKanbanWatcher == nil {
			t.Fatalf("cycle %d: Kanban tab did not take exclusive watcher ownership", cycle)
		}
		waitWatcherEventsClosed(t, activeNotesWatcher.Events)
		context.activeTab = 0
		context.syncActiveWatchers()
		if context.kanbanPanel.watcher != nil || context.notesWatcher == nil {
			t.Fatalf("cycle %d: Content tab did not restore exclusive watcher ownership", cycle)
		}
		waitWatcherEventsClosed(t, activeKanbanWatcher.Events)
		oldKnowledgeWatcher := knowledge.knowledgeWatcher
		oldJobsWatcher := knowledge.jobsWatcher
		oldNotesWatcher := context.notesWatcher
		oldKnowledgeGeneration := knowledge.knowledgeGeneration
		oldJobsGeneration := knowledge.jobsGeneration
		oldNotesGeneration := context.notesGeneration
		if oldKnowledgeWatcher == nil || oldJobsWatcher == nil || oldNotesWatcher == nil {
			t.Fatalf("cycle %d: activation missed a watcher", cycle)
		}

		knowledge.Deactivate()
		context.Deactivate()
		if knowledge.knowledgeWatcher != nil || knowledge.jobsWatcher != nil || context.notesWatcher != nil {
			t.Fatalf("cycle %d: deactivation retained a watcher", cycle)
		}
		knowledge.Update(knowledgeChangedMsg{generation: oldKnowledgeGeneration, watcher: oldKnowledgeWatcher})
		knowledge.Update(jobsChangedMsg{generation: oldJobsGeneration, watcher: oldJobsWatcher})
		context.Update(notesChangedMsg{generation: oldNotesGeneration, watcher: oldNotesWatcher})
		if knowledge.scrollOffset != 9 || !knowledge.plansCollapsed || context.scrollOffset != 6 || !context.expandedNotes["preserved"] {
			t.Fatalf("cycle %d: stale events changed preserved panel state", cycle)
		}
		waitWatcherEventsClosed(t, oldKnowledgeWatcher.Events)
		waitWatcherEventsClosed(t, oldJobsWatcher.Events)
		waitWatcherEventsClosed(t, oldNotesWatcher.Events)
	}
}

func TestChatFamiliarWatcherLifecycleSoak(t *testing.T) {
	profile := "chat-lifecycle-soak"
	sessionID := "chat-lifecycle-soak__chat"
	t.Setenv("AI_DATA_HOME", t.TempDir())
	if err := os.MkdirAll(paths.SessionsDir(profile), 0o755); err != nil {
		t.Fatal(err)
	}
	panel := NewChatPanel(portalis.NewEmulator(sessionID, "chat", "/bin/cat", nil), sessionID, profile)
	defer panel.Close()

	for cycle := range 24 {
		panel.Activate()
		oldWatcher := panel.familiarWatcher
		oldGeneration := panel.familiarGeneration
		if oldWatcher == nil {
			t.Fatalf("cycle %d: activation missed familiar watcher", cycle)
		}
		panel.Deactivate()
		if panel.active || panel.familiarWatcher != nil {
			t.Fatalf("cycle %d: deactivation retained familiar watcher", cycle)
		}
		panel.Update(familiarDetectedMsg{id: "stale", familiarID: "expert", generation: oldGeneration})
		if len(panel.sessions) != 1 || panel.activeIdx != 0 {
			t.Fatalf("cycle %d: stale event changed familiar tabs", cycle)
		}
		waitWatcherEventsClosed(t, oldWatcher.Events)
	}
}

func waitWatcherEventsClosed(t *testing.T, events <-chan fsnotify.Event) {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case _, ok := <-events:
			if !ok {
				return
			}
		case <-timer.C:
			t.Fatal("watcher event channel did not close after deactivation")
		}
	}
}
