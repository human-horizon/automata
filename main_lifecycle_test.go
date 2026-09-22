package main

import (
	"errors"
	"testing"

	"github.com/HumanHorizon/automata/internal/tree"
	"github.com/Starframe/portalis"
	"github.com/fsnotify/fsnotify"
)

func TestStopSessionRuntimeClearsOwnerAndFamiliarState(t *testing.T) {
	tr := tree.New()
	app := &App{
		tree:                  tr,
		profile:               "test",
		activeSessions:        map[string]struct{}{"owner": {}, "owner__expert": {}},
		runningSessions:       map[string]struct{}{"owner": {}, "owner__expert": {}},
		emulatorCache:         map[string]*portalis.Emulator{"owner": portalis.NewEmulator("owner", "owner", "", nil)},
		familiarEmulatorCache: map[string]*portalis.Emulator{"owner__expert": portalis.NewEmulator("owner__expert", "expert", "", nil)},
		sessionWatchers:       make(map[string]*fsnotify.Watcher),
		sessionWatchPending:   map[string]bool{"owner": true, "owner__expert": true},
	}
	killed := make([]string, 0, 2)
	app.killSessionFn = func(_, sessionID string) error {
		killed = append(killed, sessionID)
		return nil
	}

	if err := app.stopSessionRuntime("owner", stopSessionOptions{
		stopJobs:           true,
		stopFamiliars:      true,
		persistInactive:    true,
		familiarSessionIDs: []string{"owner__expert"},
	}); err != nil {
		t.Fatal(err)
	}
	if len(killed) != 2 {
		t.Fatalf("kill calls = %v, want owner and familiar", killed)
	}
	for _, sessionID := range []string{"owner", "owner__expert"} {
		if _, ok := app.emulatorCache[sessionID]; ok {
			t.Fatalf("emulator cache retained %q", sessionID)
		}
		if _, ok := app.familiarEmulatorCache[sessionID]; ok {
			t.Fatalf("familiar emulator cache retained %q", sessionID)
		}
		if _, ok := app.runningSessions[sessionID]; ok {
			t.Fatalf("runningSessions retained %q", sessionID)
		}
		if _, ok := app.activeSessions[sessionID]; ok {
			t.Fatalf("activeSessions retained %q", sessionID)
		}
		if _, ok := app.sessionWatchPending[sessionID]; ok {
			t.Fatalf("sessionWatchPending retained %q", sessionID)
		}
	}
}

func TestCleanupDeletedTreeItemLeavesRuntimeOnJobStopFailure(t *testing.T) {
	tr := tree.New()
	tr.Profile = "test"
	tr.AddChat("chat")
	item := tr.Root()[0]
	sessionID := tr.SessionKeyOf(item)
	app := &App{
		tree:                  tr,
		profile:               "test",
		currentSessionID:      sessionID,
		activeSessions:        map[string]struct{}{sessionID: {}},
		runningSessions:       map[string]struct{}{sessionID: {}},
		emulatorCache:         map[string]*portalis.Emulator{sessionID: portalis.NewEmulator(sessionID, "chat", "", nil)},
		familiarEmulatorCache: make(map[string]*portalis.Emulator),
		sessionWatchers:       make(map[string]*fsnotify.Watcher),
		sessionWatchPending:   make(map[string]bool),
	}
	stopErr := errors.New("job stop failed")
	app.killSessionFn = func(string, string) error { return stopErr }

	if err := app.cleanupDeletedTreeItem(item); !errors.Is(err, stopErr) {
		t.Fatalf("cleanup error = %v, want %v", err, stopErr)
	}
	if _, ok := app.emulatorCache[sessionID]; !ok {
		t.Fatal("runtime emulator was removed after job-stop preflight failure")
	}
	if _, ok := app.runningSessions[sessionID]; !ok {
		t.Fatal("running session was removed after job-stop preflight failure")
	}
	if _, ok := app.activeSessions[sessionID]; !ok {
		t.Fatal("active session was removed after job-stop preflight failure")
	}
	if tr.Root()[0] != item {
		t.Fatal("tree item changed during failed cleanup")
	}
}
