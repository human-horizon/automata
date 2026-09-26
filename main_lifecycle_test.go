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
	}
}

func TestStopSessionRuntimeSurfacesActiveSessionPersistenceFailure(t *testing.T) {
	tr := tree.New()
	const sessionID = "owner"
	tr.SetActiveSessionsInMemory(map[string]struct{}{sessionID: {}})
	persistErr := errors.New("active state unavailable")
	tr.SetSaveStateFunc(func() error { return persistErr })
	app := &App{
		tree:                  tr,
		profile:               "test",
		activeSessions:        map[string]struct{}{sessionID: {}},
		runningSessions:       map[string]struct{}{sessionID: {}},
		emulatorCache:         map[string]*portalis.Emulator{sessionID: portalis.NewEmulator(sessionID, "owner", "", nil)},
		familiarEmulatorCache: make(map[string]*portalis.Emulator),
		sessionWatchers:       make(map[string]*fsnotify.Watcher),
	}

	err := app.stopSessionRuntime(sessionID, stopSessionOptions{persistInactive: true})
	if err == nil || !runtimeStopWasCommitted(err) || !errors.Is(err, persistErr) {
		t.Fatalf("persistence error = %v, committed=%v", err, runtimeStopWasCommitted(err))
	}
	if _, ok := app.emulatorCache[sessionID]; ok {
		t.Fatal("runtime emulator remained after committed stop")
	}
	if _, ok := app.activeSessions[sessionID]; ok {
		t.Fatal("active session remained after committed stop")
	}
	if _, ok := app.runningSessions[sessionID]; ok {
		t.Fatal("running session remained after committed stop")
	}
	if got := tr.ActiveSessionIDs(); len(got) != 0 {
		t.Fatalf("Tree active sessions after committed stop = %v, want none", got)
	}
}

func TestStopSessionRuntimeStopsPreparedJobsAfterPersistenceFailure(t *testing.T) {
	tr := tree.New()
	persistErr := errors.New("active state unavailable")
	tr.SetSaveStateFunc(func() error { return persistErr })
	const sessionID = "owner"
	app := &App{
		tree:            tr,
		profile:         "test",
		activeSessions:  map[string]struct{}{sessionID: {}},
		runningSessions: map[string]struct{}{sessionID: {}},
	}
	jobErr := errors.New("job finalization failed")
	jobCalls := 0
	app.killSessionFn = func(string, string) error {
		jobCalls++
		return jobErr
	}

	err := app.stopSessionRuntime(sessionID, stopSessionOptions{stopJobs: true, persistInactive: true})
	if err == nil || !runtimeStopWasCommitted(err) || !runtimeStopPersistenceFailed(err) {
		t.Fatalf("cleanup error = %v, want committed persistence failure", err)
	}
	if !errors.Is(err, persistErr) || !errors.Is(err, jobErr) {
		t.Fatalf("cleanup error = %v, want both persistence and job failures", err)
	}
	if jobCalls != 1 {
		t.Fatalf("job finalization calls = %d, want one after runtime commit", jobCalls)
	}
}

func TestCleanupDeletedTreeItemAbortsOnActiveSessionPersistenceFailure(t *testing.T) {
	tr := tree.New()
	tr.Profile = "test"
	tr.AddChat("chat")
	item := tr.Root()[0]
	tr.SetSaveStateFunc(func() error { return errors.New("active state unavailable") })
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
	}

	if err := app.cleanupDeletedTreeItem(item); err == nil {
		t.Fatal("cleanup unexpectedly succeeded")
	}
	if tr.Root()[0] != item {
		t.Fatal("tree item was removed after active-session persistence failure")
	}
}

func TestCleanupDeletedTreeItemLeavesRuntimeOnJobPreflightFailure(t *testing.T) {
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
	}
	stopErr := errors.New("job preflight failed")
	app.prepareJobSessionFn = func(string, string) error { return stopErr }

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

