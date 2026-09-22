package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HumanHorizon/automata/internal/paths"
	"github.com/HumanHorizon/automata/internal/tree"
)

func TestMoveChatRejectsKanbanTargetCollisionBeforeMutation(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	profile := "Collision Profile"
	app := newApp(profile, "")
	defer app.Close()

	app.tree.AddFolder("source")
	app.tree.AddFolder("target")
	source := app.tree.Root()[0]
	target := app.tree.Root()[1]
	chat := &tree.Item{Name: "chat", CWD: "/work"}
	source.AddChild(chat)
	oldID := app.tree.SessionKeyOf(chat)
	sourceDomain := source.Domain(profile)
	targetDomain := target.Domain(profile)
	if err := os.MkdirAll(paths.SessionDir(profile, oldID), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTask := func(path, assigned string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		data := fmt.Sprintf("---\ntitle: Task\nstatus: progress\nassigned_to: %s\n---\nbody\n", assigned)
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sourceTask := filepath.Join(paths.DomainDir(profile, sourceDomain), "kanban", "same.md")
	targetTask := filepath.Join(paths.DomainDir(profile, targetDomain), "kanban", "same.md")
	writeTask(sourceTask, oldID)
	writeTask(targetTask, "other-session")

	app.tree.MoveItem(chat, target)

	if got := app.tree.SessionKeyOf(chat); got != oldID {
		t.Fatalf("chat moved despite task collision: got session %q, want %q", got, oldID)
	}
	gotSource, err := os.ReadFile(sourceTask)
	if err != nil || !strings.Contains(string(gotSource), "assigned_to: "+oldID) {
		t.Fatalf("source task changed after rejected move: err=%v data=%q", err, gotSource)
	}
	gotTarget, err := os.ReadFile(targetTask)
	if err != nil || !strings.Contains(string(gotTarget), "assigned_to: other-session") {
		t.Fatalf("target collision task changed: err=%v data=%q", err, gotTarget)
	}
}
