package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/HumanHorizon/automata/internal/paths"
	"github.com/HumanHorizon/automata/internal/tree"
	"github.com/Starframe/portalis"
	"github.com/fsnotify/fsnotify"
)

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
	if err := os.WriteFile(filepath.Join(jobDir, "job.json"), []byte(`{"id":"job-stale","pid":2147483647,"status":"running"}`), 0o644); err != nil {
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
		sessionWatchPending:   map[string]bool{sessionID: true},
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
