package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/HumanHorizon/automata/internal/tree"
	"github.com/Starframe/portalis"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestStopSessionRuntimeCommandPreservesStateUntilJobsFinish(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "success"},
		{name: "failure", err: errors.New("injected job stop failure")},
	} {
		t.Run(test.name, func(t *testing.T) {
			const sessionID = "async-stop__chat"
			em := portalis.NewEmulator(sessionID, "chat", "/tmp", nil)
			app := &App{
				activeSessions:        map[string]struct{}{sessionID: {}},
				runningSessions:       map[string]struct{}{sessionID: {}},
				emulatorCache:         map[string]*portalis.Emulator{sessionID: em},
				familiarEmulatorCache: make(map[string]*portalis.Emulator),
			}
			started := make(chan struct{})
			release := make(chan struct{})
			app.killSessionFn = func(_, got string) error {
				if got != sessionID {
					t.Fatalf("stopped session = %q, want %q", got, sessionID)
				}
				close(started)
				<-release
				return test.err
			}
			cmd, err := app.stopSessionRuntimeCmd(sessionID)
			if err != nil {
				t.Fatal(err)
			}
			if !app.runtimeOperationPending(sessionID) {
				t.Fatal("runtime operation was not reserved before command start")
			}
			completion := make(chan sessionStopJobsCompletedMsg, 1)
			go func() { completion <- cmd().(sessionStopJobsCompletedMsg) }()
			select {
			case <-started:
			case <-time.After(time.Second):
				close(release)
				t.Fatal("job stop command did not start")
			}
			if app.emulatorCache[sessionID] != em || !app.runtimeOperationPending(sessionID) {
				close(release)
				t.Fatal("runtime changed before job stop completed")
			}
			if _, exists := app.activeSessions[sessionID]; !exists {
				close(release)
				t.Fatal("active session was cleared before job stop completed")
			}
			close(release)
			result := <-completion
			app.Update(result)
			if app.runtimeOperationPending(sessionID) {
				t.Fatal("completion retained runtime operation reservation")
			}
			if test.err != nil {
				if app.emulatorCache[sessionID] != em {
					t.Fatal("failed job stop removed runtime emulator")
				}
				if _, exists := app.activeSessions[sessionID]; !exists {
					t.Fatal("failed job stop cleared active session")
				}
				return
			}
			if app.emulatorCache[sessionID] != nil {
				t.Fatal("successful job stop retained runtime emulator")
			}
			if _, exists := app.activeSessions[sessionID]; exists {
				t.Fatal("successful job stop retained active session")
			}
		})
	}
}

func TestAppUpdateReturnsBeforeBlockingStopCommandCompletes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AI_DATA_HOME", t.TempDir())
	app := newApp("", "")
	defer app.Close()
	app.tree.AddChat("agent")
	item := app.tree.AllItems()[0]
	sessionID := app.tree.SessionKeyOf(item)
	em := portalis.NewEmulator(sessionID, item.Name, "/bin/sh", nil)
	app.activeSessions[sessionID] = struct{}{}
	app.runningSessions[sessionID] = struct{}{}
	app.emulatorCache[sessionID] = em
	app.tree.SetActiveSessionsInMemory(app.activeSessions)

	started := make(chan struct{})
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	app.killSessionFn = func(_, got string) error {
		if got != sessionID {
			t.Errorf("stopped session = %q, want %q", got, sessionID)
		}
		close(started)
		<-release
		return nil
	}

	app.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	app.View()
	stopX, stopY := -1, -1
	for y, line := range strings.Split(ansi.Strip(app.tree.View(30, 24)), "\n") {
		index := strings.Index(line, "■")
		if index >= 0 && strings.Contains(line, item.Name) {
			stopX, stopY = ansi.StringWidth(line[:index]), y
			break
		}
	}
	if stopX < 0 {
		t.Fatal("rendered Tree has no active-session stop button")
	}

	type updateResult struct{ cmd tea.Cmd }
	updated := make(chan updateResult, 1)
	go func() {
		_, cmd := app.Update(tea.MouseMsg{
			X: stopX, Y: stopY, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress,
		})
		updated <- updateResult{cmd: cmd}
	}()
	var cmd tea.Cmd
	select {
	case result := <-updated:
		cmd = result.cmd
	case <-started:
		close(release)
		<-updated
		t.Fatal("App.Update blocked on job shutdown")
	case <-time.After(time.Second):
		close(release)
		<-updated
		t.Fatal("App.Update did not return promptly")
	}
	if cmd == nil {
		t.Fatal("Stop click returned no command")
	}
	if !app.runtimeOperationPending(sessionID) || app.emulatorCache[sessionID] != em {
		t.Fatal("Stop click changed runtime state before its command completed")
	}

	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("Stop click command result = %T, want tea.BatchMsg", cmd())
	}
	completed := make(chan sessionStopJobsCompletedMsg, 1)
	for _, child := range batch {
		go func(child tea.Cmd) {
			if result, ok := child().(sessionStopJobsCompletedMsg); ok {
				completed <- result
			}
		}(child)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("returned Stop command did not start the blocking job shutdown")
	}
	if app.emulatorCache[sessionID] != em {
		t.Fatal("blocking job shutdown changed runtime before completion")
	}
	close(release)
	select {
	case result := <-completed:
		app.Update(result)
	case <-time.After(time.Second):
		t.Fatal("blocking Stop command did not return its completion")
	}
	if app.emulatorCache[sessionID] != nil || app.runtimeOperationPending(sessionID) {
		t.Fatal("Stop completion did not clear runtime and reservation")
	}
}

