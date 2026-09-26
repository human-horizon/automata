package main

import (
	"bytes"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HumanHorizon/automata/internal/paths"
	"github.com/HumanHorizon/automata/internal/tree"
	"github.com/Starframe/portalis"
	"github.com/fsnotify/fsnotify"
)

func TestNewAppLogsInvalidTreeSnapshotAndBlocksOverwrite(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	profile := "invalid-startup"
	path := paths.StatePath(profile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	invalid := []byte(`{"version":9,"items":[]}`)
	if err := os.WriteFile(path, invalid, 0o644); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	oldWriter := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(oldWriter)

	app := newApp(profile, "")
	defer app.Close()
	if app.tree.StateLoadError() == nil {
		t.Fatal("newApp discarded the invalid state error")
	}
	if !strings.Contains(logs.String(), "load tree state") {
		t.Fatalf("startup diagnostics missing from logs: %q", logs.String())
	}
	app.tree.AddChat("must-not-overwrite")
	if len(app.tree.Root()) != 0 {
		t.Fatal("Tree accepted a mutation after invalid startup state")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, invalid) {
		t.Fatalf("startup autosave overwrote invalid state: %s", got)
	}
}

func TestTreeDeletePreflightsThenRunsCleanupOnlyAfterStateCommit(t *testing.T) {
	profile := "Delete Transaction"
	t.Setenv("AI_DATA_HOME", t.TempDir())
	app := newApp(profile, "")
	defer app.Close()
	item, err := app.tree.CreateChat("transactional")
	if err != nil {
		t.Fatal(err)
	}
	sessionID := app.tree.SessionKeyOf(item)
	app.currentSessionID = sessionID
	app.activeSessions[sessionID] = struct{}{}
	app.tree.SetActiveSessionsInMemory(app.activeSessions)
	em := portalis.NewEmulator(sessionID, item.Name, "", nil)
	app.emulatorCache[sessionID] = em

	prepareCalls, stopCalls := 0, 0
	app.prepareJobSessionFn = func(_, got string) error {
		prepareCalls++
		if got != sessionID {
			t.Fatalf("preflight session = %q, want %q", got, sessionID)
		}
		return nil
	}
	app.killSessionFn = func(_, got string) error {
		stopCalls++
		if got != sessionID {
			t.Fatalf("stop session = %q, want %q", got, sessionID)
		}
		return nil
	}
	app.tree.SetSaveStateFunc(func() error { return errors.New("state commit failed") })
	if err := app.tree.DeleteItem(item); err == nil {
		t.Fatal("DeleteItem succeeded despite injected state failure")
	}
	if prepareCalls == 0 {
		t.Fatal("delete did not preflight runtime jobs")
	}
	if stopCalls != 0 {
		t.Fatalf("runtime cleanup ran before state commit: %d calls", stopCalls)
	}
	if len(app.tree.Root()) != 1 || app.tree.Root()[0] != item {
		t.Fatal("failed state commit did not restore tree item")
	}
	if app.emulatorCache[sessionID] != em || app.currentSessionID != sessionID {
		t.Fatal("failed state commit changed runtime state")
	}
	if _, ok := app.activeSessions[sessionID]; !ok {
		t.Fatal("failed state commit removed the active session")
	}

	app.tree.SetSaveStateFunc(nil)
	if err := app.tree.DeleteItem(item); err != nil {
		t.Fatalf("DeleteItem after restoring persistence: %v", err)
	}
	if stopCalls != 1 {
		t.Fatalf("post-commit runtime stop calls = %d, want 1", stopCalls)
	}
	if len(app.tree.Root()) != 0 || app.emulatorCache[sessionID] != nil || app.currentSessionID != "" {
		t.Fatal("committed deletion did not clean tree/runtime state")
	}
	if _, ok := app.activeSessions[sessionID]; ok {
		t.Fatal("committed deletion retained the active session")
	}
}

func TestCleanupDeletedTreeItemStopsRuntimeAndRemovesGhostState(t *testing.T) {
	profile := "Delete Profile"
	t.Setenv("AI_DATA_HOME", t.TempDir())
	tr := tree.New()
	tr.Profile = profile
	tr.AddFolder("Projects")
	folder := tr.Root()[0]
	chat := &tree.Item{Name: "Chat"}
	folder.AddChild(chat)
	sessionID := tr.SessionKeyOf(chat)
	familiarID := sessionID + "__expert"

	jobDir := filepath.Join(paths.SessionDir(profile, sessionID), "jobs", "job-stale")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().UTC().Format(time.RFC3339)
	jobRecord := `{"id":"job-stale","pid":2147483647,"status":"running","startedAt":"` + startedAt + `"}`
	if err := os.WriteFile(filepath.Join(jobDir, "job.json"), []byte(jobRecord), 0o644); err != nil {
		t.Fatal(err)
	}

	app := &App{
		tree:                  tr,
		profile:               profile,
		currentSessionID:      sessionID,
		activeSessions:        map[string]struct{}{sessionID: {}, familiarID: {}},
		emulatorCache:         map[string]*portalis.Emulator{sessionID: portalis.NewEmulator(sessionID, "Chat", "", nil)},
		familiarEmulatorCache: map[string]*portalis.Emulator{familiarID: portalis.NewEmulator(familiarID, "Expert", "", nil)},
		sessionWatchers:       make(map[string]*fsnotify.Watcher),
	}

	if err := app.cleanupDeletedTreeItem(folder); err != nil {
		t.Fatal(err)
	}
	if len(app.activeSessions) != 0 || len(app.emulatorCache) != 0 || len(app.familiarEmulatorCache) != 0 {
		t.Fatalf("runtime state remained after delete: active=%v emulators=%v familiars=%v", app.activeSessions, app.emulatorCache, app.familiarEmulatorCache)
	}
	if app.currentSessionID != "" {
		t.Fatalf("current session remained after delete: %q", app.currentSessionID)
	}
	if _, err := os.Stat(jobDir); !os.IsNotExist(err) {
		t.Fatalf("stale job remained after delete: %v", err)
	}
	if got := tr.ActiveSessionIDs(); len(got) != 0 {
		t.Fatalf("tree retained ghost active sessions: %v", got)
	}
}
