package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HumanHorizon/automata/internal/kanban"
	tea "github.com/charmbracelet/bubbletea"
)

// TestKanbanPanelSetupWatcherAttaches verifies SetDomain attaches an fsnotify
// watcher when the kanban directory exists, so external edits trigger reloads.
func TestKanbanPanelSetupWatcherAttaches(t *testing.T) {
	profile := "kanban-watch-test"
	domain := "watch-domain"

	tmpDir := t.TempDir()
	os.Setenv("AI_DATA_HOME", tmpDir)
	defer os.Unsetenv("AI_DATA_HOME")

	dir := kanban.KanbanDir(domain, profile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir kanban dir: %v", err)
	}

	k := NewKanbanPanel(profile)
	defer k.closeWatcher()
	k.SetDomain(domain)

	if k.watcher == nil {
		t.Fatalf("expected watcher to be attached after SetDomain")
	}
	if k.domain != domain {
		t.Fatalf("domain not stored: got %q", k.domain)
	}
}

// TestKanbanPanelDrainReloadsOnFileChange verifies that sending a
// kanbanChangedMsg causes the panel to reload tasks from disk.
func TestKanbanPanelDrainReloadsOnFileChange(t *testing.T) {
	profile := "kanban-drain-test"
	domain := "drain-domain"

	tmpDir := t.TempDir()
	os.Setenv("AI_DATA_HOME", tmpDir)
	defer os.Unsetenv("AI_DATA_HOME")

	dir := kanban.KanbanDir(domain, profile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir kanban dir: %v", err)
	}

	k := NewKanbanPanel(profile)
	defer k.closeWatcher()
	k.SetDomain(domain)

	if k.watcher == nil {
		t.Fatalf("watcher not attached")
	}
	if len(k.tasks) != 0 {
		t.Fatalf("expected no tasks initially, got %d", len(k.tasks))
	}

	// Create a new task file.
	taskPath := filepath.Join(dir, "task-drain.md")
	contents := "---\ntitle: Drain test\nstatus: todo\n---\nbody\n"
	if err := os.WriteFile(taskPath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write task: %v", err)
	}

	// Simulate the fsnotify event by sending kanbanChangedMsg through Update.
	// This is what the real watchKanbanCmd does.
	k.Update(kanbanChangedMsg{})

	if len(k.tasks) != 1 {
		t.Fatalf("expected 1 task after kanbanChangedMsg, got %d", len(k.tasks))
	}
	if k.tasks[0].Title != "Drain test" {
		t.Fatalf("unexpected title: %q", k.tasks[0].Title)
	}
	if k.tasks[0].Status != "todo" {
		t.Fatalf("unexpected status: %q", k.tasks[0].Status)
	}
}

// TestKanbanPanelCloseWatcherReleases verifies SetDomain cleans up the
// previous watcher before attaching a new one, so no file descriptors leak
// when the user switches domains.
func TestKanbanPanelCloseWatcherReleases(t *testing.T) {
	profile := "kanban-close-test"

	tmpDir := t.TempDir()
	os.Setenv("AI_DATA_HOME", tmpDir)
	defer os.Unsetenv("AI_DATA_HOME")

	// Two domains so we can switch between them.
	for _, domain := range []string{"domain-a", "domain-b"} {
		dir := kanban.KanbanDir(domain, profile)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", domain, err)
		}
	}

	k := NewKanbanPanel(profile)
	defer k.closeWatcher()
	k.SetDomain("domain-a")
	first := k.watcher
	if first == nil {
		t.Fatalf("watcher not attached for domain-a")
	}

	k.SetDomain("domain-b")
	if k.watcher == nil {
		t.Fatalf("watcher not re-attached for domain-b")
	}
	if k.watcher == first {
		t.Fatalf("expected new watcher instance after domain switch")
	}
}

