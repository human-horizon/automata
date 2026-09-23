package kanban

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTaskFile is a tiny test helper that writes a minimal frontmatter
// file to <dir>/<name> with the given title and status.
func writeTaskFile(t *testing.T, dir, name, title, status string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, name)
	body := "---\n" +
		"title: " + title + "\n" +
		"status: " + status + "\n" +
		"---\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

// TestReadTaskParsesSubstatus verifies the readTask parser populates the
// Substatus field from frontmatter.
func TestReadTaskParsesSubstatus(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "task.md")
	body := "---\n" +
		"title: Parses substatus\n" +
		"status: progress\n" +
		"substatus: analyze\n" +
		"---\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	task, err := readTask(path)
	if err != nil {
		t.Fatalf("readTask: %v", err)
	}
	if task.Substatus != "analyze" {
		t.Errorf("Substatus = %q, want %q", task.Substatus, "analyze")
	}
}

func TestUpdateStatusPreservesUnknownFrontmatter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata.md")
	contents := "---\n" +
		"title: Preserve metadata\n" +
		"status: progress\n" +
		"priority: high\n" +
		"labels: [one, two]\n" +
		"owner_note: \"keep: this\"\n" +
		"---\nbody\n"
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateStatus(path, "done"); err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"priority: high", "labels: [one, two]", "owner_note: \"keep: this\""} {
		if !strings.Contains(string(updated), want) {
			t.Fatalf("unknown metadata %q was not preserved:\n%s", want, updated)
		}
	}
}

// TestUpdateStatusClearsSubstatus verifies that moving a task out of
// "progress" wipes its substatus — the spec requires substatus to reflect
// the agent's *current* activity, and a non-progress task has none.
func TestUpdateStatusClearsSubstatus(t *testing.T) {
	dir := t.TempDir()
	path := writeTaskFile(t, dir, "clrc.md", "Clear substatus", "progress")
	if _, err := UpdateSubstatus(path, "read"); err != nil {
		t.Fatalf("UpdateSubstatus: %v", err)
	}
	if _, err := UpdateStatus(path, "todo"); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "substatus:") {
		t.Errorf("expected substatus line to be removed, got:\n%s", data)
	}

	// Also verify via the parser: status=todo, substatus=""
	task, err := readTask(path)
	if err != nil {
		t.Fatalf("readTask: %v", err)
	}
	if task.Status != "todo" {
		t.Errorf("Status = %q, want %q", task.Status, "todo")
	}
	if task.Substatus != "" {
		t.Errorf("Substatus = %q, want empty", task.Substatus)
	}
}

// TestUpdateStatusPreservesSubstatusWhenStayingInProgress ensures that
// UpdateStatus to "progress" does NOT wipe the existing substatus.
func TestUpdateStatusPreservesSubstatusWhenStayingInProgress(t *testing.T) {
	dir := t.TempDir()
	path := writeTaskFile(t, dir, "prsv.md", "Preserve", "progress")
	if _, err := UpdateSubstatus(path, "write"); err != nil {
		t.Fatalf("UpdateSubstatus: %v", err)
	}
	if _, err := UpdateStatus(path, "progress"); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "substatus: write") {
		t.Errorf("expected substatus: write preserved, got:\n%s", data)
	}
}

// TestUpdateStatusClearsSubstatusForAllNonProgressTransitions verifies the
// clear-on-transition rule for every non-progress status, not just todo.
func TestUpdateStatusClearsSubstatusForAllNonProgressTransitions(t *testing.T) {
	for _, newStatus := range []string{"todo", "pending", "done"} {
		dir := t.TempDir()
		path := writeTaskFile(t, dir, "task.md", "Multi", "progress")
		if _, err := UpdateSubstatus(path, "analyze"); err != nil {
			t.Fatalf("seed: %v", err)
		}
		if _, err := UpdateStatus(path, newStatus); err != nil {
			t.Fatalf("UpdateStatus(%s): %v", newStatus, err)
		}
		task, err := readTask(path)
		if err != nil {
			t.Fatalf("readTask: %v", err)
		}
		if task.Substatus != "" {
			t.Errorf("transition to %q: Substatus = %q, want empty", newStatus, task.Substatus)
		}
	}
}

// TestUpdateStatusDoneToProgressForbidden guards the rule "a done task must
// not return to progress". Without this gate, a race condition, scripted
// edit, or external .md rewrite could bypass the (now-removed) "↩ Progress"
// UI button. UpdateStatus must return ErrDoneToProgressForbidden and leave
// the file unchanged.
func TestUpdateStatusDoneToProgressForbidden(t *testing.T) {
	dir := t.TempDir()
	path := writeTaskFile(t, dir, "task.md", "Closed", "done")

	_, err := UpdateStatus(path, "progress")
	if err == nil {
		t.Fatalf("expected ErrDoneToProgressForbidden, got nil")
	}
	if !errors.Is(err, ErrDoneToProgressForbidden) {
		t.Errorf("err = %v, want ErrDoneToProgressForbidden", err)
	}

	// File must still read as done.
	task, err := readTask(path)
	if err != nil {
		t.Fatalf("readTask after failed update: %v", err)
	}
	if task.Status != "done" {
		t.Errorf("status mutated after rejected update: got %q, want done", task.Status)
	}
}

// TestUpdateStatusFromDoneToOtherStatusAllowed verifies that the guard
// blocks ONLY done→progress. Other transitions out of done must remain
// allowed (e.g. todo/pending).
func TestUpdateStatusFromDoneToOtherStatusAllowed(t *testing.T) {
	dir := t.TempDir()
	path := writeTaskFile(t, dir, "task.md", "Reopenable", "done")

	for _, target := range []string{"todo", "pending"} {
		task, err := UpdateStatus(path, target)
		if err != nil {
			t.Errorf("UpdateStatus(%s): unexpected error: %v", target, err)
		}
		if task.Status != target {
			t.Errorf("status = %q, want %q", task.Status, target)
		}
	}
}
