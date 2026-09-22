package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HumanHorizon/automata/internal/paths"
	"github.com/HumanHorizon/automata/internal/tree"
	"github.com/Starframe/portalis"
)

func TestRenameFolderMigratesContextsAndJSONL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	profile := "HumanHorizon"
	agentDir := filepath.Join(home, ".ai", "just", "pi")
	app := &App{
		profile:               profile,
		piAgentDir:            agentDir,
		activeSessions:        make(map[string]struct{}),
		emulatorCache:         make(map[string]*portalis.Emulator),
		familiarEmulatorCache: make(map[string]*portalis.Emulator),
	}
	app.tree = tree.New()
	app.tree.Profile = profile
	app.tree.AddFolder("old-folder")
	folder := app.tree.Root()[0]
	chat := &tree.Item{Name: "chat", CWD: "/work"}
	terminal := &tree.Item{Name: "shell", IsTerminal: true, CWD: "/work"}
	folder.AddChild(chat)
	folder.AddChild(terminal)

	oldChatID := app.tree.SessionKeyOf(chat)
	oldTerminalID := app.tree.SessionKeyOf(terminal)
	oldDomain := folder.Domain(profile)
	chatDir := filepath.Join(app.sessionBaseDir(), oldChatID)
	terminalDir := filepath.Join(app.sessionBaseDir(), oldTerminalID)
	if err := os.MkdirAll(filepath.Join(chatDir, "jobs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(terminalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chatDir, "plans.json"), []byte(`[{"name":"plan","steps":[]}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(terminalDir, "status.json"), []byte(`{"action":"idle"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	domainDir := paths.DomainDir(profile, oldDomain)
	if err := os.MkdirAll(filepath.Join(domainDir, "kanban"), 0o755); err != nil {
		t.Fatal(err)
	}
	notes := []byte(`[{"title":"keep","sections":[{"title":"Context","content":"unchanged"}]}]`)
	if err := os.WriteFile(filepath.Join(domainDir, "notes.json"), notes, 0o644); err != nil {
		t.Fatal(err)
	}
	kanban := filepath.Join(domainDir, "kanban", "task.md")
	kanbanData := fmt.Sprintf("---\ntitle: Task\nstatus: progress\nassigned_to: %s\n---\ntask data\n", oldChatID)
	if err := os.WriteFile(kanban, []byte(kanbanData), 0o644); err != nil {
		t.Fatal(err)
	}

	jsonlDir := filepath.Join(agentDir, "sessions", paths.EncodeCwdDir(chat.CWD))
	if err := os.MkdirAll(jsonlDir, 0o755); err != nil {
		t.Fatal(err)
	}
	jsonlPath := filepath.Join(jsonlDir, "chat.jsonl")
	jsonlRest := "\n{\"type\":\"message\",\"text\":\"history\"}\n"
	jsonl := fmt.Sprintf("{\"type\":\"session\",\"id\":%q}\n%s", oldChatID, jsonlRest[1:])
	if err := os.WriteFile(jsonlPath, []byte(jsonl), 0o600); err != nil {
		t.Fatal(err)
	}

	oldFamiliarID := oldChatID + "__expert"
	familiarPath := paths.FamiliarsJSONLPath(profile, oldChatID)
	if err := os.MkdirAll(filepath.Dir(familiarPath), 0o755); err != nil {
		t.Fatal(err)
	}
	familiarJSON := fmt.Sprintf(`[{"id":"expert","sessionId":%q,"created":"now"}]`, oldFamiliarID)
	if err := os.WriteFile(familiarPath, []byte(familiarJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(app.sessionBaseDir(), oldFamiliarID), 0o755); err != nil {
		t.Fatal(err)
	}
	familiarJSONLDir := filepath.Join(agentDir, "sessions", paths.EncodeCwdDir(chat.CWD))
	familiarJSONL := filepath.Join(familiarJSONLDir, "familiar.jsonl")
	familiarHeader := fmt.Sprintf("{\"type\":\"session\",\"id\":%q}\n", oldFamiliarID)
	if err := os.WriteFile(familiarJSONL, []byte(familiarHeader), 0o600); err != nil {
		t.Fatal(err)
	}
	app.emulatorCache[oldChatID] = portalis.NewEmulator(oldChatID, chat.Name, "", nil)
	app.emulatorCache[oldTerminalID] = portalis.NewEmulator(oldTerminalID, terminal.Name, "", nil)
	app.familiarEmulatorCache[oldFamiliarID] = portalis.NewEmulator(oldFamiliarID, "expert", "", nil)
	app.activeSessions[oldChatID] = struct{}{}
	app.activeSessions[oldTerminalID] = struct{}{}
	app.activeSessions[oldFamiliarID] = struct{}{}

	app.tree.SetOnRename(app.renameTreeItem)
	if err := app.tree.RenameItem(folder, "new-folder"); err != nil {
		t.Fatalf("renameTreeItem: %v", err)
	}

	newChatID := app.tree.SessionKeyOf(chat)
	newTerminalID := app.tree.SessionKeyOf(terminal)
	newDomain := folder.Domain(profile)
	newFamiliarID := newChatID + "__expert"
	if folder.Name != "new-folder" {
		t.Fatalf("folder name = %q", folder.Name)
	}
	if newChatID == oldChatID || newTerminalID == oldTerminalID || newDomain == oldDomain {
		t.Fatal("rename did not change derived identifiers")
	}
	if _, err := os.Stat(filepath.Join(app.sessionBaseDir(), oldChatID)); !os.IsNotExist(err) {
		t.Fatalf("old chat session still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(app.sessionBaseDir(), newChatID, "plans.json")); err != nil {
		t.Fatalf("new chat context missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(app.sessionBaseDir(), newTerminalID, "status.json")); err != nil {
		t.Fatalf("new terminal context missing: %v", err)
	}
	newNotesPath := filepath.Join(paths.DomainDir(profile, newDomain), "notes.json")
	gotNotes, err := os.ReadFile(newNotesPath)
	if err != nil {
		t.Fatalf("new notes missing: %v", err)
	}
	if string(gotNotes) != string(notes) {
		t.Fatalf("notes changed:\nwant %s\ngot  %s", notes, gotNotes)
	}
	gotKanban, err := os.ReadFile(filepath.Join(paths.DomainDir(profile, newDomain), "kanban", "task.md"))
	if err != nil || !strings.Contains(string(gotKanban), "assigned_to: "+newChatID) || !strings.Contains(string(gotKanban), "task data") {
		t.Fatalf("kanban was not preserved or reassigned: err=%v data=%q", err, gotKanban)
	}

	gotJSONL, err := os.ReadFile(jsonlPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gotJSONL), `"id":"`+newChatID+`"`) || !strings.Contains(string(gotJSONL), `"text":"history"`) {
		t.Fatalf("chat JSONL was not migrated: %s", gotJSONL)
	}
	newFamiliarPath := paths.FamiliarsJSONLPath(profile, newChatID)
	gotFamiliars, err := os.ReadFile(newFamiliarPath)
	if err != nil {
		t.Fatalf("new familiars file missing: %v", err)
	}
	if !strings.Contains(string(gotFamiliars), newFamiliarID) {
		t.Fatalf("familiar ID was not migrated: %s", gotFamiliars)
	}
	gotFamiliarJSONL, err := os.ReadFile(familiarJSONL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gotFamiliarJSONL), newFamiliarID) {
		t.Fatalf("familiar JSONL was not migrated: %s", gotFamiliarJSONL)
	}
	if _, err := os.Stat(filepath.Join(app.sessionBaseDir(), newFamiliarID)); err != nil {
		t.Fatalf("familiar session directory missing: %v", err)
	}
	if len(app.activeSessions) != 0 || len(app.emulatorCache) != 0 || len(app.familiarEmulatorCache) != 0 {
		t.Fatalf("working sessions were not stopped: active=%v cache=%v familiar=%v", app.activeSessions, app.emulatorCache, app.familiarEmulatorCache)
	}
}

func TestRenameRejectsConflictWithoutMutation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	app := &App{
		profile:        "test",
		piAgentDir:     filepath.Join(home, ".ai", "just", "pi"),
		activeSessions: make(map[string]struct{}),
		emulatorCache:  make(map[string]*portalis.Emulator),
		tree:           tree.New(),
	}
	app.tree.Profile = app.profile
	app.tree.AddChat("Alpha")
	app.tree.AddChat("Beta")
	beta := app.tree.Root()[1]
	app.tree.SetOnRename(app.renameTreeItem)

	if err := app.tree.RenameItem(beta, " alpha "); err == nil {
		t.Fatal("expected rename conflict")
	}
	if beta.Name != "Beta" {
		t.Fatalf("conflict changed name to %q", beta.Name)
	}
}

func TestRenameRejectsExistingSessionTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	app := &App{
		profile:        "test",
		piAgentDir:     filepath.Join(home, ".ai", "just", "pi"),
		activeSessions: make(map[string]struct{}),
		emulatorCache:  make(map[string]*portalis.Emulator),
		tree:           tree.New(),
	}
	app.tree.Profile = app.profile
	app.tree.AddChat("old")
	item := app.tree.Root()[0]
	targetID := app.profile + "__target"
	if err := os.MkdirAll(filepath.Join(app.sessionBaseDir(), targetID), 0o755); err != nil {
		t.Fatal(err)
	}
	app.tree.SetOnRename(app.renameTreeItem)

	if err := app.tree.RenameItem(item, "target"); err == nil {
		t.Fatal("expected existing session target error")
	}
	if item.Name != "old" {
		t.Fatalf("target conflict changed name to %q", item.Name)
	}
	if _, err := os.Stat(filepath.Join(app.sessionBaseDir(), targetID)); err != nil {
		t.Fatalf("target data was changed after conflict: %v", err)
	}
}

func TestRenameStopsOnlySelectedSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	app := &App{
		profile:        "test",
		piAgentDir:     filepath.Join(home, ".ai", "just", "pi"),
		activeSessions: make(map[string]struct{}),
		emulatorCache:  make(map[string]*portalis.Emulator),
		tree:           tree.New(),
	}
	app.tree.Profile = app.profile
	app.tree.AddChat("first")
	app.tree.AddChat("second")
	first := app.tree.Root()[0]
	second := app.tree.Root()[1]
	firstID := app.tree.SessionKeyOf(first)
	secondID := app.tree.SessionKeyOf(second)
	app.emulatorCache[firstID] = portalis.NewEmulator(firstID, first.Name, "", nil)
	app.emulatorCache[secondID] = portalis.NewEmulator(secondID, second.Name, "", nil)
	app.tree.SetOnRename(app.renameTreeItem)

	if err := app.tree.RenameItem(first, "renamed"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, ok := app.emulatorCache[firstID]; ok {
		t.Fatal("renamed session emulator was not stopped")
	}
	if _, ok := app.emulatorCache[secondID]; !ok {
		t.Fatal("sibling emulator was stopped")
	}
}

func TestMoveChatAndTerminalReusesRenameMigration(t *testing.T) {
	home := t.TempDir()
	dataHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AI_DATA_HOME", dataHome)
	profile := "Move Profile"
	app := newApp(profile, "")
	defer app.Close()

	app.tree.AddFolder("source")
	app.tree.AddFolder("target")
	source := app.tree.Root()[0]
	target := app.tree.Root()[1]
	chat := &tree.Item{Name: "chat", CWD: "/work"}
	terminal := &tree.Item{Name: "shell", IsTerminal: true, CWD: "/work"}
	source.AddChild(chat)
	source.AddChild(terminal)

	oldChatID := app.tree.SessionKeyOf(chat)
	oldTerminalID := app.tree.SessionKeyOf(terminal)
	newChatID := fullRenameSessionID(profile, []string{"target"}, "chat")
	newTerminalID := fullRenameSessionID(profile, []string{"target"}, "shell")
	oldChatDomain := sessionDomainID(oldChatID)
	newChatDomain := sessionDomainID(newChatID)

	for _, id := range []string{oldChatID, oldTerminalID} {
		if err := os.MkdirAll(paths.SessionDir(profile, id), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(paths.DomainDir(profile, oldChatDomain), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.DomainDir(profile, oldChatDomain), "notes.json"), []byte("move notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	kanbanDir := filepath.Join(paths.DomainDir(profile, oldChatDomain), "kanban")
	if err := os.MkdirAll(kanbanDir, 0o755); err != nil {
		t.Fatal(err)
	}
	kanbanPath := filepath.Join(kanbanDir, "move.md")
	if err := os.WriteFile(kanbanPath, []byte(fmt.Sprintf("---\ntitle: Move\nstatus: progress\nassigned_to: %s\n---\nmove task\n", oldChatID)), 0o644); err != nil {
		t.Fatal(err)
	}
	oldFamiliarID := oldChatID + "__expert"
	newFamiliarID := newChatID + "__expert"
	familiarPath := paths.FamiliarsJSONLPath(profile, oldChatID)
	if err := os.MkdirAll(filepath.Dir(familiarPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(familiarPath, []byte(fmt.Sprintf(`[{"id":"expert","sessionId":%q}]`, oldFamiliarID)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.SessionDir(profile, oldFamiliarID), 0o755); err != nil {
		t.Fatal(err)
	}
	jsonlDir := filepath.Join(app.renameAgentDir(), "sessions", paths.EncodeCwdDir(chat.CWD))
	if err := os.MkdirAll(jsonlDir, 0o755); err != nil {
		t.Fatal(err)
	}
	jsonlPath := filepath.Join(jsonlDir, "chat.jsonl")
	if err := os.WriteFile(jsonlPath, []byte(fmt.Sprintf("{\"type\":\"session\",\"id\":%q}\n", oldChatID)), 0o600); err != nil {
		t.Fatal(err)
	}
	familiarJSONLPath := filepath.Join(jsonlDir, "familiar.jsonl")
	if err := os.WriteFile(familiarJSONLPath, []byte(fmt.Sprintf("{\"type\":\"session\",\"id\":%q}\n", oldFamiliarID)), 0o600); err != nil {
		t.Fatal(err)
	}

	app.tree.MoveItem(chat, target)

	if got := app.tree.SessionKeyOf(chat); got != newChatID {
		t.Fatalf("chat session ID = %q, want %q", got, newChatID)
	}
	if _, err := os.Stat(paths.SessionDir(profile, oldChatID)); !os.IsNotExist(err) {
		t.Fatalf("old chat session remains: %v", err)
	}
	if _, err := os.Stat(paths.SessionDir(profile, newChatID)); err != nil {
		t.Fatalf("new chat session missing: %v", err)
	}
	if _, err := os.Stat(paths.SessionDir(profile, oldTerminalID)); err != nil {
		t.Fatalf("sibling terminal session was changed: %v", err)
	}
	if _, err := os.Stat(paths.SessionDir(profile, newTerminalID)); err == nil {
		t.Fatalf("sibling terminal moved unexpectedly")
	}
	gotNotes, err := os.ReadFile(filepath.Join(paths.DomainDir(profile, newChatDomain), "notes.json"))
	if err != nil || string(gotNotes) != "move notes" {
		t.Fatalf("moved chat domain missing: err=%v notes=%q", err, gotNotes)
	}
	if _, err := os.Stat(paths.DomainDir(profile, oldChatDomain)); !os.IsNotExist(err) {
		t.Fatalf("old chat domain remains: %v", err)
	}
	gotJSONL, err := os.ReadFile(jsonlPath)
	if err != nil || !strings.Contains(string(gotJSONL), newChatID) {
		t.Fatalf("session JSONL was not migrated: err=%v data=%q", err, gotJSONL)
	}
	gotKanban, err := os.ReadFile(filepath.Join(paths.DomainDir(profile, newChatDomain), "kanban", "move.md"))
	if err != nil || !strings.Contains(string(gotKanban), "assigned_to: "+newChatID) {
		t.Fatalf("Kanban assignment was not migrated: err=%v data=%q", err, gotKanban)
	}
	newFamiliarPath := paths.FamiliarsJSONLPath(profile, newChatID)
	gotFamiliars, err := os.ReadFile(newFamiliarPath)
	if err != nil || !strings.Contains(string(gotFamiliars), newFamiliarID) {
		t.Fatalf("familiars mapping was not migrated: err=%v data=%q", err, gotFamiliars)
	}
	if _, err := os.Stat(paths.SessionDir(profile, oldFamiliarID)); !os.IsNotExist(err) {
		t.Fatalf("old familiar session remains: %v", err)
	}
	if _, err := os.Stat(paths.SessionDir(profile, newFamiliarID)); err != nil {
		t.Fatalf("new familiar session missing: %v", err)
	}
	gotFamiliarJSONL, err := os.ReadFile(familiarJSONLPath)
	if err != nil || !strings.Contains(string(gotFamiliarJSONL), newFamiliarID) {
		t.Fatalf("familiar JSONL was not migrated: err=%v data=%q", err, gotFamiliarJSONL)
	}
}

func TestRenameRootChatMovesItsDomain(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	profile := "test"
	app := &App{
		profile:        profile,
		piAgentDir:     filepath.Join(home, ".ai", "just", "pi"),
		activeSessions: make(map[string]struct{}),
		emulatorCache:  make(map[string]*portalis.Emulator),
		tree:           tree.New(),
	}
	app.tree.Profile = profile
	app.tree.AddChat("old")
	chat := app.tree.Root()[0]
	oldID := app.tree.SessionKeyOf(chat)
	oldDomain := sessionDomainID(oldID)
	domainDir := paths.DomainDir(profile, oldDomain)
	if err := os.MkdirAll(domainDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(domainDir, "notes.json"), []byte("root notes"), 0o644); err != nil {
		t.Fatal(err)
	}

	app.tree.SetOnRename(app.renameTreeItem)
	if err := app.tree.RenameItem(chat, "new"); err != nil {
		t.Fatalf("rename root chat: %v", err)
	}
	newID := app.tree.SessionKeyOf(chat)
	newDomain := sessionDomainID(newID)
	if _, err := os.Stat(filepath.Join(paths.DomainDir(profile, oldDomain), "notes.json")); !os.IsNotExist(err) {
		t.Fatalf("old root domain still exists: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(paths.DomainDir(profile, newDomain), "notes.json"))
	if err != nil {
		t.Fatalf("new root domain missing: %v", err)
	}
	if string(got) != "root notes" {
		t.Fatalf("root notes changed: %q", got)
	}
}