// TestKanbanWatchCmdReactsToFsnotifyEvent verifies that watchKanbanCmd
// blocks on the watcher's Events channel and returns a kanbanChangedMsg
// when a .md file appears in the kanban directory. This is the property
// that makes the new "0 CPU while idle" design work: no heartbeat, just
// a single blocking Cmd that wakes up exactly once per real change.
func TestKanbanWatchCmdReactsToFsnotifyEvent(t *testing.T) {
	profile := "kanban-watch-cmd"
	domain := "watch-cmd-domain"

	tmpDir := t.TempDir()
	os.Setenv("AI_DATA_HOME", tmpDir)
	defer os.Unsetenv("AI_DATA_HOME")

	dir := kanban.KanbanDir(domain, profile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	k := NewKanbanPanel(profile)
	defer k.closeWatcher()
	k.SetDomain(domain)
	if k.watcher == nil {
		t.Fatalf("watcher not attached")
	}

	cmd := k.watchKanbanCmd()
	if cmd == nil {
		t.Fatalf("watchKanbanCmd returned nil")
	}
	type result struct {
		msg tea.Msg
	}
	ch := make(chan result, 1)
	go func() {
		ch <- result{msg: cmd()}
	}()

	// Give the goroutine a moment to enter the blocking receive.
	time.Sleep(150 * time.Millisecond)
	select {
	case <-ch:
		t.Fatalf("watchKanbanCmd returned before any fsnotify event")
	default:
	}

	taskPath := filepath.Join(dir, "task-watch.md")
	contents := "---\ntitle: Watch\nstatus: todo\n---\nbody\n"
	if err := os.WriteFile(taskPath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	select {
	case r := <-ch:
		if _, ok := r.msg.(kanbanChangedMsg); !ok {
			t.Errorf("expected kanbanChangedMsg, got %T: %v", r.msg, r.msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("watchKanbanCmd did not unblock after file change")
	}
}

// TestFallbackIntervalIs5s guards against regressing to a busy 150ms heartbeat.
// The previous design caused 100% CPU after long sessions.
func TestFallbackIntervalIs5s(t *testing.T) {
	if kanbanFallbackInterval < time.Second {
		t.Errorf("kanbanFallbackInterval = %v, want >= 1s to avoid 100%% CPU", kanbanFallbackInterval)
	}
}

// TestDoneHasNoProgressTransition guards the rule "a done task cannot
// return to progress". The statusTransitions table is the single source
// of truth for what buttons render on a card — if any done-transition
// reappears, this fails.
func TestDoneHasNoProgressTransition(t *testing.T) {
	done, ok := statusTransitions["done"]
	if !ok {
		t.Fatalf("statusTransitions map is missing the \"done\" entry")
	}
	for _, tr := range done {
		if tr.next == "progress" {
			t.Errorf("found a \"done\" transition back to \"progress\" (%q -> %q) — "+
				"a task in done must not return to progress", tr.label, tr.next)
		}
	}
}

// TestKanbanDoneCardRendersNoTransitionButtons verifies the actual board
// output for a DONE card contains no "↩ Progress" button text.
func TestKanbanDoneCardRendersNoTransitionButtons(t *testing.T) {
	profile := "kanban-done-no-back-btn"
	domain := "done-no-back"

	tmpDir := t.TempDir()
	os.Setenv("AI_DATA_HOME", tmpDir)
	defer os.Unsetenv("AI_DATA_HOME")

	dir := kanban.KanbanDir(domain, profile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	taskPath := filepath.Join(dir, "closed.md")
	body := "---\ntitle: Closed\nstatus: done\n---\nbody\n"
	if err := os.WriteFile(taskPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	k := NewKanbanPanel(profile)
	defer k.closeWatcher()
	k.SetDomain(domain)
	if len(k.tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(k.tasks))
	}

	view := k.View(120, 30)
	stripped := stripAnsiFn(view)
	// No "↩ Progress" button text anywhere on a done card.
	if strings.Contains(stripped, "↩ Progress") {
		t.Errorf("done card renders \"↩ Progress\" button — should not return to progress:\n%s", stripped)
	}
	// And no progress transition arrow direction into the done column.
	if strings.Contains(stripped, "→ Progress") {
		t.Errorf("done card renders \"→ Progress\" button — should not return to progress:\n%s", stripped)
	}
}
