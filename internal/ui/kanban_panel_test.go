package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HumanHorizon/automata/internal/atomicfile"
	"github.com/HumanHorizon/automata/internal/kanban"
	"github.com/HumanHorizon/automata/internal/paths"
	tea "github.com/charmbracelet/bubbletea"
)

func clickKanbanTransition(t *testing.T, panel *KanbanPanel, columnIndex int, transition string) {
	t.Helper()
	column := panel.colPanels[columnIndex]
	column.width = 40
	column.height = 20
	for y := 1; y < 40; y++ {
		row, button := column.hitTest(y, 1)
		if row == 0 && button == transition {
			column.handleMouse(tea.MouseMsg{Y: y, X: 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
			return
		}
	}
	t.Fatalf("transition %q not found in column %d", transition, columnIndex)
}

func newTransitionFixture(t *testing.T, profile, domain, status, assignedTo string) (*KanbanPanel, string, string, []byte, []byte) {
	t.Helper()
	t.Setenv("AI_DATA_HOME", t.TempDir())
	taskPath := filepath.Join(kanban.KanbanDir(domain, profile), "task.md")
	if err := os.MkdirAll(filepath.Dir(taskPath), 0o755); err != nil {
		t.Fatal(err)
	}
	assignedField := ""
	if assignedTo != "" {
		assignedField = "assigned_to: " + assignedTo + "\n"
	}
	taskBefore := []byte("---\ntitle: Build\nstatus: " + status + "\n" + assignedField + "---\nbody\n")
	if err := os.WriteFile(taskPath, taskBefore, 0o644); err != nil {
		t.Fatal(err)
	}
	statusPath := ""
	statusBefore := []byte(`{"action":"existing"}`)
	if assignedTo != "" {
		statusPath = filepath.Join(paths.SessionDir(profile, assignedTo), "status.json")
		if err := os.MkdirAll(filepath.Dir(statusPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(statusPath, statusBefore, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	panel := NewKanbanPanel(profile)
	panel.domain = domain
	panel.reload()
	t.Cleanup(panel.Close)
	return panel, taskPath, statusPath, taskBefore, statusBefore
}

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

func TestCreateTaskUsesCollisionSafeName(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	profile := "collision-safe"
	domain := "collision-domain"
	dir := kanban.KanbanDir(domain, profile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, time.September, 25, 12, 30, 45, 0, time.UTC)
	firstPath := filepath.Join(dir, "task-2026-09-25T12-30-45.md")
	original := []byte("---\ntitle: Existing\nstatus: todo\n---\nkeep\n")
	if err := os.WriteFile(firstPath, original, 0o644); err != nil {
		t.Fatal(err)
	}

	panel := NewKanbanPanel(profile)
	panel.domain = domain
	if err := panel.createTaskAt(createdAt); err != nil {
		t.Fatal(err)
	}
	if err := panel.createTaskAt(createdAt); err != nil {
		t.Fatal(err)
	}

	gotOriginal, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotOriginal) != string(original) {
		t.Fatalf("existing collision file was overwritten: %q", gotOriginal)
	}
	for _, name := range []string{"task-2026-09-25T12-30-45-2.md", "task-2026-09-25T12-30-45-3.md"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("collision-safe task %s was not created: %v", name, err)
		}
	}
	if len(panel.tasks) != 3 {
		t.Fatalf("reloaded tasks = %d, want 3", len(panel.tasks))
	}
}

func TestKanbanCreateErrorIsVisibleOnBoard(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	profile := "create-error"
	domain := "create-error-domain"
	kanbanPath := kanban.KanbanDir(domain, profile)
	if err := os.MkdirAll(filepath.Dir(kanbanPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kanbanPath, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	panel := NewKanbanPanel(profile)
	panel.domain = domain
	panel.handleMouse(tea.MouseMsg{Y: 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if panel.assignmentErr == "" {
		t.Fatal("failed task creation did not retain an error")
	}
	view := strip(panel.View(120, 12))
	if !strings.Contains(view, "Ошибка Kanban-действия") {
		t.Fatalf("task creation error is not visible: %q", view)
	}
}

func TestAssignedPendingTaskTransitionToTodoRollsBackSnapshots(t *testing.T) {
	const profile, domain, sessionID = "pending-rollback", "pending-rollback-domain", "pending-rollback__chat"
	panel, taskPath, statusPath, taskBefore, statusBefore := newTransitionFixture(t, profile, domain, "pending", sessionID)
	writeErr := errors.New("injected Kanban commit failure")
	previousWriter := assignKanbanTaskStatus
	assignKanbanTaskStatus = func(string, string, string) (kanban.Task, kanban.Task, error) {
		return kanban.Task{}, kanban.Task{}, writeErr
	}
	t.Cleanup(func() { assignKanbanTaskStatus = previousWriter })

	clickKanbanTransition(t, panel, 1, "todo")

	gotTask, err := os.ReadFile(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotTask) != string(taskBefore) {
		t.Fatalf("task snapshot changed after failed transition: %q", gotTask)
	}
	gotStatus, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotStatus) != string(statusBefore) {
		t.Fatalf("chat status snapshot changed after failed transition: %q", gotStatus)
	}
	if !strings.Contains(panel.assignmentErr, writeErr.Error()) {
		t.Fatalf("transition error not visible: %q", panel.assignmentErr)
	}
}

func TestAssignedPendingTaskTransitionToTodoKeepsCommittedSnapshots(t *testing.T) {
	const profile, domain, sessionID = "pending-committed", "pending-committed-domain", "pending-committed__chat"
	panel, taskPath, statusPath, _, _ := newTransitionFixture(t, profile, domain, "pending", sessionID)
	previousWriter := assignKanbanTaskStatus
	assignKanbanTaskStatus = func(path, chat, status string) (kanban.Task, kanban.Task, error) {
		previous, updated, err := kanban.AssignTaskAndStatus(path, chat, status)
		if err != nil {
			return previous, updated, err
		}
		return previous, updated, &atomicfile.CommittedError{Path: path, Err: errors.New("directory sync warning")}
	}
	t.Cleanup(func() { assignKanbanTaskStatus = previousWriter })

	clickKanbanTransition(t, panel, 1, "todo")

	updated, err := kanban.ReadTask(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "todo" || updated.AssignedTo != "" {
		t.Fatalf("committed transition was rolled back: %+v", updated)
	}
	statusData, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(statusData), `"action": "task_removed"`) {
		t.Fatalf("assigned chat was not notified: %s", statusData)
	}
	if !strings.Contains(panel.actionWarning, "directory sync warning") {
		t.Fatalf("committed warning is not visible: %q", panel.actionWarning)
	}
}

func TestAssignTaskToChatKeepsCommittedKanbanWrite(t *testing.T) {
	const profile, domain, sessionID = "task-commit-warning", "task-commit-warning-domain", "task-commit-warning__chat"
	panel, taskPath, _, _, _ := newTransitionFixture(t, profile, domain, "todo", "")
	statusPath := filepath.Join(paths.SessionDir(profile, sessionID), "status.json")
	previousWriter := assignKanbanTaskStatus
	assignKanbanTaskStatus = func(path, chat, status string) (kanban.Task, kanban.Task, error) {
		previous, updated, err := kanban.AssignTaskAndStatus(path, chat, status)
		if err != nil {
			return previous, updated, err
		}
		return previous, updated, &atomicfile.CommittedError{Path: path, Err: errors.New("directory sync warning")}
	}
	t.Cleanup(func() { assignKanbanTaskStatus = previousWriter })
	callbackCalled := false
	panel.onTaskAssigned = func(string, string) (tea.Cmd, error) {
		callbackCalled = true
		return nil, nil
	}
	task, err := kanban.ReadTask(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := panel.assignTaskToChat(task, ChatInfo{SessionID: sessionID}, "progress"); err != nil {
		t.Fatal(err)
	}
	updated, err := kanban.ReadTask(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	if updated.AssignedTo != sessionID || updated.Status != "progress" || !callbackCalled {
		t.Fatalf("committed assignment/callback lost: task=%+v callback=%v", updated, callbackCalled)
	}
	statusData, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(statusData), `"action": "task_assigned"`) {
		t.Fatalf("assigned chat status was not written: %s", statusData)
	}
	if !strings.Contains(panel.actionWarning, "directory sync warning") {
		t.Fatalf("committed warning is not visible: %q", panel.actionWarning)
	}
}

func TestDeleteAssignedTaskContinuesAfterCommittedStatusWarning(t *testing.T) {
	const profile, domain, sessionID = "delete-committed-warning", "delete-committed-warning-domain", "delete-committed-warning__chat"
	panel, taskPath, statusPath, _, _ := newTransitionFixture(t, profile, domain, "progress", sessionID)
	previousWriter := writeRemovedChatTaskStatus
	writeRemovedChatTaskStatus = func(profile, chat, title string) error {
		if err := writeTaskRemovedFromChat(profile, chat, title); err != nil {
			return err
		}
		return &atomicfile.CommittedError{Path: statusPath, Err: errors.New("directory sync warning")}
	}
	t.Cleanup(func() { writeRemovedChatTaskStatus = previousWriter })
	task, err := kanban.ReadTask(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := panel.deleteTask(task); err != nil {
		t.Fatalf("delete after committed chat status: %v", err)
	}
	if _, err := os.Stat(taskPath); !os.IsNotExist(err) {
		t.Fatalf("committed deletion left task file: %v", err)
	}
	if !strings.Contains(panel.actionWarning, "directory sync warning") {
		t.Fatalf("committed warning is not visible: %q", panel.actionWarning)
	}
}

func TestDeleteAssignedTaskRollsBackTaskAndStatusSnapshots(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	profile := "delete-rollback"
	domain := "delete-domain"
	taskDir := kanban.KanbanDir(domain, profile)
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	taskPath := filepath.Join(taskDir, "task.md")
	taskBefore := []byte("---\ntitle: Build\nstatus: progress\nassigned_to: delete-rollback__chat\n---\nbody\n")
	if err := os.WriteFile(taskPath, taskBefore, 0o644); err != nil {
		t.Fatal(err)
	}
	sessionID := "delete-rollback__chat"
	statusPath := filepath.Join(paths.SessionDir(profile, sessionID), "status.json")
	if err := os.MkdirAll(filepath.Dir(statusPath), 0o755); err != nil {
		t.Fatal(err)
	}
	statusBefore := []byte(`{"action":"existing","task_title":"Build"}`)
	if err := os.WriteFile(statusPath, statusBefore, 0o644); err != nil {
		t.Fatal(err)
	}
	task, err := kanban.ReadTask(taskPath)
	if err != nil {
		t.Fatal(err)
	}

	panel := NewKanbanPanel(profile)
	err = panel.deleteTaskWith(task, func(string) error { return errors.New("injected delete failure") })
	if err == nil || !strings.Contains(err.Error(), "injected delete failure") {
		t.Fatalf("delete error = %v, want injected failure", err)
	}
	gotTask, err := os.ReadFile(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotTask) != string(taskBefore) {
		t.Fatalf("task snapshot was not restored: %q", gotTask)
	}
	gotStatus, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotStatus) != string(statusBefore) {
		t.Fatalf("status snapshot was not restored: %q", gotStatus)
	}
}

func TestDeleteAssignedTaskNotifiesChatAfterRemoval(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	profile := "delete-commit"
	domain := "delete-commit-domain"
	taskDir := kanban.KanbanDir(domain, profile)
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	taskPath := filepath.Join(taskDir, "task.md")
	if err := os.WriteFile(taskPath, []byte("---\ntitle: Build\nstatus: progress\nassigned_to: delete-commit__chat\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sessionID := "delete-commit__chat"
	task, err := kanban.ReadTask(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	panel := NewKanbanPanel(profile)
	if err := panel.deleteTask(task); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(taskPath); !os.IsNotExist(err) {
		t.Fatalf("task file remains after delete: %v", err)
	}
	statusPath := filepath.Join(paths.SessionDir(profile, sessionID), "status.json")
	statusData, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(statusData), `"action": "task_removed"`) {
		t.Fatalf("assigned chat was not notified: %s", statusData)
	}
}

func TestReassignTaskToChatNotifiesBothChats(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	profile := "reassign-profile"
	taskPath := filepath.Join(kanban.KanbanDir("reassign-domain", profile), "task.md")
	if err := os.MkdirAll(filepath.Dir(taskPath), 0o755); err != nil {
		t.Fatal(err)
	}
	oldSession := "reassign-profile__old"
	newSession := "reassign-profile__new"
	taskBefore := []byte("---\ntitle: Build\nstatus: pending\nassigned_to: reassign-profile__old\n---\nbody\n")
	if err := os.WriteFile(taskPath, taskBefore, 0o644); err != nil {
		t.Fatal(err)
	}
	oldStatusPath := filepath.Join(paths.SessionDir(profile, oldSession), "status.json")
	newStatusPath := filepath.Join(paths.SessionDir(profile, newSession), "status.json")
	for path, data := range map[string][]byte{
		oldStatusPath: []byte(`{"action":"old"}`),
		newStatusPath: []byte(`{"action":"new"}`),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	task, err := kanban.ReadTask(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	panel := NewKanbanPanel(profile)
	if _, err := panel.assignTaskToChat(task, ChatInfo{SessionID: newSession}, "progress"); err != nil {
		t.Fatal(err)
	}
	updated, err := kanban.ReadTask(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	if updated.AssignedTo != newSession || updated.Status != "progress" {
		t.Fatalf("reassigned task = %+v", updated)
	}
	oldStatus, err := os.ReadFile(oldStatusPath)
	if err != nil {
		t.Fatal(err)
	}
	newStatus, err := os.ReadFile(newStatusPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(oldStatus), `"action": "task_removed"`) {
		t.Fatalf("previous chat was not notified: %s", oldStatus)
	}
	if !strings.Contains(string(newStatus), `"action": "task_assigned"`) {
		t.Fatalf("new chat was not notified: %s", newStatus)
	}
}

func TestReassignTaskToChatRollsBackAllSnapshotsWhenStartFails(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	profile := "reassign-rollback"
	taskPath := filepath.Join(kanban.KanbanDir("reassign-rollback-domain", profile), "task.md")
	if err := os.MkdirAll(filepath.Dir(taskPath), 0o755); err != nil {
		t.Fatal(err)
	}
	oldSession := "reassign-rollback__old"
	newSession := "reassign-rollback__new"
	taskBefore := []byte("---\ntitle: Build\nstatus: pending\nassigned_to: reassign-rollback__old\n---\nbody\n")
	if err := os.WriteFile(taskPath, taskBefore, 0o644); err != nil {
		t.Fatal(err)
	}
	oldStatusPath := filepath.Join(paths.SessionDir(profile, oldSession), "status.json")
	newStatusPath := filepath.Join(paths.SessionDir(profile, newSession), "status.json")
	oldStatusBefore := []byte(`{"action":"old"}`)
	newStatusBefore := []byte(`{"action":"new"}`)
	for path, data := range map[string][]byte{
		oldStatusPath: oldStatusBefore,
		newStatusPath: newStatusBefore,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	task, err := kanban.ReadTask(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	panel := NewKanbanPanel(profile)
	panel.onTaskAssigned = func(string, string) (tea.Cmd, error) {
		return nil, errors.New("injected PTY start failure")
	}
	if _, err := panel.assignTaskToChat(task, ChatInfo{SessionID: newSession}, "progress"); err == nil || !strings.Contains(err.Error(), "injected PTY start failure") {
		t.Fatalf("reassignment error = %v, want PTY start failure", err)
	}
	gotTask, err := os.ReadFile(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotTask) != string(taskBefore) {
		t.Fatalf("Kanban task snapshot was not restored: %q", gotTask)
	}
	for path, want := range map[string][]byte{
		oldStatusPath: oldStatusBefore,
		newStatusPath: newStatusBefore,
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("status snapshot %s was not restored: %q", path, got)
		}
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
	k.onTaskAssigned = func(gotSessionID, gotTitle string) (tea.Cmd, error) {
		if gotSessionID != sessionID || gotTitle != "Build" {
			t.Errorf("callback payload = %q/%q", gotSessionID, gotTitle)
		}
		return func() tea.Msg { return "listen" }, nil
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

func TestAssignTaskToChatRollsBackWhenPTYStartFails(t *testing.T) {
	profile := "Assignment Rollback"
	domain := "assignment-rollback"
	t.Setenv("AI_DATA_HOME", t.TempDir())
	taskDir := kanban.KanbanDir(domain, profile)
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	taskPath := filepath.Join(taskDir, "task.md")
	if err := os.WriteFile(taskPath, []byte("---\ntitle: Build\nstatus: todo\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sessionID := "assignment-rollback__chat"
	statusPath := filepath.Join(paths.SessionDir(profile, sessionID), "status.json")
	if err := os.MkdirAll(filepath.Dir(statusPath), 0o755); err != nil {
		t.Fatal(err)
	}
	originalStatus := []byte(`{"existing":true}`)
	if err := os.WriteFile(statusPath, originalStatus, 0o644); err != nil {
		t.Fatal(err)
	}

	panel := NewKanbanPanel(profile)
	panel.onTaskAssigned = func(string, string) (tea.Cmd, error) {
		return nil, errors.New("injected PTY start failure")
	}
	task, err := kanban.ReadTask(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := panel.assignTaskToChat(task, ChatInfo{SessionID: sessionID}, "progress"); err == nil || !strings.Contains(err.Error(), "injected PTY start failure") {
		t.Fatalf("assignment error = %v, want PTY start failure", err)
	}
	unchanged, err := kanban.ReadTask(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.AssignedTo != "" || unchanged.Status != "todo" {
		t.Fatalf("task was not rolled back: %+v", unchanged)
	}
	statusData, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(statusData) != string(originalStatus) {
		t.Fatalf("status snapshot was not restored: %s", statusData)
	}
}

func TestAssignTaskToChatKeepsCommittedAssignmentAndShowsWarning(t *testing.T) {
	profile := "Assignment Committed"
	domain := "assignment-committed"
	t.Setenv("AI_DATA_HOME", t.TempDir())
	taskDir := kanban.KanbanDir(domain, profile)
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	taskPath := filepath.Join(taskDir, "task.md")
	if err := os.WriteFile(taskPath, []byte("---\ntitle: Build\nstatus: todo\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sessionID := "assignment-committed__chat"
	statusPath := filepath.Join(paths.SessionDir(profile, sessionID), "status.json")
	if err := os.MkdirAll(filepath.Dir(statusPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statusPath, []byte(`{"existing":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	panel := NewKanbanPanel(profile)
	panel.onTaskAssigned = func(string, string) (tea.Cmd, error) {
		return func() tea.Msg { return "listen" }, &CommittedActionError{Err: errors.New("runtime snapshot warning")}
	}
	task, err := kanban.ReadTask(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := panel.assignTaskToChat(task, ChatInfo{SessionID: sessionID}, "progress")
	if err != nil || cmd == nil {
		t.Fatalf("committed assignment = (%v, %v), want command without rollback error", cmd, err)
	}
	if !strings.Contains(panel.actionWarning, "runtime snapshot warning") {
		t.Fatalf("committed warning is not visible in panel state: %q", panel.actionWarning)
	}
	assigned, err := kanban.ReadTask(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	if assigned.AssignedTo != sessionID || assigned.Status != "progress" {
		t.Fatalf("committed assignment was rolled back: %+v", assigned)
	}
	if view := panel.View(120, 8); !strings.Contains(view, "runtime snapshot warning") {
		t.Fatalf("committed warning is not rendered: %q", view)
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
	k.onTaskAssigned = func(string, string) (tea.Cmd, error) {
		called = true
		return nil, nil
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
	k.Activate()

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
	k.Update(kanbanChangedMsg{generation: k.generation, watcher: k.watcher})
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
	defer k.Close()
	k.SetDomain(domain)
	k.Activate()

	if k.watcher == nil {
		t.Fatalf("expected watcher to be attached after Activate")
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
	defer k.Close()
	k.SetDomain(domain)
	k.Activate()

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

	// Simulate the fsnotify event by sending a generation-bound message.
	k.Update(kanbanChangedMsg{generation: k.generation, watcher: k.watcher})

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
	defer k.Close()
	k.SetDomain("domain-a")
	k.Activate()
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
	defer k.Close()
	k.SetDomain(domain)
	k.Activate()
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
			k.Activate()
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
