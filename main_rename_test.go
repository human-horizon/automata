package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	akactions "github.com/HumanHorizon/automata/internal/ai-knowledge/actions"
	"github.com/HumanHorizon/automata/internal/atomicfile"
	"github.com/HumanHorizon/automata/internal/kanban"
	"github.com/HumanHorizon/automata/internal/paths"
	"github.com/HumanHorizon/automata/internal/tree"
	"github.com/Starframe/portalis"
)

func newRenameWarningTestApp(t *testing.T, profile string) (*App, *tree.Tree) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AI_DATA_HOME", t.TempDir())
	tr := tree.New()
	tr.Profile = profile
	app := &App{
		tree:                  tr,
		profile:               profile,
		piAgentDir:            filepath.Join(home, ".ai", "just", "pi"),
		activeSessions:        make(map[string]struct{}),
		runningSessions:       make(map[string]struct{}),
		emulatorCache:         make(map[string]*portalis.Emulator),
		familiarEmulatorCache: make(map[string]*portalis.Emulator),
		killSessionFn: func(_, _ string) error {
			return errors.New("injected post-commit job cleanup failure")
		},
	}
	return app, tr
}

func bindTransactionalRenameForTest(app *App, tr *tree.Tree) {
	app.pendingRenamePlans = make(map[*tree.Item]*renamePlan)
	tr.SetOnBeforeRename(func(item *tree.Item, newName string) (func() error, error) {
		plan, err := buildRenamePlan(item, newName, app.profile)
		if err != nil {
			return nil, err
		}
		rollback, err := app.applyRenamePlan(plan)
		if err != nil {
			return nil, err
		}
		app.pendingRenamePlans[item] = plan
		return func() error {
			delete(app.pendingRenamePlans, item)
			return rollback()
		}, nil
	})
	tr.SetOnRenameCommitted(func(item *tree.Item, _, _ string) {
		plan := app.pendingRenamePlans[item]
		delete(app.pendingRenamePlans, item)
		if plan == nil {
			return
		}
		app.applyRenameMappings(plan)
		_ = app.finalizeRenamePlan(plan)
	})
}