func TestRuntimeOperationRejectsOverlapAndStaleCompletion(t *testing.T) {
	app := &App{}
	operationID, err := app.reserveRuntimeOperation([]string{"chat-a", "chat-b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.reserveRuntimeOperation([]string{"chat-b", "chat-c"}); err == nil {
		t.Fatal("overlapping runtime operation was accepted")
	}
	if app.runtimeOperationPending("chat-c") {
		t.Fatal("rejected operation partially reserved its non-overlapping session")
	}
	app.handleRuntimeJobsCompleted(runtimeJobsCompletedMsg{
		operationID: operationID + 1,
		operation:   "stale completion",
		sessionIDs:  []string{"chat-a", "chat-b"},
	})
	if !app.runtimeOperationOwned(operationID, "chat-a", "chat-b") {
		t.Fatal("stale completion changed another operation's reservation")
	}
	app.handleRuntimeJobsCompleted(runtimeJobsCompletedMsg{
		operationID: operationID,
		sessionIDs:  []string{"chat-a", "chat-b"},
		err:         errors.New("job stop failed"),
	})
	if app.runtimeOperationPending("chat-a") || app.runtimeOperationPending("chat-b") {
		t.Fatal("failed completion retained runtime operation reservation")
	}

	created := 0
	app.createChatEmulatorFn = func(sessionID string) *portalis.Emulator {
		created++
		return portalis.NewEmulator(sessionID, sessionID, "/tmp", nil)
	}
	operationID, err = app.reserveRuntimeOperation([]string{"chat-c"})
	if err != nil {
		t.Fatal(err)
	}
	if em := app.newChatEmulator("chat-c"); em != nil || created != 0 {
		t.Fatal("session start was not blocked by pending runtime operation")
	}
	app.releaseRuntimeOperation(operationID)
}

func TestStopSessionRuntimeClearsOwnerAndFamiliarState(t *testing.T) {
	tr := tree.New()
	app := &App{
		tree:                  tr,
		profile:               "test",
		activeSessions:        map[string]struct{}{"owner": {}, "owner__expert": {}},
		runningSessions:       map[string]struct{}{"owner": {}, "owner__expert": {}},
		emulatorCache:         map[string]*portalis.Emulator{"owner": portalis.NewEmulator("owner", "owner", "", nil)},
		familiarEmulatorCache: map[string]*portalis.Emulator{"owner__expert": portalis.NewEmulator("owner__expert", "expert", "", nil)},
		sessionWatchers:       make(map[string]struct{}),
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
		sessionWatchers:       make(map[string]struct{}),
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
		sessionWatchers:       make(map[string]struct{}),
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
		sessionWatchers:       make(map[string]struct{}),
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
		sessionWatchers:       make(map[string]struct{}),
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
		sessionWatchers:       make(map[string]struct{}),
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
		sessionWatchers:       make(map[string]struct{}),
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