func TestCleanupDeletedTreeItemCommitsRuntimeOnSignalFailure(t *testing.T) {
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
	}
	app.killSessionFn = func(string, string) error {
		return errors.New("signal failed")
	}

	if err := app.cleanupDeletedTreeItem(item); err != nil {
		t.Fatalf("committed cleanup returned error: %v", err)
	}
	if _, ok := app.emulatorCache[sessionID]; ok {
		t.Fatal("runtime emulator remained after committed cleanup")
	}
	if _, ok := app.runningSessions[sessionID]; ok {
		t.Fatal("running session remained after committed cleanup")
	}
	if _, ok := app.activeSessions[sessionID]; ok {
		t.Fatal("active session remained after committed cleanup")
	}
}

func TestStopSessionRuntimePreflightsAllSessionsBeforeSignals(t *testing.T) {
	app := &App{
		profile:               "test",
		activeSessions:        map[string]struct{}{"owner": {}, "owner__expert": {}},
		runningSessions:       map[string]struct{}{"owner": {}, "owner__expert": {}},
		emulatorCache:         map[string]*portalis.Emulator{"owner": portalis.NewEmulator("owner", "owner", "", nil)},
		familiarEmulatorCache: map[string]*portalis.Emulator{"owner__expert": portalis.NewEmulator("owner__expert", "expert", "", nil)},
		sessionWatchers:       make(map[string]*fsnotify.Watcher),
	}
	prepared := make([]string, 0, 2)
	prepareErr := errors.New("unknown PID identity")
	app.prepareJobSessionFn = func(_, sessionID string) error {
		prepared = append(prepared, sessionID)
		if sessionID == "owner__expert" {
			return prepareErr
		}
		return nil
	}
	app.killSessionFn = func(string, string) error {
		t.Fatal("signal phase started after preflight failure")
		return nil
	}

	err := app.stopSessionRuntime("owner", stopSessionOptions{stopJobs: true, stopFamiliars: true, familiarSessionIDs: []string{"owner__expert"}})
	if !errors.Is(err, prepareErr) || runtimeStopWasCommitted(err) {
		t.Fatalf("preflight error = %v, committed=%v", err, runtimeStopWasCommitted(err))
	}
	if len(prepared) != 2 {
		t.Fatalf("prepared sessions = %v, want both owner and familiar", prepared)
	}
	if len(app.emulatorCache) != 1 || len(app.familiarEmulatorCache) != 1 || len(app.runningSessions) != 2 {
		t.Fatal("runtime mutated after multi-session preflight failure")
	}
}

func TestStopSessionRuntimeCleansAllTargetsAfterPartialSignalFailure(t *testing.T) {
	app := &App{
		profile:               "test",
		activeSessions:        map[string]struct{}{"owner": {}, "owner__expert": {}},
		runningSessions:       map[string]struct{}{"owner": {}, "owner__expert": {}},
		emulatorCache:         map[string]*portalis.Emulator{"owner": portalis.NewEmulator("owner", "owner", "", nil)},
		familiarEmulatorCache: map[string]*portalis.Emulator{"owner__expert": portalis.NewEmulator("owner__expert", "expert", "", nil)},
		sessionWatchers:       make(map[string]*fsnotify.Watcher),
	}
	calls := 0
	app.killSessionFn = func(_, sessionID string) error {
		calls++
		if sessionID == "owner__expert" {
			return errors.New("signal failed")
		}
		return nil
	}

	err := app.stopSessionRuntime("owner", stopSessionOptions{stopJobs: true, stopFamiliars: true, persistInactive: true, familiarSessionIDs: []string{"owner__expert"}})
	if err == nil || !runtimeStopWasCommitted(err) {
		t.Fatalf("partial signal error = %v, committed=%v", err, runtimeStopWasCommitted(err))
	}
	if calls != 2 {
		t.Fatalf("signal calls = %d, want 2", calls)
	}
	if len(app.emulatorCache) != 0 || len(app.familiarEmulatorCache) != 0 || len(app.runningSessions) != 0 || len(app.activeSessions) != 0 {
		t.Fatal("runtime state was not deterministically cleaned after partial signal failure")
	}
}