func TestRenameCleanupWarningsRemainVisibleWithoutRollback(t *testing.T) {
	t.Run("committed rename callback", func(t *testing.T) {
		app, tr := newRenameWarningTestApp(t, "callback-warning")
		tr.AddChat("old")
		item := tr.Root()[0]
		bindTransactionalRenameForTest(app, tr)

		if err := tr.RenameItem(item, "new"); err != nil {
			t.Fatalf("RenameItem: %v", err)
		}
		if item.Name != "new" {
			t.Fatalf("committed rename was rolled back: %q", item.Name)
		}
		if warning := tr.LastActionError(); warning == nil || !strings.Contains(warning.Error(), "runtime cleanup is incomplete") {
			t.Fatalf("post-commit cleanup warning = %v", warning)
		}
	})

	t.Run("committed move callback", func(t *testing.T) {
		app, tr := newRenameWarningTestApp(t, "move-warning")
		tr.AddFolder("source")
		tr.AddFolder("target")
		source, target := tr.Root()[0], tr.Root()[1]
		item := &tree.Item{Name: "chat"}
		source.AddChild(item)
		app.pendingMovePlans = make(map[*tree.Item]*renamePlan)
		tr.SetOnBeforeItemMoved(func(item, newParent *tree.Item) (func() error, error) {
			plan, err := buildMovePlan(item, newParent, app.profile)
			if err != nil {
				return nil, err
			}
			rollback, err := app.applyRenamePlan(plan)
			if err != nil {
				return nil, err
			}
			app.pendingMovePlans[item] = plan
			return func() error {
				delete(app.pendingMovePlans, item)
				return rollback()
			}, nil
		})
		tr.SetOnItemMoved(func(item *tree.Item, _, _ string) {
			plan := app.pendingMovePlans[item]
			delete(app.pendingMovePlans, item)
			app.applyRenameMappings(plan)
			if err := app.finalizeRenamePlan(plan); err == nil {
				t.Error("expected injected post-commit cleanup warning")
			}
		})

		if err := tr.MoveItemChecked(item, target); err != nil {
			t.Fatalf("MoveItemChecked: %v", err)
		}
		if got, want := tr.SessionKeyOf(item), "move-warning__target.chat"; got != want {
			t.Fatalf("committed move session = %q, want %q", got, want)
		}
		if warning := tr.LastActionError(); warning == nil || !strings.Contains(warning.Error(), "runtime cleanup is incomplete") {
			t.Fatalf("post-commit cleanup warning = %v", warning)
		}
	})
}

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
	actionsDir, err := akactions.Directory(profile, oldDomain)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(actionsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	actionID := strings.Repeat("a", 64)
	actionJSON := fmt.Sprintf(`{"id":%q,"name":"Build","command":"make test","cwd":"/work"}`, actionID)
	if err := os.WriteFile(filepath.Join(actionsDir, actionID+".json"), []byte(actionJSON), 0o600); err != nil {
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

	bindTransactionalRenameForTest(app, app.tree)
	if err := app.tree.RenameItem(folder, "new-folder"); err != nil {
		t.Fatalf("RenameItem: %v", err)
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
	gotActions, err := akactions.ReadForProfile(profile, newDomain)
	if err != nil || len(gotActions) != 1 || gotActions[0].Name != "Build" {
		t.Fatalf("folder actions were not migrated with the domain: err=%v actions=%#v", err, gotActions)
	}
	if _, err := os.Stat(filepath.Join(paths.DomainDir(profile, oldDomain), "actions", actionID+".json")); !os.IsNotExist(err) {
		t.Fatalf("old domain still contains moved action: %v", err)
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

func TestMoveFolderMigratesActions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	profile := "HumanHorizon"
	tr := tree.New()
	tr.Profile = profile
	tr.AddFolder("source")
	tr.AddFolder("target")
	source, target := tr.Root()[0], tr.Root()[1]
	oldDomain := source.Domain(profile)
	actionsDir, err := akactions.Directory(profile, oldDomain)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(actionsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	actionID := strings.Repeat("c", 64)
	actionJSON := fmt.Sprintf(`{"id":%q,"name":"Build","command":"make test","cwd":"/work"}`, actionID)
	if err := os.WriteFile(filepath.Join(actionsDir, actionID+".json"), []byte(actionJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	app := &App{
		tree:                  tr,
		profile:               profile,
		activeSessions:        make(map[string]struct{}),
		runningSessions:       make(map[string]struct{}),
		emulatorCache:         make(map[string]*portalis.Emulator),
		familiarEmulatorCache: make(map[string]*portalis.Emulator),
		pendingMovePlans:      make(map[*tree.Item]*renamePlan),
	}
	tr.SetOnBeforeItemMoved(func(item, newParent *tree.Item) (func() error, error) {
		plan, err := buildMovePlan(item, newParent, profile)
		if err != nil {
			return nil, err
		}
		rollback, err := app.applyRenamePlan(plan)
		if err != nil {
			return nil, err
		}
		app.pendingMovePlans[item] = plan
		return func() error {
			delete(app.pendingMovePlans, item)
			return rollback()
		}, nil
	})
	tr.SetOnItemMoved(func(item *tree.Item, _, _ string) {
		plan := app.pendingMovePlans[item]
		delete(app.pendingMovePlans, item)
		app.applyRenameMappings(plan)
		if err := app.finalizeRenamePlan(plan); err != nil {
			t.Errorf("finalize folder move: %v", err)
		}
	})

	if err := tr.MoveItemChecked(source, target); err != nil {
		t.Fatalf("MoveItemChecked: %v", err)
	}
	newDomain := source.Domain(profile)
	if newDomain == oldDomain {
		t.Fatal("moving folder did not change its domain")
	}
	got, err := akactions.ReadForProfile(profile, newDomain)
	if err != nil || len(got) != 1 || got[0].Name != "Build" {
		t.Fatalf("folder action was not migrated with the moved folder: err=%v actions=%#v", err, got)
	}
	if _, err := os.Stat(filepath.Join(paths.DomainDir(profile, oldDomain), "actions", actionID+".json")); !os.IsNotExist(err) {
		t.Fatalf("old domain still contains moved action: %v", err)
	}
}

func TestMoveRollbackRestoresRuntimeAndFilesystem(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	profile := "Rollback Profile"
	tr := tree.New()
	tr.Profile = profile
	tr.AddFolder("source")
	tr.AddFolder("target")
	source := tr.Root()[0]
	target := tr.Root()[1]
	chat := &tree.Item{Name: "chat", CWD: "/work"}
	source.AddChild(chat)
	oldID := tr.SessionKeyOf(chat)
	newID := fullRenameSessionID(profile, []string{"target"}, "chat")
	if err := os.MkdirAll(paths.SessionDir(profile, oldID), 0o755); err != nil {
		t.Fatal(err)
	}

	em := portalis.NewEmulator(oldID, "chat", "/bin/sh", nil)
	app := &App{
		tree:                  tr,
		profile:               profile,
		piAgentDir:            filepath.Join(t.TempDir(), "pi"),
		activeSessions:        map[string]struct{}{oldID: {}},
		emulatorCache:         map[string]*portalis.Emulator{oldID: em},
		familiarEmulatorCache: make(map[string]*portalis.Emulator),
		runningSessions:       map[string]struct{}{oldID: {}},
		currentSessionID:      "unrelated-session",
	}
	startCalls := 0
	app.startEmulatorSyncFn = func(*portalis.Emulator, []string) error {
		startCalls++
		return nil
	}
	plan, err := buildMovePlan(chat, target, profile)
	if err != nil {
		t.Fatal(err)
	}
	rollback, err := app.applyRenamePlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.SessionDir(profile, newID)); err != nil {
		t.Fatalf("new session missing before rollback: %v", err)
	}
	if _, ok := app.emulatorCache[oldID]; ok {
		t.Fatal("runtime cache was not stopped before move commit")
	}
	if err := rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.SessionDir(profile, oldID)); err != nil {
		t.Fatalf("old session missing after rollback: %v", err)
	}
	if _, err := os.Stat(paths.SessionDir(profile, newID)); !os.IsNotExist(err) {
		t.Fatalf("new session remains after rollback: %v", err)
	}
	if app.emulatorCache[oldID] != em {
		t.Fatal("runtime emulator cache was not restored")
	}
	if startCalls != 1 {
		t.Fatalf("running emulator restart calls = %d, want 1", startCalls)
	}
	if _, ok := app.runningSessions[oldID]; !ok {
		t.Fatal("running session was not restored")
	}
	if _, ok := app.activeSessions[oldID]; !ok {
		t.Fatal("active session was not restored")
	}
	if app.currentSessionID != "unrelated-session" {
		t.Fatalf("unrelated current session changed to %q", app.currentSessionID)
	}
	if got := tr.ActiveSessionIDs(); len(got) != 1 || got[0] != oldID {
		t.Fatalf("tree active sessions after rollback = %v", got)
	}
}

func TestRenameSaveStateFailureRollsBackExternalData(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AI_DATA_HOME", t.TempDir())
	profile := "Rename Transaction"
	agentDir := filepath.Join(home, ".ai", "just", "pi")
	tr := tree.New()
	tr.Profile = profile
	tr.AddChat("old")
	chat := tr.Root()[0]
	oldID := fullRenameSessionID(profile, nil, "old")
	newID := fullRenameSessionID(profile, nil, "new")
	if err := os.MkdirAll(paths.SessionDir(profile, oldID), 0o755); err != nil {
		t.Fatal(err)
	}
	jsonlDir := filepath.Join(agentDir, "sessions", paths.EncodeCwdDir(chat.CWD))
	if err := os.MkdirAll(jsonlDir, 0o755); err != nil {
		t.Fatal(err)
	}
	jsonlPath := filepath.Join(jsonlDir, "chat.jsonl")
	if err := os.WriteFile(jsonlPath, []byte(fmt.Sprintf("{\"type\":\"session\",\"id\":%q}\n", oldID)), 0o600); err != nil {
		t.Fatal(err)
	}

	app := &App{
		tree:                  tr,
		profile:               profile,
		piAgentDir:            agentDir,
		activeSessions:        make(map[string]struct{}),
		runningSessions:       make(map[string]struct{}),
		emulatorCache:         make(map[string]*portalis.Emulator),
		familiarEmulatorCache: make(map[string]*portalis.Emulator),
	}
	committed := false
	app.killSessionFn = func(_, sessionID string) error {
		t.Fatalf("job finalization ran before SaveState commit for %s", sessionID)
		return nil
	}
	tr.SetOnBeforeRename(func(item *tree.Item, newName string) (func() error, error) {
		plan, err := buildRenamePlan(item, newName, profile)
		if err != nil {
			return nil, err
		}
		return app.applyRenamePlan(plan)
	})
	tr.SetOnRenameCommitted(func(item *tree.Item, _, _ string) {
		committed = true
	})
	saveCalls := 0
	saveErr := errors.New("injected SaveState failure")
	tr.SetSaveStateFunc(func() error {
		saveCalls++
		if saveCalls == 1 {
			return saveErr
		}
		return nil
	})

	if err := tr.RenameItem(chat, "new"); !errors.Is(err, saveErr) {
		t.Fatalf("rename error = %v, want SaveState error", err)
	}
	if committed {
		t.Fatal("rename committed callback ran after failed SaveState")
	}
	if chat.Name != "old" || saveCalls != 1 {
		t.Fatalf("tree rollback = name %q, save calls %d; want old, 1", chat.Name, saveCalls)
	}
	if _, err := os.Stat(paths.SessionDir(profile, oldID)); err != nil {
		t.Fatalf("old session missing after rollback: %v", err)
	}
	if _, err := os.Stat(paths.SessionDir(profile, newID)); !os.IsNotExist(err) {
		t.Fatalf("new session remains after rollback: %v", err)
	}
	gotJSONL, err := os.ReadFile(jsonlPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gotJSONL), oldID) {
		t.Fatalf("JSONL header was not rolled back: %s", gotJSONL)
	}
	state, err := os.ReadFile(paths.StatePath(profile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(state), `"name": "old"`) {
		t.Fatalf("persisted tree was not old after rollback: %s", state)
	}
}

func TestMoveSaveStateFailureRestoresTreeBeforeRuntimeRollback(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	profile := "Move Transaction"
	tr := tree.New()
	tr.Profile = profile
	tr.AddFolder("source")
	tr.AddFolder("target")
	source := tr.Root()[0]
	target := tr.Root()[1]
	chat := &tree.Item{Name: "chat", CWD: "/work"}
	source.AddChild(chat)
	oldID := fullRenameSessionID(profile, []string{"source"}, "chat")
	newID := fullRenameSessionID(profile, []string{"target"}, "chat")
	if err := os.MkdirAll(paths.SessionDir(profile, oldID), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := tr.SetActiveSessions(map[string]struct{}{oldID: {}}); err != nil {
		t.Fatal(err)
	}

	app := &App{
		tree:                  tr,
		profile:               profile,
		piAgentDir:            filepath.Join(t.TempDir(), "pi"),
		activeSessions:        map[string]struct{}{oldID: {}},
		runningSessions:       map[string]struct{}{oldID: {}},
		emulatorCache:         map[string]*portalis.Emulator{oldID: portalis.NewEmulator(oldID, "chat", "/bin/sh", nil)},
		familiarEmulatorCache: make(map[string]*portalis.Emulator),
		startEmulatorSyncFn: func(*portalis.Emulator, []string) error {
			return nil
		},
	}
	tr.SetOnBeforeItemMoved(func(item, newParent *tree.Item) (func() error, error) {
		plan, err := buildMovePlan(item, newParent, profile)
		if err != nil {
			return nil, err
		}
		return app.applyRenamePlan(plan)
	})
	savedIDs := make([]string, 0, 2)
	saveCalls := 0
	tr.SetSaveStateFunc(func() error {
		saveCalls++
		savedIDs = append(savedIDs, tr.SessionKeyOf(chat))
		if saveCalls == 1 {
			return errors.New("injected move SaveState failure")
		}
		return nil
	})

	tr.MoveItem(chat, target)
	if saveCalls != 1 {
		t.Fatalf("SaveState calls = %d, want 1 (rollback does not rewrite the persisted snapshot)", saveCalls)
	}
	if len(savedIDs) != 1 || savedIDs[0] != newID {
		t.Fatalf("SaveState tree IDs = %v, want [%s]", savedIDs, newID)
	}
	if got := tr.SessionKeyOf(chat); got != oldID {
		t.Fatalf("chat session after rollback = %q, want %q", got, oldID)
	}
	if _, err := os.Stat(paths.SessionDir(profile, oldID)); err != nil {
		t.Fatalf("old session missing after move rollback: %v", err)
	}
	if _, err := os.Stat(paths.SessionDir(profile, newID)); !os.IsNotExist(err) {
		t.Fatalf("new session remains after move rollback: %v", err)
	}
	if _, ok := app.activeSessions[oldID]; !ok {
		t.Fatal("active session was not restored")
	}
	state, err := os.ReadFile(paths.StatePath(profile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(state), "source") || strings.Contains(string(state), "target\\\"\\n") {
		t.Fatalf("persisted tree was not kept old after move rollback: %s", state)
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
	bindTransactionalRenameForTest(app, app.tree)

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
	bindTransactionalRenameForTest(app, app.tree)

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
	bindTransactionalRenameForTest(app, app.tree)

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

func TestMoveRenameTaskRollsBackCommittedAssignmentWarning(t *testing.T) {
	base := t.TempDir()
	oldPath := filepath.Join(base, "source", "task.md")
	newPath := filepath.Join(base, "target", "task.md")
	if err := os.MkdirAll(filepath.Dir(oldPath), 0o755); err != nil {
		t.Fatal(err)
	}
	contents := []byte("---\ntitle: Build\nstatus: progress\nassigned_to: profile__old\n---\nbody\n")
	if err := os.WriteFile(oldPath, contents, 0o644); err != nil {
		t.Fatal(err)
	}

	realAssign := kanban.AssignTask
	previousAssign := assignRenameTask
	calls := 0
	assignRenameTask = func(path, sessionID string) (kanban.Task, error) {
		calls++
		task, err := realAssign(path, sessionID)
		if err != nil {
			return task, err
		}
		if calls == 1 {
			return task, &atomicfile.CommittedError{Path: path, Err: errors.New("directory sync warning")}
		}
		return task, nil
	}
	t.Cleanup(func() { assignRenameTask = previousAssign })

	move := renameTaskMove{
		oldPath:     oldPath,
		newPath:     newPath,
		oldAssigned: "profile__old",
		newAssigned: "profile__new",
	}
	if err := moveRenameTask(&move); err == nil || !atomicfile.IsCommitted(err) {
		t.Fatalf("moveRenameTask error = %v, want committed warning", err)
	}
	if !move.assignmentUpdated {
		t.Fatal("committed assignment was not recorded for rollback")
	}

	if err := rollbackRenameTask(&move); err != nil {
		t.Fatalf("rollbackRenameTask: %v", err)
	}
	if _, err := os.Stat(newPath); !os.IsNotExist(err) {
		t.Fatalf("target task remains after rollback: %v", err)
	}
	rolledBack, err := kanban.ReadTask(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.AssignedTo != "profile__old" {
		t.Fatalf("rolled-back assignee = %q, want profile__old", rolledBack.AssignedTo)
	}
}

func TestRollbackRenameJoinsFailuresAndContinues(t *testing.T) {
	filesystemFailure := errors.New("injected filesystem rollback failure")
	jsonlFailure := errors.New("injected JSONL rollback failure")
	kanbanFailure := errors.New("injected Kanban rollback failure")
	registryFailure := errors.New("injected familiar registry rollback failure")

	previousRenameDirectory := rollbackRenameDirectory
	previousRenameFile := rollbackRenameFile
	previousMigrateJSONL := rollbackMigrateSessionJSONL
	previousRewriteFamiliars := rollbackRewriteFamiliarSessionIDs
	previousAssignTask := assignRenameTask
	t.Cleanup(func() {
		rollbackRenameDirectory = previousRenameDirectory
		rollbackRenameFile = previousRenameFile
		rollbackMigrateSessionJSONL = previousMigrateJSONL
		rollbackRewriteFamiliarSessionIDs = previousRewriteFamiliars
		assignRenameTask = previousAssignTask
	})

	directoryCalls := 0
	fileCalls := 0
	jsonlCalls := 0
	familiarCalls := 0
	kanbanCalls := 0
	rollbackRenameDirectory = func(string, string) error {
		directoryCalls++
		return filesystemFailure
	}
	rollbackRenameFile = func(string, string) error {
		fileCalls++
		return filesystemFailure
	}
	rollbackMigrateSessionJSONL = func(string, string, string, string) (string, error) {
		jsonlCalls++
		return "", jsonlFailure
	}
	rollbackRewriteFamiliarSessionIDs = func(string, string, string) (map[string]string, error) {
		familiarCalls++
		return nil, registryFailure
	}
	assignRenameTask = func(string, string) (kanban.Task, error) {
		kanbanCalls++
		return kanban.Task{}, kanbanFailure
	}

	joined := rollbackRename(
		&App{profile: "profile", piAgentDir: "pi-agent"},
		[]renameDirectoryMove{{oldPath: "old-dir", newPath: "new-dir"}},
		[]renameJSONLMove{{oldID: "new-id", newID: "old-id"}},
		[]renameFamiliarFileMove{{oldOwnerID: "old-owner", newOwnerID: "new-owner"}},
		[]renameAssignmentMove{{path: "task.md", old: "old-id"}},
		[]renameTaskMove{{oldPath: "old-task.md", newPath: "new-task.md", oldAssigned: "old-id", moved: true, assignmentUpdated: true}},
	)

	for name, failure := range map[string]error{
		"filesystem": filesystemFailure,
		"JSONL":      jsonlFailure,
		"Kanban":     kanbanFailure,
		"registry":   registryFailure,
	} {
		if !errors.Is(joined, failure) {
			t.Errorf("rollback error does not preserve %s failure: %v", name, joined)
		}
	}
	if directoryCalls != 1 || fileCalls != 1 || jsonlCalls != 1 || familiarCalls != 1 || kanbanCalls != 2 {
		t.Fatalf("rollback calls: directory=%d file=%d jsonl=%d familiars=%d Kanban=%d", directoryCalls, fileCalls, jsonlCalls, familiarCalls, kanbanCalls)
	}
}

func TestMoveChatReusesRenameMigrationWithoutMovingDomain(t *testing.T) {
	home := t.TempDir()
	dataHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AI_DATA_HOME", dataHome)
	profile := "Move Profile"
	agentDir := filepath.Join(home, ".ai", "just", "pi")
	app := newApp(profile, agentDir)
	defer app.Close()

	app.tree.AddFolder("source")
	app.tree.AddFolder("target")
	source := app.tree.Root()[0]
	target := app.tree.Root()[1]
	chatA := &tree.Item{Name: "chat-a", CWD: "/work"}
	chatB := &tree.Item{Name: "chat-b", CWD: "/work"}
	source.AddChild(chatA)
	source.AddChild(chatB)

	oldAID := app.tree.SessionKeyOf(chatA)
	oldBID := app.tree.SessionKeyOf(chatB)
	newAID := fullRenameSessionID(profile, []string{"target"}, "chat-a")
	app.currentSessionID = oldAID
	sourceDomain := source.Domain(profile)
	targetDomain := target.Domain(profile)
	for _, id := range []string{oldAID, oldBID} {
		if err := os.MkdirAll(paths.SessionDir(profile, id), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(paths.DomainDir(profile, sourceDomain), "kanban"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.DomainDir(profile, sourceDomain), "notes.json"), []byte("source notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTask := func(name, title, assigned string) {
		t.Helper()
		path := filepath.Join(paths.DomainDir(profile, sourceDomain), "kanban", name)
		data := fmt.Sprintf("---\ntitle: %s\nstatus: progress\nassigned_to: %s\n---\n%s\n", title, assigned, title)
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeTask("task-for-a.md", "Task A", oldAID)
	writeTask("task-for-b.md", "Task B", oldBID)

	oldFamiliarID := oldAID + "__expert"
	newFamiliarID := newAID + "__expert"
	familiarPath := paths.FamiliarsJSONLPath(profile, oldAID)
	if err := os.MkdirAll(filepath.Dir(familiarPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(familiarPath, []byte(fmt.Sprintf(`[{"id":"expert","sessionId":%q}]`, oldFamiliarID)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.SessionDir(profile, oldFamiliarID), 0o755); err != nil {
		t.Fatal(err)
	}
	jsonlDir := filepath.Join(app.renameAgentDir(), "sessions", paths.EncodeCwdDir(chatA.CWD))
	if err := os.MkdirAll(jsonlDir, 0o755); err != nil {
		t.Fatal(err)
	}
	jsonlPath := filepath.Join(jsonlDir, "chat-a.jsonl")
	if err := os.WriteFile(jsonlPath, []byte(fmt.Sprintf("{\"type\":\"session\",\"id\":%q}\n", oldAID)), 0o600); err != nil {
		t.Fatal(err)
	}
	familiarJSONLPath := filepath.Join(jsonlDir, "familiar.jsonl")
	if err := os.WriteFile(familiarJSONLPath, []byte(fmt.Sprintf("{\"type\":\"session\",\"id\":%q}\n", oldFamiliarID)), 0o600); err != nil {
		t.Fatal(err)
	}

	targetTaskPath := filepath.Join(paths.DomainDir(profile, targetDomain), "kanban", "task-for-a.md")
	jobStops := make([]string, 0, 2)
	app.killSessionFn = func(_, sessionID string) error {
		if _, err := os.Stat(targetTaskPath); err != nil {
			t.Errorf("job stop for %s happened before task migration: %v", sessionID, err)
		}
		jobStops = append(jobStops, sessionID)
		return nil
	}

	app.tree.MoveItem(chatA, target)
	if app.currentSessionID != oldAID {
		t.Fatal("move remapped the active session before asynchronous completion")
	}
	if len(jobStops) != 0 {
		t.Fatalf("move stopped jobs before asynchronous completion: %v", jobStops)
	}
	if len(app.pendingBubbleTeaCmds) != 1 {
		t.Fatalf("move completion commands = %d, want 1", len(app.pendingBubbleTeaCmds))
	}
	completionCmd := app.pendingBubbleTeaCmds[0]
	app.pendingBubbleTeaCmds = nil
	app.Update(completionCmd())
	if app.currentSessionID != newAID {
		t.Fatalf("move completion active session = %q, want %q", app.currentSessionID, newAID)
	}

	if len(jobStops) != 2 {
		t.Fatalf("job stops = %v, want chat and familiar after filesystem commit", jobStops)
	}
	if got := app.tree.SessionKeyOf(chatA); got != newAID {
		t.Fatalf("chat-a session ID = %q, want %q", got, newAID)
	}
	if _, err := os.Stat(paths.SessionDir(profile, oldAID)); !os.IsNotExist(err) {
		t.Fatalf("old chat-a session remains: %v", err)
	}
	if _, err := os.Stat(paths.SessionDir(profile, newAID)); err != nil {
		t.Fatalf("new chat-a session missing: %v", err)
	}
	if _, err := os.Stat(paths.SessionDir(profile, oldBID)); err != nil {
		t.Fatalf("chat-b session changed: %v", err)
	}
	gotNotes, err := os.ReadFile(filepath.Join(paths.DomainDir(profile, sourceDomain), "notes.json"))
	if err != nil || string(gotNotes) != "source notes" {
		t.Fatalf("source notes disappeared: err=%v notes=%q", err, gotNotes)
	}
	gotTargetTask, err := os.ReadFile(targetTaskPath)
	if err != nil || !strings.Contains(string(gotTargetTask), "assigned_to: "+newAID) {
		t.Fatalf("assigned task was not migrated to target domain: err=%v data=%q", err, gotTargetTask)
	}
	if _, err := os.Stat(filepath.Join(paths.DomainDir(profile, sourceDomain), "kanban", "task-for-a.md")); !os.IsNotExist(err) {
		t.Fatalf("assigned task remains in source domain: %v", err)
	}
	remainingTask, err := os.ReadFile(filepath.Join(paths.DomainDir(profile, sourceDomain), "kanban", "task-for-b.md"))
	if err != nil || !strings.Contains(string(remainingTask), "assigned_to: "+oldBID) {
		t.Fatalf("unrelated task assignment changed: err=%v data=%q", err, remainingTask)
	}
	gotJSONL, err := os.ReadFile(jsonlPath)
	if err != nil || !strings.Contains(string(gotJSONL), newAID) {
		t.Fatalf("session JSONL was not migrated: err=%v data=%q", err, gotJSONL)
	}
	newFamiliarPath := paths.FamiliarsJSONLPath(profile, newAID)
	gotFamiliars, err := os.ReadFile(newFamiliarPath)
	if err != nil || !strings.Contains(string(gotFamiliars), newFamiliarID) {
		t.Fatalf("familiars mapping was not migrated: err=%v data=%q", err, gotFamiliars)
	}
	if _, err := os.Stat(paths.SessionDir(profile, oldFamiliarID)); !os.IsNotExist(err) {
		t.Fatalf("old familiar session remains: %v", err)
	}
	gotFamiliarJSONL, err := os.ReadFile(familiarJSONLPath)
	if err != nil || !strings.Contains(string(gotFamiliarJSONL), newFamiliarID) {
		t.Fatalf("familiar JSONL was not migrated: err=%v data=%q", err, gotFamiliarJSONL)
	}
}

func TestRenameFolderRollsBackWhenKanbanReadIsIncomplete(t *testing.T) {
	home, dataHome := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AI_DATA_HOME", dataHome)
	profile := "test"
	app := &App{
		profile:               profile,
		piAgentDir:            filepath.Join(home, ".ai", "just", "pi"),
		activeSessions:        make(map[string]struct{}),
		runningSessions:       make(map[string]struct{}),
		emulatorCache:         make(map[string]*portalis.Emulator),
		familiarEmulatorCache: make(map[string]*portalis.Emulator),
		tree:                  tree.New(),
	}
	app.tree.Profile = profile
	app.tree.AddFolder("old-folder")
	folder := app.tree.Root()[0]
	chat := &tree.Item{Name: "chat"}
	folder.AddChild(chat)
	oldID := app.tree.SessionKeyOf(chat)
	app.activeSessions[oldID] = struct{}{}
	app.tree.SetActiveSessionsInMemory(app.activeSessions)

	oldDomain := paths.DomainID(profile, []string{folder.Name})
	newDomain := paths.DomainID(profile, []string{"new-folder"})
	oldKanbanDir := filepath.Join(paths.DomainDir(profile, oldDomain), "kanban")
	if err := os.MkdirAll(oldKanbanDir, 0o755); err != nil {
		t.Fatal(err)
	}
	validPath := filepath.Join(oldKanbanDir, "valid.md")
	validTask := []byte(fmt.Sprintf("---\ntitle: Valid\nstatus: progress\nassigned_to: %s\n---\nbody\n", oldID))
	if err := os.WriteFile(validPath, validTask, 0o644); err != nil {
		t.Fatal(err)
	}
	invalidPath := filepath.Join(oldKanbanDir, "invalid.md")
	if err := os.WriteFile(invalidPath, []byte("not frontmatter\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := buildRenamePlan(folder, "new-folder", profile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.applyRenamePlan(plan); err == nil || !strings.Contains(err.Error(), "read Kanban") {
		t.Fatalf("incomplete Kanban read error = %v, want read Kanban error", err)
	}
	if folder.Name != "old-folder" {
		t.Fatalf("folder name changed despite incomplete Kanban read: %q", folder.Name)
	}
	if _, err := os.Stat(paths.DomainDir(profile, newDomain)); !os.IsNotExist(err) {
		t.Fatalf("target domain remains after failed rename: %v", err)
	}
	gotTask, err := os.ReadFile(validPath)
	if err != nil || string(gotTask) != string(validTask) {
		t.Fatalf("partial Kanban task was changed: data=%q err=%v", gotTask, err)
	}
	if _, active := app.activeSessions[oldID]; !active {
		t.Fatal("active session was not restored after failed migration")
	}
}

func TestRenameRootChatKeepsProfileDomain(t *testing.T) {
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
	if oldID == "" {
		t.Fatal("root chat session ID is empty")
	}
	domain := paths.DomainID(profile, nil)
	domainDir := paths.DomainDir(profile, domain)
	if err := os.MkdirAll(domainDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(domainDir, "notes.json"), []byte("root notes"), 0o644); err != nil {
		t.Fatal(err)
	}

	bindTransactionalRenameForTest(app, app.tree)
	if err := app.tree.RenameItem(chat, "new"); err != nil {
		t.Fatalf("rename root chat: %v", err)
	}
	newID := app.tree.SessionKeyOf(chat)
	if newID == oldID {
		t.Fatalf("root chat session ID did not change: %q", newID)
	}
	got, err := os.ReadFile(filepath.Join(paths.DomainDir(profile, domain), "notes.json"))
	if err != nil {
		t.Fatalf("new root domain missing: %v", err)
	}
	if string(got) != "root notes" {
		t.Fatalf("root notes changed: %q", got)
	}
}
