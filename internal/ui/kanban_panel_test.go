package ui

import (
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

func TestKanbanPanelShowsPartialReadWarningAndValidTasks(t *testing.T) {
	profile := "Partial Read"
	domain := "partial-read"
	t.Setenv("AI_DATA_HOME", t.TempDir())
	dir := kanban.KanbanDir(domain, profile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "valid.md"), []byte("---\ntitle: Keep this task\nstatus: todo\n---\nvalid body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.md"), []byte("not YAML frontmatter\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	panel := NewKanbanPanel(profile)
	defer panel.Close()
	panel.SetDomain(domain)
	if len(panel.tasks) != 1 || panel.tasks[0].Title != "Keep this task" {
		t.Fatalf("valid tasks were not retained after partial read: %#v", panel.tasks)
	}
	if panel.readWarning == "" {
		t.Fatal("partial read did not retain a diagnostic")
	}
	view := strip(panel.View(120, 12))
	if !strings.Contains(view, "Не все Kanban-задачи загружены") {
		t.Fatal("partial-read warning is not visible")
	}
	if !strings.Contains(view, "Keep this task") {
		t.Fatal("valid task disappeared from the board after partial read")
	}
}

func TestAssignTaskToChatReturnsListenCommandAndWritesBothRecords(t *testing.T) {
	profile := "Assignment Profile"
	domain := "assignment-domain"
	t.Setenv("AI_DATA_HOME", t.TempDir())
	taskDir := kanban.KanbanDir(domain, profile)
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	taskPath := filepath.Join(taskDir, "task.md")
	if err := os.WriteFile(taskPath, []byte("---\ntitle: Build\nstatus: todo\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sessionID := "assignment-profile__chat"
	statusPath := filepath.Join(paths.SessionDir(profile, sessionID), "status.json")
	if err := os.MkdirAll(filepath.Dir(statusPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statusPath, []byte(`{"existing":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	k := NewKanbanPanel(profile)
	k.onTaskAssigned = func(gotSessionID, gotTitle string) tea.Cmd {
		if gotSessionID != sessionID || gotTitle != "Build" {
			t.Errorf("callback payload = %q/%q", gotSessionID, gotTitle)
		}
		return func() tea.Msg { return "listen" }
	}
	task, err := kanban.ReadTask(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := k.assignTaskToChat(task, ChatInfo{SessionID: sessionID}, "progress")
	if err != nil {
		t.Fatal(err)
	}
	if cmd == nil {
		t.Fatal("assignment dropped the callback command")
	}
	if got := cmd(); got != "listen" {
		t.Fatalf("callback command result = %v, want listen", got)
	}
	updated, err := kanban.ReadTask(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	if updated.AssignedTo != sessionID || updated.Status != "progress" {
		t.Fatalf("updated task = %+v", updated)
	}
	statusData, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(statusData), `"task_title": "Build"`) {
		t.Fatalf("status.json did not contain assignment: %s", statusData)
	}
}

func TestAssignTaskToChatFailsBeforeCallbackOnInvalidStatus(t *testing.T) {
	profile := "Assignment Failure Profile"
	domain := "assignment-failure"
	t.Setenv("AI_DATA_HOME", t.TempDir())
	taskDir := kanban.KanbanDir(domain, profile)
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	taskPath := filepath.Join(taskDir, "task.md")
	if err := os.WriteFile(taskPath, []byte("---\ntitle: Build\nstatus: todo\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sessionID := "assignment-failure-profile__chat"
	statusPath := filepath.Join(paths.SessionDir(profile, sessionID), "status.json")
	if err := os.MkdirAll(filepath.Dir(statusPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statusPath, []byte("not-json"), 0o644); err != nil {
		t.Fatal(err)
	}

	called := false
	k := NewKanbanPanel(profile)
	k.onTaskAssigned = func(string, string) tea.Cmd {
		called = true
		return nil
	}
	task, err := kanban.ReadTask(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k.assignTaskToChat(task, ChatInfo{SessionID: sessionID}, "progress"); err == nil {
		t.Fatal("invalid status.json did not fail assignment")
	}
	if called {
		t.Fatal("assignment callback ran after failed durable status write")
	}
	unchanged, err := kanban.ReadTask(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.AssignedTo != "" || unchanged.Status != "todo" {
		t.Fatalf("Kanban task mutated after failed assignment: %+v", unchanged)
	}
}

func TestTaskStatusWritesUseExplicitProfilePath(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	profile := "Profile Ω"
	sessionID := "profile-ω__chat"

	writeTaskToChatStatus(profile, sessionID, "Build", "/tmp/build.md")
	statusPath := filepath.Join(paths.SessionDir(profile, sessionID), "status.json")
	data, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"action": "task_assigned"`) {
		t.Fatalf("assigned status = %s", data)
	}

	writeTaskRemovedFromChat(profile, sessionID, "Build")
	data, err = os.ReadFile(statusPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"action": "task_removed"`) {
		t.Fatalf("removed status = %s", data)
	}
}

func TestKanbanPanelLateAttachesWatcherWhenDirectoryAppears(t *testing.T) {
	profile := "kanban-late-watch"
	domain := "late-domain"
	t.Setenv("AI_DATA_HOME", t.TempDir())
	k := NewKanbanPanel(profile)
	defer k.Close()
	k.SetDomain(domain)

	kanbanDir := kanban.KanbanDir(domain, profile)
	if k.watcher == nil || k.watcherPath != filepath.Dir(kanbanDir) {
		t.Fatalf("missing-kanban watcher = %q, want domain parent %q", k.watcherPath, filepath.Dir(kanbanDir))
	}
	cmd := k.watchKanbanCmd()
	if cmd == nil {
		t.Fatal("missing-kanban watch command is nil")
	}
	messages := make(chan tea.Msg, 1)
	go func() { messages <- cmd() }()
	if err := os.MkdirAll(kanbanDir, 0o755); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-messages:
		if _, ok := msg.(kanbanChangedMsg); !ok {
			t.Fatalf("kanban creation message = %T", msg)
		}
		k.Update(msg)
	case <-time.After(2 * time.Second):
		t.Fatal("domain watcher did not observe late kanban creation")
	}
	if k.watcherPath != kanbanDir {
		t.Fatalf("late watcher path = %q, want %q", k.watcherPath, kanbanDir)
	}

	taskPath := filepath.Join(kanbanDir, "late.md")
	if err := os.WriteFile(taskPath, []byte("---\ntitle: Late\nstatus: todo\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	k.Update(kanbanChangedMsg{})
	if len(k.tasks) != 1 || k.tasks[0].Title != "Late" {
		t.Fatalf("tasks after late watcher attach = %+v", k.tasks)
	}
}

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

func TestKanbanWatcherErrorRecreatesWatcherAndReloads(t *testing.T) {
	for _, test := range []struct {
		name        string
		closeErrors bool
	}{
		{name: "reported error"},
		{name: "closed error channel", closeErrors: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("AI_DATA_HOME", t.TempDir())
			profile := "watch-error"
			domain := "watch-error-domain"
			dir := kanban.KanbanDir(domain, profile)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "task.md"), []byte("---\ntitle: Reloaded\nstatus: todo\n---\nbody\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			k := NewKanbanPanel(profile)
			defer k.Close()
			k.SetDomain(domain)
			oldWatcher := k.watcher
			if oldWatcher == nil {
				t.Fatal("watcher was not initialized")
			}
			k.tasks = nil
			watchErrors := make(chan error, 1)
			if test.closeErrors {
				close(watchErrors)
			} else {
				watchErrors <- errors.New("injected watcher failure")
			}
			k.watchErrors = watchErrors
			k.watchPending = true

			msg := k.watchKanbanCmd()()
			watcherError, ok := msg.(kanbanWatcherErrorMsg)
			if !ok || watcherError.err == nil {
				t.Fatalf("watch command returned %T (%v), want watcher error", msg, msg)
			}
			k.Update(watcherError)

			if k.watcher == nil || k.watcher == oldWatcher {
				t.Fatal("watcher error did not create a fresh watcher")
			}
			if len(k.tasks) != 1 || k.tasks[0].Title != "Reloaded" {
				t.Fatalf("tasks after watcher recovery = %+v, want reloaded task", k.tasks)
			}
			if !k.watchPending {
				t.Fatal("watcher was not re-armed after recovery")
			}
		})
	}
}

// TestNoKanbanTickFallbackExists guards against regressing to a periodic
// poll — the kanban panel must stay event-driven via fsnotify. If a
// `tickCmd` or `kanbanTickMsg` ever comes back, this test fails.
func TestNoKanbanTickFallbackExists(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "internal", "ui", "kanban_panel.go"))
	if err != nil {
		t.Fatalf("read kanban_panel.go: %v", err)
	}
	contents := string(src)
	for _, banned := range []string{"kanbanTickMsg", "tickCmd", "tickPending", "kanbanFallbackInterval"} {
		if strings.Contains(contents, banned) {
			t.Errorf("kanban_panel.go still references %q — the panel must stay event-driven via fsnotify", banned)
		}
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

func TestKanbanPanelKeepsReadableTasksWhenAnotherFileIsInvalid(t *testing.T) {
	profile, domain := "kanban-partial-read", "kanban-partial-read"
	t.Setenv("AI_DATA_HOME", t.TempDir())
	dir := kanban.KanbanDir(domain, profile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "valid.md"), []byte("---\ntitle: Valid task\nstatus: todo\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "invalid.md"), []byte("---\ntitle: Invalid task\nstatus: broken\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	panel := NewKanbanPanel(profile)
	defer panel.Close()
	panel.SetDomain(domain)
	if len(panel.tasks) != 1 || panel.tasks[0].Title != "Valid task" {
		t.Fatalf("panel tasks after partial read = %#v", panel.tasks)
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
