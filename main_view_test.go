package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/HumanHorizon/automata/internal/paths"
	"github.com/HumanHorizon/automata/internal/scrollback"
	"github.com/HumanHorizon/automata/internal/status"
	"github.com/HumanHorizon/automata/internal/tree"
	"github.com/HumanHorizon/automata/internal/ui"
	"github.com/Starframe/portalis"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/fsnotify/fsnotify"
	"github.com/muesli/termenv"
	warp "github.com/starframe-dev/warp"
)

func installFakePi(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "pi")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return path
}

func TestAppViewModal(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	app := newApp("", "")

	app.warp.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	app.tree.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})

	out := app.View()
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		lw := lipgloss.Width(line)
		aw := ansi.StringWidth(line)
		if lw != 80 {
			t.Errorf("line %d lipgloss width: expected 80, got %d", i, lw)
		}
		if aw != 80 {
			t.Errorf("line %d ansi width: expected 80, got %d", i, aw)
		}
	}
}

func TestClearRestartsChatWithConfiguredPiAgentDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	installFakePi(t)

	const sessionID = "profile__chat"
	piAgentDir := filepath.Join(home, ".ai", "just", "pi")
	app := &App{
		tree:           tree.New(),
		activeSessions: map[string]struct{}{},
		emulatorCache:  make(map[string]*portalis.Emulator),
		piAgentDir:     piAgentDir,
	}

	var restarted *portalis.Emulator
	app.startEmulatorSyncFn = func(em *portalis.Emulator, env []string) error {
		restarted = em
		return nil
	}

	t.Log("Дано: чат запущен с каталогом агента ~/.ai/just/pi")
	t.Log("Когда: пользователь нажимает Clear")
	restartResult := app.clearSessionCmd(sessionID, "", nil)()

	t.Log("Тогда: Clear синхронно запускает новый PTY и сообщает Automata продолжить Listen")
	ready, ok := restartResult.(portalis.PtyReadyMsg)
	if !ok {
		t.Fatalf("Clear returned %T, want portalis.PtyReadyMsg", restartResult)
	}
	if ready.SessionID != sessionID {
		t.Fatalf("restarted session = %q, want %q", ready.SessionID, sessionID)
	}
	if restarted == nil || restarted.SessionID != sessionID {
		t.Fatalf("started session = %v, want %q", restarted, sessionID)
	}

	t.Log("И: эмулятор несёт PI_CODING_AGENT_DIR и canonical default profile в startEnv")
	gotEnv := restarted.StartEnv()
	for _, wantEnv := range []string{
		"PI_CODING_AGENT_DIR=" + piAgentDir,
		"AI_PROFILE=default",
		"AUTOMATA_PROFILE=default",
	} {
		if !containsString(gotEnv, wantEnv) {
			t.Fatalf("start environment = %#v, missing %q", gotEnv, wantEnv)
		}
	}
	if len(gotEnv) != 3 {
		t.Fatalf("start environment = %#v, want agent dir and both profile variables", gotEnv)
	}
}

func TestPiLaunchSetsAgentDirAndProfile(t *testing.T) {
	t.Setenv("PI_CMD", "")
	home := t.TempDir()
	t.Setenv("HOME", home)

	geticDir := filepath.Join(home, ".ai", "getic", "pi")
	fakePi := installFakePi(t)
	app := &App{profile: "Getic", piAgentDir: geticDir}

	t.Log("Когда: резолвер запуска pi вызывается для профиля Getic с --pi getic")
	cmd, args, env := app.piLaunch("getic__chat")

	t.Log("Тогда: запускается /usr/local/bin/pi с --session-id")
	if cmd != fakePi {
		t.Fatalf("cmd = %q, want %q", cmd, fakePi)
	}
	if len(args) != 2 || args[0] != "--session-id" || args[1] != "getic__chat" {
		t.Fatalf("args = %#v, want [--session-id getic__chat]", args)
	}

	t.Log("И: env содержит PI_CODING_AGENT_DIR=getic и обе canonical profile variables")
	want := map[string]bool{
		"PI_CODING_AGENT_DIR=" + geticDir: false,
		"AI_PROFILE=getic":                false,
		"AUTOMATA_PROFILE=getic":          false,
	}
	for _, e := range env {
		if _, ok := want[e]; ok {
			want[e] = true
		}
	}
	for e, found := range want {
		if !found {
			t.Fatalf("env missing %q, got %#v", e, env)
		}
	}
}

func TestPiLaunchHonorsPiCmdOverride(t *testing.T) {
	t.Setenv("PI_CMD", "/bin/bash")
	t.Setenv("AI_PROFILE", "wrong-profile")
	t.Setenv("AUTOMATA_PROFILE", "wrong-profile")
	app := &App{profile: "Keller", piAgentDir: "/whatever"}

	t.Log("Когда: задан PI_CMD (e2e-режим)")
	cmd, _, env := app.piLaunch("keller__chat")

	t.Log("Тогда: используется PI_CMD, а piAgentDir игнорируется")
	if cmd != "/bin/bash" {
		t.Fatalf("cmd = %q, want /bin/bash", cmd)
	}
	for _, e := range env {
		if e == "PI_CODING_AGENT_DIR=/whatever" {
			t.Fatalf("env must not contain PI_CODING_AGENT_DIR with PI_CMD override, got %#v", env)
		}
	}
	for _, want := range []string{"AI_PROFILE=keller", "AUTOMATA_PROFILE=keller"} {
		if !containsString(env, want) {
			t.Fatalf("PI_CMD environment = %#v, missing canonical profile %q", env, want)
		}
	}
}

func TestPiLaunchAlwaysSetsCanonicalProfileEnvironment(t *testing.T) {
	tests := []struct {
		name    string
		profile string
		want    string
	}{
		{name: "default", profile: "", want: "default"},
		{name: "named", profile: "Getic", want: "getic"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("PI_CMD", "/bin/bash")
			t.Setenv("AI_PROFILE", "wrong-profile")
			t.Setenv("AUTOMATA_PROFILE", "wrong-profile")
			app := &App{profile: test.profile}
			cmd, _, env := app.piLaunch("chat")
			if cmd != "/bin/bash" {
				t.Fatalf("cmd = %q, want PI_CMD /bin/bash", cmd)
			}
			for _, key := range []string{"AI_PROFILE", "AUTOMATA_PROFILE"} {
				wantEnv := key + "=" + test.want
				count := 0
				for _, item := range env {
					if strings.HasPrefix(item, key+"=") {
						count++
						if item != wantEnv {
							t.Fatalf("profile environment = %q, want %q", item, wantEnv)
						}
					}
				}
				if count != 1 {
					t.Fatalf("%s entries = %d in %#v, want exactly one", key, count, env)
				}
			}
		})
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestCreateChatEmulatorWithoutPiCommandReturnsNil(t *testing.T) {
	t.Setenv("PI_CMD", "")
	t.Setenv("PATH", t.TempDir()) // no just-pi on PATH
	app := &App{tree: tree.New()} // no piAgentDir

	t.Log("Когда: ни piAgentDir, ни just-pi, ни PI_CMD недоступны")
	em := app.createChatEmulator("profile__chat")

	t.Log("Тогда: эмулятор не создаётся вместо молчаливой деградации в шелл")
	if em != nil {
		t.Fatalf("createChatEmulator = %v, want nil", em)
	}
}

func TestCreateChatEmulatorTerminalUsesShellWithoutPiEnv(t *testing.T) {
	t.Setenv("PI_CMD", "")
	app := &App{
		tree:       tree.New(),
		piAgentDir: "/some/agent/dir",
	}
	app.tree.AddTerminal("term")

	t.Log("Когда: создаётся эмулятор для терминала")
	em := app.createChatEmulator("term")

	t.Log("Тогда: это шелл без PI_CODING_AGENT_DIR")
	if em == nil {
		t.Fatal("createChatEmulator returned nil for terminal")
	}
	for _, e := range em.StartEnv() {
		if strings.HasPrefix(e, "PI_CODING_AGENT_DIR=") {
			t.Fatalf("terminal must not get PI_CODING_AGENT_DIR, got %#v", em.StartEnv())
		}
	}
}

func TestRouteCachedEmulatorMessageHandlesAllPTYMessages(t *testing.T) {
	const sessionID = "background"
	app := &App{
		emulatorCache: map[string]*portalis.Emulator{
			sessionID: portalis.NewEmulator(sessionID, "Background", "/bin/sh", nil),
		},
	}

	tests := []struct {
		name string
		msg  tea.Msg
	}{
		{name: "ready", msg: portalis.PtyReadyMsg{SessionID: sessionID}},
		{name: "output", msg: portalis.PtyOutputMsg{SessionID: sessionID, Data: []byte("output")}},
		{name: "render tick", msg: portalis.RenderTickMsg{SessionID: sessionID}},
		{name: "exit", msg: portalis.PtyExitMsg{SessionID: sessionID}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, handled := app.routeCachedEmulatorMessage(test.msg)
			if test.name == "exit" {
				if handled {
					t.Fatal("exit must remain available for Warp after cache eviction")
				}
				if _, ok := app.emulatorCache[sessionID]; ok {
					t.Fatal("exited emulator remained cached")
				}
				return
			}
			if !handled {
				t.Fatalf("cached PTY message %q was not handled", test.name)
			}
		})
	}
}

func TestPtyReadyKeepsTreeActiveWhenPersistenceFails(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const sessionID = "ready-save-failure"
	persistErr := errors.New("active state unavailable")
	tr := tree.New()
	tr.SetSaveStateFunc(func() error { return persistErr })

	logs := captureLogOutput(t)
	app := &App{
		tree:            tr,
		activeSessions:  make(map[string]struct{}),
		runningSessions: make(map[string]struct{}),
		emulatorCache: map[string]*portalis.Emulator{
			sessionID: portalis.NewEmulator(sessionID, "Ready", "/bin/sh", nil),
		},
	}

	if _, handled := app.routeCachedEmulatorMessage(portalis.PtyReadyMsg{SessionID: sessionID}); !handled {
		t.Fatal("ready was not routed to cached emulator")
	}
	if _, active := app.activeSessions[sessionID]; !active {
		t.Fatal("App did not retain the started session")
	}
	if _, running := app.runningSessions[sessionID]; !running {
		t.Fatal("runningSessions did not retain the started session")
	}
	if got := tr.ActiveSessionIDs(); !reflect.DeepEqual(got, []string{sessionID}) {
		t.Fatalf("Tree active sessions = %v, want [%s] after persistence failure", got, sessionID)
	}
	if !strings.Contains(logs.String(), persistErr.Error()) {
		t.Fatalf("persistence error was not logged: %q", logs.String())
	}
}

func captureLogOutput(t *testing.T) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	return &output
}

func TestPtyReadyActivatesSessionOnlyAfterReady(t *testing.T) {
	const sessionID = "ready-gated"
	app := &App{
		activeSessions: make(map[string]struct{}),
		emulatorCache: map[string]*portalis.Emulator{
			sessionID: portalis.NewEmulator(sessionID, "Ready", "/bin/sh", nil),
		},
		tree: tree.New(),
	}

	if _, handled := app.routeCachedEmulatorMessage(portalis.PtyOutputMsg{SessionID: sessionID}); !handled {
		t.Fatal("output was not routed to cached emulator")
	}
	if _, active := app.activeSessions[sessionID]; active {
		t.Fatal("session became active before PtyReadyMsg")
	}

	if _, handled := app.routeCachedEmulatorMessage(portalis.PtyReadyMsg{SessionID: sessionID}); !handled {
		t.Fatal("ready was not routed to cached emulator")
	}
	if _, active := app.activeSessions[sessionID]; !active {
		t.Fatal("session did not become active after PtyReadyMsg")
	}
}

func TestAssignedTaskStartKeepsTreeActiveWhenPersistenceFails(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	t.Setenv("PI_CMD", "/bin/sh")
	tr := tree.New()
	tr.Profile = "task-start"
	tr.AddChat("assigned-chat")
	item := tr.Root()[0]
	sessionID := tr.SessionKeyOf(item)
	persistErr := errors.New("active state unavailable")
	tr.SetSaveStateFunc(func() error { return persistErr })

	logs := captureLogOutput(t)
	app := &App{
		tree:            tr,
		profile:         tr.Profile,
		piAgentDir:      t.TempDir(),
		activeSessions:  make(map[string]struct{}),
		runningSessions: make(map[string]struct{}),
		emulatorCache:   make(map[string]*portalis.Emulator),
	}
	started := false
	app.startEmulatorSyncFn = func(em *portalis.Emulator, _ []string) error {
		if em.SessionID != sessionID {
			t.Fatalf("started session = %q, want %q", em.SessionID, sessionID)
		}
		started = true
		return nil
	}

	_, err := app.startAssignedTaskSession(sessionID)
	var committedErr *ui.CommittedActionError
	if !errors.As(err, &committedErr) {
		t.Fatalf("startAssignedTaskSession error = %v, want committed persistence warning", err)
	}
	if !started {
		t.Fatal("task assignment did not start its PTY")
	}
	if _, ok := app.emulatorCache[sessionID]; !ok {
		t.Fatal("started task session is missing from emulator cache")
	}
	if _, ok := app.runningSessions[sessionID]; !ok {
		t.Fatal("started task session is missing from runningSessions")
	}
	if _, ok := app.activeSessions[sessionID]; !ok {
		t.Fatal("started task session is missing from App activeSessions")
	}
	if !tr.IsActiveSession(item) {
		t.Fatal("Tree does not reflect the started task session after persistence failure")
	}
	if !strings.Contains(logs.String(), persistErr.Error()) {
		t.Fatalf("persistence error was not logged: %q", logs.String())
	}
}

func TestAssignedTaskStartRejectsMissingOrNonChatTreeItems(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	tr := tree.New()
	tr.Profile = "task-preflight"
	terminal, err := tr.CreateTerminal("Terminal")
	if err != nil {
		t.Fatal(err)
	}

	app := &App{
		tree:            tr,
		activeSessions:  make(map[string]struct{}),
		runningSessions: make(map[string]struct{}),
		emulatorCache:   make(map[string]*portalis.Emulator),
	}
	started := false
	app.startEmulatorSyncFn = func(*portalis.Emulator, []string) error {
		started = true
		return nil
	}

	for _, sessionID := range []string{"task-preflight__missing-chat", tr.SessionKeyOf(terminal)} {
		if _, err := app.startAssignedTaskSession(sessionID); err == nil {
			t.Errorf("startAssignedTaskSession(%q) succeeded, want preflight error", sessionID)
		}
	}
	if started {
		t.Fatal("invalid assigned Tree item started a PTY")
	}
	if len(app.emulatorCache) != 0 || len(app.activeSessions) != 0 {
		t.Fatalf("invalid assignment mutated runtime state: cache=%v active=%v", app.emulatorCache, app.activeSessions)
	}
}

func TestAssignedTaskStartRollsBackBeforePTYCommit(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	t.Setenv("PI_CMD", "/bin/sh")
	tr := tree.New()
	tr.Profile = "task-start-failure"
	tr.AddChat("assigned-chat")
	sessionID := tr.SessionKeyOf(tr.Root()[0])
	app := &App{
		tree:            tr,
		piAgentDir:      t.TempDir(),
		activeSessions:  make(map[string]struct{}),
		runningSessions: make(map[string]struct{}),
		emulatorCache:   make(map[string]*portalis.Emulator),
	}
	app.startEmulatorSyncFn = func(*portalis.Emulator, []string) error {
		return errors.New("injected PTY start failure")
	}

	if cmd, err := app.startAssignedTaskSession(sessionID); err == nil || cmd != nil {
		t.Fatalf("startAssignedTaskSession() = (%v, %v), want pre-commit error and no command", cmd, err)
	}
	if len(app.emulatorCache) != 0 || len(app.runningSessions) != 0 || len(app.activeSessions) != 0 {
		t.Fatalf("failed PTY start committed runtime state: cache=%v running=%v active=%v", app.emulatorCache, app.runningSessions, app.activeSessions)
	}
	if tr.IsActiveSession(tr.Root()[0]) {
		t.Fatal("failed PTY start marked Tree session active")
	}
}

func TestAssignedTaskStartDoesNotDuplicateCachedEmulator(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	t.Setenv("PI_CMD", "/bin/sh")
	tr := tree.New()
	tr.Profile = "task-duplicate"
	tr.AddChat("assigned-chat")
	sessionID := tr.SessionKeyOf(tr.Root()[0])
	app := &App{
		tree:            tr,
		piAgentDir:      t.TempDir(),
		activeSessions:  make(map[string]struct{}),
		runningSessions: make(map[string]struct{}),
		emulatorCache:   make(map[string]*portalis.Emulator),
	}
	starts := 0
	app.startEmulatorSyncFn = func(*portalis.Emulator, []string) error {
		starts++
		return nil
	}

	if _, err := app.startAssignedTaskSession(sessionID); err != nil {
		t.Fatalf("first start failed: %v", err)
	}
	if cmd, err := app.startAssignedTaskSession(sessionID); err != nil || cmd != nil {
		t.Fatalf("duplicate start = (%v, %v), want no-op", cmd, err)
	}
	if starts != 1 {
		t.Fatalf("PTY start count = %d, want 1", starts)
	}
}

func TestTreeMutationsRefreshCurrentKanbanPicker(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	tr := tree.New()
	tr.Profile = "picker-profile"
	folder, err := tr.CreateFolder("Projects")
	if err != nil {
		t.Fatal(err)
	}
	firstChat, err := tr.CreateChildChat(folder, "Alpha")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tr.CreateChildTerminal(folder, "Terminal"); err != nil {
		t.Fatal(err)
	}
	outside, err := tr.CreateChat("Outside")
	if err != nil {
		t.Fatal(err)
	}

	container := ui.NewContainer(nil)
	app := &App{tree: tr, container: container}
	app.updateChatList(folder)
	app.bindChatPickerToTree()
	assertChatNames := func(want ...string) {
		t.Helper()
		chats := container.Chats()
		if len(chats) != len(want) {
			t.Fatalf("picker chats = %+v, want names %v", chats, want)
		}
		for i, name := range want {
			if chats[i].Name != name {
				t.Fatalf("picker chat[%d] = %+v, want name %q", i, chats[i], name)
			}
		}
	}
	assertChatNames("Alpha")

	movedChat, err := tr.CreateChildChat(folder, "Beta")
	if err != nil {
		t.Fatal(err)
	}
	assertChatNames("Alpha", "Beta")
	oldSessionID := tr.SessionKeyOf(movedChat)
	if err := tr.RenameItem(movedChat, "Gamma"); err != nil {
		t.Fatal(err)
	}
	if movedChat.Name != "Gamma" || tr.SessionKeyOf(movedChat) == oldSessionID {
		t.Fatal("renamed chat identity was not reflected in Tree")
	}
	assertChatNames("Alpha", "Gamma")

	if err := tr.MoveItemChecked(movedChat, outside); err != nil {
		t.Fatal(err)
	}
	assertChatNames("Alpha")
	if err := tr.DeleteItem(firstChat); err != nil {
		t.Fatal(err)
	}
	assertChatNames()
	if err := tr.DeleteItem(folder); err != nil {
		t.Fatal(err)
	}
	assertChatNames("Outside", "Gamma")
	container.Close()
}

func TestClearSessionErrorMessageSurfacesOnCapturedChatPanel(t *testing.T) {
	panel := ui.NewChatPanel(portalis.NewEmulator("clear-chat", "Chat", "/bin/sh", nil), "clear-chat", "")
	app := &App{}
	_, cmd := app.Update(clearSessionErrorMsg{
		sessionID: "clear-chat",
		panel:     panel,
		err:       errors.New("cleanup failed\nrestart skipped"),
	})
	if cmd != nil {
		t.Fatalf("clear error Update command = %v, want nil", cmd)
	}
	if view := panel.View(100, 3); !strings.Contains(view, "cleanup failed; restart skipped") {
		t.Fatalf("clear error was not displayed on the captured panel: %q", view)
	}
}

func TestClearRestartFailureDoesNotRestoreActiveSession(t *testing.T) {
	t.Setenv("PI_CMD", "/bin/sh")
	const sessionID = "restart-failure"
	app := &App{
		tree:           tree.New(),
		activeSessions: map[string]struct{}{sessionID: {}},
		emulatorCache:  map[string]*portalis.Emulator{sessionID: portalis.NewEmulator(sessionID, "Old", "/bin/sh", nil)},
		piAgentDir:     t.TempDir(),
	}
	app.startEmulatorSyncFn = func(*portalis.Emulator, []string) error {
		return fmt.Errorf("injected start failure")
	}

	msg := app.clearSessionCmd(sessionID, "", nil)()
	clearErr, ok := msg.(clearSessionErrorMsg)
	if !ok || clearErr.err == nil || !strings.Contains(clearErr.err.Error(), "injected start failure") {
		t.Fatalf("failed restart result = %#v, want visible startup error", msg)
	}
	if _, ok := app.emulatorCache[sessionID]; ok {
		t.Fatal("failed restart remained in emulator cache")
	}
	if _, ok := app.activeSessions[sessionID]; ok {
		t.Fatal("failed restart remained active")
	}
}

func TestClearAggregatesCommittedCleanupErrorsAndDoesNotRestart(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	t.Setenv("PI_CMD", "/bin/sh")
	profile := "clear-errors"
	ownerID := "clear-errors__chat"
	familiarID := ownerID + "__expert"
	agentDir := t.TempDir()
	sessionDir := filepath.Join(agentDir, "sessions", "other-cwd")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a-familiar.jsonl", "b-owner.jsonl"} {
		if err := os.WriteFile(filepath.Join(sessionDir, name), []byte("not-json\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	registryPath := paths.FamiliarsJSONLPath(profile, ownerID)
	if err := os.MkdirAll(filepath.Dir(registryPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(registryPath, 0o755); err != nil {
		t.Fatal(err)
	}

	tr := tree.New()
	tr.Profile = profile
	tr.AddChat("chat")
	app := &App{
		tree:            tr,
		profile:         profile,
		piAgentDir:      agentDir,
		activeSessions:  map[string]struct{}{ownerID: {}, familiarID: {}},
		runningSessions: map[string]struct{}{ownerID: {}, familiarID: {}},
		emulatorCache: map[string]*portalis.Emulator{
			ownerID: portalis.NewEmulator(ownerID, "Chat", "/bin/sh", nil),
		},
		familiarEmulatorCache: make(map[string]*portalis.Emulator),
	}
	app.killSessionFn = func(_, _ string) error { return errors.New("injected job stop failure") }
	restarted := false
	app.startEmulatorSyncFn = func(*portalis.Emulator, []string) error {
		restarted = true
		return nil
	}

	msg := app.clearSessionCmd(ownerID, "", []string{familiarID})()
	clearErr, ok := msg.(clearSessionErrorMsg)
	if !ok {
		t.Fatalf("Clear result = %T, want aggregated cleanup error", msg)
	}
	for _, want := range []string{"injected job stop failure", "clear familiar", "clear session history", "clear familiars.json"} {
		if !strings.Contains(clearErr.err.Error(), want) {
			t.Errorf("Clear error %q does not include %q", clearErr.err, want)
		}
	}
	if restarted {
		t.Fatal("Clear restarted Pi despite committed cleanup errors")
	}
	if _, exists := app.emulatorCache[ownerID]; exists {
		t.Fatal("stopped emulator remained in cache after cleanup error")
	}
	if _, active := app.activeSessions[ownerID]; active {
		t.Fatal("stopped session remained active after committed cleanup")
	}
}

func TestRouteCachedFamiliarEmulatorMessageKeepsListenChain(t *testing.T) {
	const sessionID = "background-familiar"
	em := portalis.NewEmulator(sessionID, "Expert", "/bin/sh", nil)
	if err := em.StartSync(nil); err != nil {
		t.Fatalf("start familiar emulator: %v", err)
	}
	defer em.Stop()

	app := &App{
		emulatorCache:         make(map[string]*portalis.Emulator),
		familiarEmulatorCache: map[string]*portalis.Emulator{sessionID: em},
	}

	const marker = "background familiar output"
	cmd, handled := app.routeCachedEmulatorMessage(portalis.PtyOutputMsg{
		SessionID: sessionID,
		Data:      []byte(marker),
	})
	if !handled {
		t.Fatal("familiar PTY output was not handled while its ChatPanel was inactive")
	}
	if cmd == nil {
		t.Fatal("familiar PTY output did not continue the Listen chain")
	}
	if got := em.View(80, 24); !strings.Contains(got, marker) {
		t.Fatalf("familiar emulator view does not contain routed output %q: %q", marker, got)
	}
}

func TestCreateFamiliarEmulatorReusesCachedEmulator(t *testing.T) {
	t.Setenv("PI_CMD", "/bin/sh")
	const sessionID = "cached-familiar"
	app := &App{profile: "test"}

	first, firstEnv := app.createFamiliarEmulator(sessionID)
	if first == nil {
		t.Fatal("first familiar emulator is nil")
	}

	second, secondEnv := app.createFamiliarEmulator(sessionID)
	if second != first {
		t.Fatalf("familiar emulator was recreated: first=%p second=%p", first, second)
	}
	if !reflect.DeepEqual(secondEnv, firstEnv) {
		t.Fatalf("reused familiar environment = %#v, want %#v", secondEnv, firstEnv)
	}
	if got := app.familiarEmulatorCache[sessionID]; got != first {
		t.Fatalf("cache contains %p, want original emulator %p", got, first)
	}
}

func TestFamiliarExitEvictsCacheAndCreatesFreshEmulator(t *testing.T) {
	t.Setenv("PI_CMD", "/bin/sh")
	const sessionID = "exited-familiar"
	app := &App{
		activeSessions:        map[string]struct{}{sessionID: {}},
		familiarEmulatorCache: make(map[string]*portalis.Emulator),
	}

	first, firstEnv := app.createFamiliarEmulator(sessionID)
	if first == nil {
		t.Fatal("first familiar emulator is nil")
	}

	_, handled := app.routeCachedEmulatorMessage(portalis.PtyExitMsg{SessionID: sessionID})
	if handled {
		t.Fatal("familiar PtyExitMsg was swallowed before ChatPanel could remove its tab")
	}
	if _, ok := app.familiarEmulatorCache[sessionID]; ok {
		t.Fatal("exited familiar remained in familiarEmulatorCache")
	}
	if _, ok := app.activeSessions[sessionID]; ok {
		t.Fatal("exited familiar remained in activeSessions")
	}

	second, secondEnv := app.createFamiliarEmulator(sessionID)
	if second == nil {
		t.Fatal("replacement familiar emulator is nil")
	}
	if second == first {
		t.Fatal("replacement familiar reused the exited emulator")
	}
	if !reflect.DeepEqual(secondEnv, firstEnv) {
		t.Fatalf("replacement familiar environment = %#v, want %#v", secondEnv, firstEnv)
	}
}

func TestRouteCachedEmulatorMessageLeavesUnknownMessagesForWarp(t *testing.T) {
	app := &App{emulatorCache: make(map[string]*portalis.Emulator)}

	tests := []struct {
		name string
		msg  tea.Msg
	}{
		{name: "unknown session", msg: portalis.PtyOutputMsg{SessionID: "context-session", Data: []byte("output")}},
		{name: "non PTY message", msg: tea.KeyMsg{Type: tea.KeyEnter}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, handled := app.routeCachedEmulatorMessage(test.msg)
			if handled {
				t.Fatalf("message %q must remain available for Warp", test.name)
			}
		})
	}
}

func TestKnowledgeRefreshDoesNotSchedulePeriodicWork(t *testing.T) {
	app := &App{container: &ui.Container{}}
	_, cmd := app.Update(ui.KnowledgeRefreshMsg{})
	if cmd != nil {
		t.Fatal("KnowledgeRefreshMsg unexpectedly scheduled periodic work")
	}
}

func TestWindowResizeDoesNotSchedulePeriodicWork(t *testing.T) {
	w := warp.New()
	w.SetRoot(nil)
	app := &App{
		warp: w,
		tree: tree.New(),
	}
	_, cmd := app.Update(tea.WindowSizeMsg{})
	if cmd != nil {
		t.Fatal("WindowSizeMsg unexpectedly scheduled periodic work")
	}
}

// TestClearKillsFamiliarsOfThisSession verifies that Clear on the main session
// also stops the running familiar emulators, deletes their JSONL histories,
// and clears familiars.json so the UI eventually drops the familiar tabs.
func TestClearKillsFamiliarsOfThisSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	installFakePi(t)

	const (
		mainSID = "humanhorizon__chat"
		fam1SID = "humanhorizon__chat__expert"
		fam2SID = "humanhorizon__chat__helper"
		cwd     = "/Users/a/Space"
	)

	// 1. Lay down JSONL files for the two familiars.
	fam1JSONL := writeJSONLFixture(t, home, cwd, fam1SID, time.Now())
	fam2JSONL := writeJSONLFixture(t, home, cwd, fam2SID, time.Now())

	// 2. Lay down familiars.json for the main session.
	famDir := filepath.Dir(paths.FamiliarsJSONLPath("", mainSID))
	if err := os.MkdirAll(famDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	familiars := []map[string]string{
		{"id": "expert", "sessionId": fam1SID, "created": "2024-01-01"},
		{"id": "helper", "sessionId": fam2SID, "created": "2024-01-01"},
	}
	famBytes, _ := json.Marshal(familiars)
	if err := os.WriteFile(filepath.Join(famDir, "familiars.json"), famBytes, 0o644); err != nil {
		t.Fatalf("WriteFile familiars.json: %v", err)
	}

	// 3. Build minimal App with cached familiar emulators.
	fam1Em := portalis.NewEmulator(fam1SID, "expert", "/bin/sh", nil)
	fam2Em := portalis.NewEmulator(fam2SID, "helper", "/bin/sh", nil)
	app := &App{
		tree:           tree.New(),
		activeSessions: map[string]struct{}{mainSID: {}, fam1SID: {}, fam2SID: {}},
		emulatorCache: map[string]*portalis.Emulator{
			fam1SID: fam1Em,
			fam2SID: fam2Em,
		},
		profile:    "",
		piAgentDir: filepath.Join(home, ".ai", "just", "pi"),
	}
	app.startEmulatorSyncFn = func(em *portalis.Emulator, env []string) error {
		return nil
	}

	// 4. Act: Clear with the two familiar session ids.
	msg := app.clearSessionCmd(mainSID, cwd, []string{fam1SID, fam2SID})()

	// 5. Familiars removed from emulator cache.
	if _, ok := app.emulatorCache[fam1SID]; ok {
		t.Errorf("familiar %q still in emulatorCache", fam1SID)
	}
	if _, ok := app.emulatorCache[fam2SID]; ok {
		t.Errorf("familiar %q still in emulatorCache", fam2SID)
	}

	// 6. Familiars JSONL files are gone.
	if _, err := os.Stat(fam1JSONL); !os.IsNotExist(err) {
		t.Errorf("familiar %q JSONL still exists: %v", fam1SID, err)
	}
	if _, err := os.Stat(fam2JSONL); !os.IsNotExist(err) {
		t.Errorf("familiar %q JSONL still exists: %v", fam2SID, err)
	}

	// 7. familiars.json now contains an explicit empty list.
	famData, err := os.ReadFile(filepath.Join(famDir, "familiars.json"))
	if err != nil {
		t.Fatalf("ReadFile familiars.json: %v", err)
	}
	if got := strings.TrimSpace(string(famData)); got != "[]" {
		t.Errorf("familiars.json = %q, want %q", got, "[]")
	}

	// 8. Main session is cached but becomes active only when Bubble Tea routes
	// the ready message returned by the restart command.
	if _, ok := app.emulatorCache[mainSID]; !ok {
		t.Error("main emulator missing from emulatorCache after restart")
	}
	if _, ok := app.activeSessions[mainSID]; ok {
		t.Error("main became active before PtyReadyMsg was routed")
	}
	if _, handled := app.routeCachedEmulatorMessage(msg); !handled {
		t.Fatal("restart PtyReadyMsg was not routed to the cached emulator")
	}
	if _, ok := app.activeSessions[mainSID]; !ok {
		t.Error("main not in activeSessions after PtyReadyMsg")
	}
	if _, ok := app.activeSessions[fam1SID]; ok {
		t.Errorf("familiar %q still in activeSessions", fam1SID)
	}
	if _, ok := app.activeSessions[fam2SID]; ok {
		t.Errorf("familiar %q still in activeSessions", fam2SID)
	}

	// 9. Return value is PtyReadyMsg so Warp starts Listen on the new PTY.
	if _, ok := msg.(portalis.PtyReadyMsg); !ok {
		t.Errorf("clearSession returned %T, want portalis.PtyReadyMsg", msg)
	}
}

// TestCloseFamiliarCleansHostState verifies that confirmed familiar close
// removes the emulator state, JSONL history, and familiars.json entry.
func TestCloseFamiliarCleansHostState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AI_DATA_HOME", home)

	const (
		profile = "close-familiar-profile"
		mainSID = "close-familiar-profile__chat"
		famSID  = "close-familiar-profile__chat__expert"
		cwd     = "/tmp"
	)

	famJSONL := writeJSONLFixture(t, home, cwd, famSID, time.Now())
	mainEm := portalis.NewEmulator(mainSID, "chat", cwd, nil)
	familiarEm := portalis.NewEmulator(famSID, "expert", cwd, nil)
	chatPanel := ui.NewChatPanel(mainEm, mainSID, profile)
	container := ui.NewContainer(chatPanel)
	familiarsPath := paths.FamiliarsJSONLPath(profile, mainSID)
	if err := os.MkdirAll(filepath.Dir(familiarsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll familiars directory: %v", err)
	}
	if err := os.WriteFile(familiarsPath, []byte(`[{"id":"expert","sessionId":"`+famSID+`"},{"id":"helper","sessionId":"`+mainSID+`__helper"}]`), 0o644); err != nil {
		t.Fatalf("WriteFile familiars.json: %v", err)
	}

	app := &App{
		container:      container,
		tree:           tree.New(),
		activeSessions: map[string]struct{}{mainSID: {}, famSID: {}},
		emulatorCache: map[string]*portalis.Emulator{
			famSID: familiarEm,
		},
		familiarEmulatorCache: map[string]*portalis.Emulator{
			famSID: familiarEm,
		},
		profile:    profile,
		piAgentDir: filepath.Join(home, ".ai", "just", "pi"),
	}
	app.tree.Profile = profile

	app.closeFamiliar(famSID, familiarEm)

	if _, ok := app.emulatorCache[famSID]; ok {
		t.Fatal("familiar remained in emulatorCache")
	}
	if _, ok := app.familiarEmulatorCache[famSID]; ok {
		t.Fatal("familiar remained in familiarEmulatorCache")
	}
	if _, ok := app.activeSessions[famSID]; ok {
		t.Fatal("familiar remained in activeSessions")
	}
	if _, err := os.Stat(famJSONL); !os.IsNotExist(err) {
		t.Fatalf("familiar JSONL still exists: %v", err)
	}
	data, err := os.ReadFile(familiarsPath)
	if err != nil {
		t.Fatalf("ReadFile familiars.json: %v", err)
	}
	var entries []paths.FamiliarEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("decode familiars.json: %v", err)
	}
	if len(entries) != 1 || entries[0].SessionID != mainSID+"__helper" {
		t.Fatalf("unexpected remaining familiars: %#v", entries)
	}
}

func TestCleanupExternallyRemovedFamiliarPreservesRegistryAndJSONL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AI_DATA_HOME", home)
	const (
		profile = "external-familiar-profile"
		mainSID = "external-familiar-profile__chat"
		famSID  = "external-familiar-profile__chat__expert"
		cwd     = "/tmp"
	)
	jsonlPath := writeJSONLFixture(t, home, cwd, famSID, time.Now())
	registryPath := paths.FamiliarsJSONLPath(profile, mainSID)
	if err := os.MkdirAll(filepath.Dir(registryPath), 0o755); err != nil {
		t.Fatal(err)
	}
	registryContents := []byte(`[]`)
	if err := os.WriteFile(registryPath, registryContents, 0o644); err != nil {
		t.Fatal(err)
	}

	em := portalis.NewEmulator(famSID, "expert", cwd, nil)
	app := &App{
		tree:                  tree.New(),
		profile:               profile,
		activeSessions:        map[string]struct{}{famSID: {}},
		runningSessions:       map[string]struct{}{famSID: {}},
		emulatorCache:         map[string]*portalis.Emulator{famSID: em},
		familiarEmulatorCache: map[string]*portalis.Emulator{famSID: em},
		sessionWatchPending:   map[string]bool{famSID: true},
	}
	app.tree.Profile = profile
	if err := app.cleanupExternallyRemovedFamiliar(famSID, em); err != nil {
		t.Fatalf("cleanupExternallyRemovedFamiliar: %v", err)
	}
	for name, sessions := range map[string]map[string]struct{}{
		"active":  app.activeSessions,
		"running": app.runningSessions,
	} {
		if _, exists := sessions[famSID]; exists {
			t.Fatalf("familiar remains in %s sessions", name)
		}
	}
	if _, exists := app.emulatorCache[famSID]; exists {
		t.Fatal("familiar remains in emulator cache")
	}
	if _, exists := app.familiarEmulatorCache[famSID]; exists {
		t.Fatal("familiar remains in familiar emulator cache")
	}
	if _, exists := app.sessionWatchPending[famSID]; exists {
		t.Fatal("familiar remains in watcher pending map")
	}
	if _, err := os.Stat(jsonlPath); err != nil {
		t.Fatalf("external removal deleted familiar JSONL: %v", err)
	}
	gotRegistry, err := os.ReadFile(registryPath)
	if err != nil || string(gotRegistry) != string(registryContents) {
		t.Fatalf("external removal changed registry: contents=%q err=%v", gotRegistry, err)
	}
}

func TestCleanupExternallyRemovedFamiliarPreflightFailurePreservesRuntime(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AI_DATA_HOME", home)
	const (
		profile = "external-familiar-preflight"
		mainSID = "external-familiar-preflight__chat"
		famSID  = "external-familiar-preflight__chat__expert"
		cwd     = "/tmp"
	)
	jsonlPath := writeJSONLFixture(t, home, cwd, famSID, time.Now())
	registryPath := paths.FamiliarsJSONLPath(profile, mainSID)
	if err := os.MkdirAll(filepath.Dir(registryPath), 0o755); err != nil {
		t.Fatal(err)
	}
	registryContents := []byte(`[]`)
	if err := os.WriteFile(registryPath, registryContents, 0o644); err != nil {
		t.Fatal(err)
	}
	em := portalis.NewEmulator(famSID, "expert", cwd, nil)
	prepareErr := errors.New("unknown job PID identity")
	app := &App{
		profile:               profile,
		activeSessions:        map[string]struct{}{famSID: {}},
		runningSessions:       map[string]struct{}{famSID: {}},
		emulatorCache:         map[string]*portalis.Emulator{famSID: em},
		familiarEmulatorCache: map[string]*portalis.Emulator{famSID: em},
	}
	app.prepareJobSessionFn = func(string, string) error { return prepareErr }
	if err := app.cleanupExternallyRemovedFamiliar(famSID, em); !errors.Is(err, prepareErr) {
		t.Fatalf("cleanup error = %v, want preflight error", err)
	}
	for name, sessions := range map[string]map[string]struct{}{
		"active":  app.activeSessions,
		"running": app.runningSessions,
	} {
		if _, exists := sessions[famSID]; !exists {
			t.Fatalf("preflight failure removed familiar from %s sessions", name)
		}
	}
	if app.emulatorCache[famSID] != em || app.familiarEmulatorCache[famSID] != em {
		t.Fatal("preflight failure removed cached familiar emulator")
	}
	if _, err := os.Stat(jsonlPath); err != nil {
		t.Fatalf("preflight failure changed JSONL: %v", err)
	}
	gotRegistry, err := os.ReadFile(registryPath)
	if err != nil || string(gotRegistry) != string(registryContents) {
		t.Fatalf("preflight failure changed registry: contents=%q err=%v", gotRegistry, err)
	}
}

func TestCloseFamiliarCompletesHostCleanupAfterCommittedPersistenceFailure(t *testing.T) {
	dataHome := t.TempDir()
	home := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	t.Setenv("HOME", home)

	const (
		profile = "close-familiar-warning"
		mainSID = "close-familiar-warning__chat"
		famSID  = "close-familiar-warning__chat__expert"
		cwd     = "/tmp"
	)
	familiarJSONL := writeJSONLFixture(t, home, cwd, famSID, time.Now())
	mainEm := portalis.NewEmulator(mainSID, "chat", cwd, nil)
	familiarEm := portalis.NewEmulator(famSID, "expert", cwd, nil)
	panel := ui.NewChatPanel(mainEm, mainSID, profile)
	familiarsPath := paths.FamiliarsJSONLPath(profile, mainSID)
	if err := os.MkdirAll(filepath.Dir(familiarsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(familiarsPath, []byte(`[{"id":"expert","sessionId":"`+famSID+`"}]`), 0o644); err != nil {
		t.Fatal(err)
	}

	persistErr := errors.New("active state unavailable")
	tr := tree.New()
	tr.Profile = profile
	tr.SetSaveStateFunc(func() error { return persistErr })
	app := &App{
		tree:            tr,
		container:       ui.NewContainer(panel),
		profile:         profile,
		piAgentDir:      filepath.Join(home, ".ai", "just", "pi"),
		activeSessions:  map[string]struct{}{famSID: {}},
		emulatorCache:   map[string]*portalis.Emulator{famSID: familiarEm},
		runningSessions: map[string]struct{}{famSID: {}},
	}

	err := app.closeFamiliar(famSID, familiarEm)
	if !errors.Is(err, persistErr) {
		t.Fatalf("close familiar error = %v, want persistence error", err)
	}
	var committedErr *ui.CommittedCleanupError
	if !errors.As(err, &committedErr) {
		t.Fatalf("close familiar error = %T, want committed cleanup warning", err)
	}
	if _, err := os.Stat(familiarJSONL); !os.IsNotExist(err) {
		t.Fatalf("familiar JSONL remains after committed close: %v", err)
	}
	data, err := os.ReadFile(familiarsPath)
	if err != nil {
		t.Fatal(err)
	}
	var entries []paths.FamiliarEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("familiar registry entries = %#v, want empty after committed close", entries)
	}
}

// TestClearKillsFamiliarsRespectsProfile verifies that ClearFamiliarsJSONL
// resolves familiars.json under the profile-scoped path, not the default one.
func TestClearKillsFamiliarsRespectsProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	installFakePi(t)

	const (
		profile = "humanhorizon"
		mainSID = "humanhorizon__chat"
		famSID  = "humanhorizon__chat__expert"
		cwd     = "/Users/a/Space"
	)

	famJSONL := writeJSONLFixture(t, home, cwd, famSID, time.Now())

	// profile-scoped familiars.json
	famDir := paths.FamiliarsJSONLPath(profile, mainSID)
	if err := os.MkdirAll(filepath.Dir(famDir), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(famDir, []byte(`[{"id":"expert","sessionId":"`+famSID+`"}]`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	app := &App{
		tree:           tree.New(),
		activeSessions: map[string]struct{}{mainSID: {}, famSID: {}},
		emulatorCache: map[string]*portalis.Emulator{
			famSID: portalis.NewEmulator(famSID, "expert", "/bin/sh", nil),
		},
		profile:    profile,
		piAgentDir: filepath.Join(home, ".ai", "just", "pi"),
	}
	app.startEmulatorSyncFn = func(em *portalis.Emulator, env []string) error { return nil }

	app.clearSessionCmd(mainSID, cwd, []string{famSID})()

	if _, err := os.Stat(famJSONL); !os.IsNotExist(err) {
		t.Errorf("familiar JSONL still exists: %v", err)
	}
	data, err := os.ReadFile(famDir)
	if err != nil {
		t.Fatalf("ReadFile familiars.json: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != "[]" {
		t.Errorf("familiars.json = %q, want %q", got, "[]")
	}
}

// writeJSONLFixture drops a valid pi session JSONL file under the
// just-pi sessions directory layout so FindSessionJSONL/DeleteSessionJSONL
// can locate it. Mirrors the helper in internal/paths/sessionfile_test.go
// but is local to this package so the main test does not need cross-package
// access.
func writeJSONLFixture(t *testing.T, home, cwd, sessionID string, modTime time.Time) string {
	t.Helper()
	dir := filepath.Join(home, ".ai", "just", "pi", "sessions", paths.EncodeCwdDir(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("%d_%s.jsonl", modTime.UnixNano(), sessionID))
	header := fmt.Sprintf("{\"type\":\"session\",\"version\":3,\"id\":%q,\"cwd\":%q}\n", sessionID, cwd)
	if err := os.WriteFile(path, []byte(header), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	return path
}

// TestClearReplacesPanelEmulator verifies that after Clear the active
// ChatPanel's chatSession (and its wrapped TermPanel) point to the freshly
// started emulator — not the stopped one. Without the fix, View() would
// render the dead emulator's empty screen instead of the new pi prompt.
//
// The test injects startEmulatorSyncFn to avoid spawning a real PTY, and
// reflects into TermPanel.em to confirm the swap (the field is unexported).
func TestClearReplacesPanelEmulator(t *testing.T) {
	t.Setenv("PI_CMD", "/bin/sh")
	home := t.TempDir()
	t.Setenv("HOME", home)

	const sessionID = "humanhorizon__chat"

	// 1. Original emulator wrapped by the ChatPanel.
	origEm := portalis.NewEmulator(sessionID, "chat", "/bin/sh", nil)
	cp := ui.NewChatPanel(origEm, sessionID, "humanhorizon")

	// 2. Container in chat mode holding the ChatPanel.
	container := ui.NewContainer(nil)
	container.SetChat(cp, sessionID)

	// 3. Minimal App wired up with the container and cache.
	app := &App{
		container:      container,
		tree:           tree.New(),
		activeSessions: map[string]struct{}{sessionID: {}},
		emulatorCache:  map[string]*portalis.Emulator{sessionID: origEm},
		piAgentDir:     filepath.Join(home, ".ai", "just", "pi"),
	}
	app.startEmulatorSyncFn = func(em *portalis.Emulator, env []string) error { return nil }

	// 4. Sanity: panel holds the original emulator before Clear.
	if cp.Sessions()[0].Em() != origEm {
		t.Fatalf("precondition: cp.sessions[0].em != origEm")
	}

	// 5. Act: Clear.
	if msg := app.clearSessionCmd(sessionID, "", nil)(); msg == nil {
		t.Fatalf("clearSessionCmd returned nil")
	}

	// 6. emulatorCache holds a new emulator (different object).
	newEm := app.emulatorCache[sessionID]
	if newEm == nil {
		t.Fatal("emulatorCache[sessionID] is nil after Clear")
	}
	if newEm == origEm {
		t.Fatal("emulatorCache[sessionID] is still the original emulator (was not replaced)")
	}

	// 7. ChatPanel's chatSession now points at the new emulator.
	if got := cp.Sessions()[0].Em(); got != newEm {
		t.Fatalf("cp.sessions[0].em = %p, want new emulator %p", got, newEm)
	}

	// 8. TermPanel wrapped by chatSession points at the new emulator too.
	//    Read the unexported TermPanel.em via unsafe because reflect's
	//    Value.Interface() refuses unexported fields. We only use this in
	//    tests; the production API stays narrow.
	panel := cp.Sessions()[0].Panel()
	if panel == nil {
		t.Fatal("chatSession.Panel() returned nil")
	}
	panelVal := reflect.ValueOf(panel).Elem()
	emField := panelVal.FieldByName("em")
	if !emField.IsValid() {
		t.Fatal("TermPanel.em field not found via reflection")
	}
	panelEm := *(**portalis.Emulator)(unsafe.Pointer(emField.UnsafeAddr()))
	if panelEm != newEm {
		t.Fatalf("TermPanel.em = %p, want new emulator %p", panelEm, newEm)
	}

	// 9. View() after a ResizeMsg must not panic and must render from the
	//    new emulator (deterministic: both old and new are empty since we
	//    never spawned a PTY, but the call exercises the render path).
	cp.Update(warp.ResizeMsg{Width: 80, Height: 24})
	if out := cp.View(80, 24); out == "" {
		t.Fatal("ChatPanel.View() returned empty string after Clear+Resize")
	}
}

func TestRestoreSessionsNeverCreatesAnEmulatorForGhostActiveID(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	tr := tree.New()
	tr.Profile = "Getic"
	tr.SetProfile("Getic")
	tr.AddChat("real")
	const ghostID = "getic__ghost"
	tr.SetActiveSessionsInMemory(map[string]struct{}{ghostID: {}})

	createCalls := 0
	app := &App{
		tree:           tr,
		profile:        "Getic",
		activeSessions: map[string]struct{}{ghostID: {}},
		emulatorCache:  make(map[string]*portalis.Emulator),
		createChatEmulatorFn: func(sessionID string) *portalis.Emulator {
			createCalls++
			return portalis.NewEmulator(sessionID, sessionID, "/bin/sh", nil)
		},
	}

	if cmd := app.restoreSessions(); cmd != nil {
		t.Fatal("restore returned a start command for a ghost session")
	}
	if createCalls != 0 {
		t.Fatalf("emulator factory called %d times for ghost session", createCalls)
	}
	if len(app.emulatorCache) != 0 {
		t.Fatalf("ghost emulator was cached: %v", app.emulatorCache)
	}
	if len(app.activeSessions) != 0 || len(tr.ActiveSessionIDs()) != 0 {
		t.Fatalf("ghost active state survived restore: app=%v tree=%v", app.activeSessions, tr.ActiveSessionIDs())
	}
	if tr.LastActionError() == nil || !strings.Contains(tr.LastActionError().Error(), ghostID) {
		t.Fatalf("ghost-session warning = %v", tr.LastActionError())
	}
}

// newTestApp builds a minimal App for watcher/badge tests. It points the
// session base dir at a fresh temp HOME so the test never touches the
// user's real ~/.ai/automata. The Tree, status reader and per-session
// watcher map are initialised; everything else is left nil because the
// watcher code under test never touches warp/container/emulator.
func TestAppCloseStopsWatchersAndEmulators(t *testing.T) {
	app := newTestApp(t, "")
	it := app.tree.AllItems()[0]
	key := app.tree.SessionKeyOf(it)
	dir := filepath.Join(app.sessionBaseDir(), key)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	app.setupStatusWatcher()
	if cmds := app.syncSessionWatchers(); len(cmds) == 0 {
		t.Fatal("expected a session watcher before Close")
	}
	app.emulatorCache = map[string]*portalis.Emulator{
		key: portalis.NewEmulator(key, "chat", "/bin/sh", nil),
	}
	app.familiarEmulatorCache = map[string]*portalis.Emulator{
		key + "__expert": portalis.NewEmulator(key+"__expert", "expert", "/bin/sh", nil),
	}
	app.activeSessions = map[string]struct{}{key: {}}
	app.tree.SetActiveSessions(app.activeSessions)

	app.Close()

	if app.statusWatcher != nil {
		t.Fatal("status watcher remained after Close")
	}
	if len(app.sessionWatchers) != 0 {
		t.Fatalf("session watchers remained after Close: %d", len(app.sessionWatchers))
	}
	if len(app.emulatorCache) != 0 || len(app.familiarEmulatorCache) != 0 {
		t.Fatal("emulator caches remained after Close")
	}
	if _, ok := app.activeSessions[key]; !ok {
		t.Fatal("Close erased active session needed for restore")
	}
	found := false
	for _, activeID := range app.tree.ActiveSessionIDs() {
		if activeID == key {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("Close erased persisted tree active session needed for restore")
	}
}

func newTestApp(t *testing.T, profile string) *App {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	tr := tree.New()
	tr.Profile = profile
	tr.AddChat("agent")
	return &App{
		tree:                tr,
		profile:             profile,
		scrollbackLines:     scrollback.DefaultLines,
		piAgentDir:          filepath.Join(home, ".ai", profile, "pi"),
		activeSessions:      make(map[string]struct{}),
		statusReader:        status.NewCachedReader(profile),
		sessionWatchers:     make(map[string]*fsnotify.Watcher),
		statusSessionDirs:   make(map[string]string),
		sessionWatchPending: make(map[string]bool),
	}
}

// TestRecomputeTreeStatusBadgesReadsAction walks every chat, reads the
// matching on-disk status.json and pushes the resulting emoji map to the
// Tree. We assert the map contains the right key→glyph and that the Tree
// exposes it through StatusBadge.
func TestRecomputeTreeStatusBadgesReadsAction(t *testing.T) {
	app := newTestApp(t, "test")
	key := app.tree.SessionKeyOf(app.tree.AllItems()[0])
	sessionDir := filepath.Join(app.sessionBaseDir(), key)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "status.json"),
		[]byte(`{"action":"thinking"}`), 0o644); err != nil {
		t.Fatalf("write status: %v", err)
	}

	app.recomputeTreeStatusBadges()

	if got := app.tree.StatusBadge(app.tree.AllItems()[0]); got == "" {
		t.Fatalf("expected badge after refresh, got empty")
	}
	if got := app.tree.StatusBadge(app.tree.AllItems()[0]); got != "~" {
		t.Fatalf("expected ~ for action=thinking, got %q", got)
	}
}

// TestRecomputeTreeStatusBadgesIdleLeavesEmpty writes a status.json with
// action=idle and confirms the Tree has no badge (the Tree renders
// "○ idle" itself, so the map must omit the key to avoid a duplicate
// glyph).
func TestRecomputeTreeStatusBadgesIdleLeavesEmpty(t *testing.T) {
	app := newTestApp(t, "test")
	key := app.tree.SessionKeyOf(app.tree.AllItems()[0])
	sessionDir := filepath.Join(app.sessionBaseDir(), key)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "status.json"),
		[]byte(`{"action":"idle"}`), 0o644); err != nil {
		t.Fatalf("write status: %v", err)
	}

	app.recomputeTreeStatusBadges()

	if got := app.tree.StatusBadge(app.tree.AllItems()[0]); got != "" {
		t.Fatalf("expected empty badge for action=idle, got %q", got)
	}
}

// TestSetupStatusWatcherCreatesMissingBase proves the watcher setup
// recovers when sessionBaseDir() doesn't exist yet: it must MkdirAll the
// base and still attach a parent watcher.
func TestSetupStatusWatcherCreatesMissingBase(t *testing.T) {
	app := newTestApp(t, "")
	base := app.sessionBaseDir()
	if _, err := os.Stat(base); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be missing before setup, got err=%v", base, err)
	}

	app.setupStatusWatcher()

	if app.statusWatcher == nil {
		t.Fatal("expected statusWatcher to be created")
	}
	if _, err := os.Stat(base); err != nil {
		t.Fatalf("expected base dir to be created, got %v", err)
	}
	app.statusWatcher.Close()
}

// TestSetupStatusWatcherAttachesPerSession creates two chat items, points
// the Tree at one of them, and confirms setupStatusWatcher opens a parent
// watcher plus one watcher per existing session directory.
func TestSetupStatusWatcherAttachesPerSession(t *testing.T) {
	app := newTestApp(t, "")
	keys := make([]string, 0, 2)
	for _, it := range app.tree.AllItems() {
		if it.IsFolder || it.IsTerminal {
			continue
		}
		k := app.tree.SessionKeyOf(it)
		dir := filepath.Join(app.sessionBaseDir(), k)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		t.Fatal("test setup: no chat items were created")
	}

	for _, key := range keys {
		app.activeSessions[key] = struct{}{}
	}
	app.setupStatusWatcher()
	_ = app.syncSessionWatchers()
	t.Cleanup(func() {
		app.statusWatcher.Close()
		for _, w := range app.sessionWatchers {
			w.Close()
		}
	})

	if app.statusWatcher == nil {
		t.Fatal("expected parent statusWatcher to be created")
	}
	for _, k := range keys {
		if _, ok := app.sessionWatchers[k]; !ok {
			t.Errorf("missing sessionWatcher for %q (have %d watchers)",
				k, len(app.sessionWatchers))
		}
	}
}

// TestSyncSessionWatchersPrunesHidden removes a chat from the Tree and
// confirms the matching per-session watcher is closed and dropped from
// the map, so file descriptors don't leak.
func TestSyncSessionWatchersPrunesHidden(t *testing.T) {
	app := newTestApp(t, "")
	it := app.tree.AllItems()[0]
	key := app.tree.SessionKeyOf(it)
	dir := filepath.Join(app.sessionBaseDir(), key)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	app.activeSessions[key] = struct{}{}
	app.setupStatusWatcher()
	_ = app.syncSessionWatchers()
	t.Cleanup(func() {
		app.statusWatcher.Close()
		for _, w := range app.sessionWatchers {
			w.Close()
		}
	})
	if _, ok := app.sessionWatchers[key]; !ok {
		t.Fatal("expected watcher after setup")
	}

	// Hide the chat by clearing the tree, then re-sync.
	app.tree.Reset()

	_ = app.syncSessionWatchers()

	if _, ok := app.sessionWatchers[key]; ok {
		t.Errorf("expected watcher for %q to be pruned, still present", key)
	}
}

// TestWatchTreeStatusCmdNilWithoutWatcher asserts the blocking cmd is
// inert when no watcher is mounted, so Update never schedules a goroutine
// that would deadlock on a closed channel.
func TestWatchTreeStatusCmdNilWithoutWatcher(t *testing.T) {
	app := newTestApp(t, "")
	if cmd := app.watchTreeStatusCmd(); cmd != nil {
		t.Errorf("expected nil cmd without watcher, got %T", cmd)
	}
}

// TestTreeStatusChangedMsgTriggersRefresh pushes treeStatusChangedMsg into
// Update and confirms a fresh recompute happened (badge appears) plus a
// re-arm cmd is returned. The recompute works against an on-disk
// status.json written before the message fires.
func TestChatAndFamiliarEmulatorsReceiveConfiguredScrollback(t *testing.T) {
	installFakePi(t)
	app := newTestApp(t, "scrollback-profile")
	app.scrollbackLines = 0

	chatID := app.tree.SessionKeyOf(app.tree.AllItems()[0])
	chat := app.createChatEmulator(chatID)
	if chat == nil {
		t.Fatal("chat emulator was not created")
	}
	if got := int(reflect.ValueOf(chat).Elem().FieldByName("scrollbackLimit").Int()); got != 0 {
		t.Fatalf("chat emulator scrollback limit = %d, want 0", got)
	}

	familiar, _ := app.createFamiliarEmulator("scrollback-profile__familiar")
	if familiar == nil {
		t.Fatal("familiar emulator was not created")
	}
	if got := int(reflect.ValueOf(familiar).Elem().FieldByName("scrollbackLimit").Int()); got != 0 {
		t.Fatalf("familiar emulator scrollback limit = %d, want 0", got)
	}
}

func TestReadyMessageAppliesUnlimitedScrollbackToCreatedScreen(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	app := newTestApp(t, "scrollback-profile")
	t.Cleanup(app.Close)
	app.scrollbackLines = 0
	item := app.tree.AllItems()[0]
	sessionID := app.tree.SessionKeyOf(item)
	emulator := portalis.NewEmulator(sessionID, item.Name, "/bin/cat", nil)
	emulator.SetScrollbackLimit(0)
	screen := portalis.NewScreen(24, 80)
	emulatorValue := reflect.ValueOf(emulator).Elem()
	screenField := emulatorValue.FieldByName("screen")
	reflect.NewAt(screenField.Type(), unsafe.Pointer(screenField.UnsafeAddr())).Elem().Set(reflect.ValueOf(screen))
	app.emulatorCache = map[string]*portalis.Emulator{sessionID: emulator}

	_, handled := app.routeCachedEmulatorMessage(portalis.PtyReadyMsg{SessionID: sessionID})
	if !handled {
		t.Fatal("ready message was not routed to cached emulator")
	}
	limitField := reflect.ValueOf(screen).Elem().FieldByName("scrollbackLimit")
	if got := reflect.NewAt(limitField.Type(), unsafe.Pointer(limitField.UnsafeAddr())).Elem().Int(); got != 0 {
		t.Fatalf("started screen scrollback limit = %d, want unlimited (0)", got)
	}
}

func TestConfigureDebugLogDisabledWithoutExplicitPath(t *testing.T) {
	var output bytes.Buffer
	logger := log.New(&output, "", 0)
	file, err := configureDebugLog("", logger)
	if err != nil {
		t.Fatal(err)
	}
	if file != nil {
		t.Fatal("disabled debug log returned an open file")
	}
	logger.Print("must not be logged by default")
	if output.Len() != 0 {
		t.Fatalf("default debug output = %q, want no output", output.String())
	}
}

func TestConfigureDebugLogReportsOpenFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "debug.log")
	if _, err := configureDebugLog(path, log.New(io.Discard, "", 0)); err == nil {
		t.Fatal("explicit debug log open failure was ignored")
	}
}

func TestWriteHeapProfileProducesGzipProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "heap.prof")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeHeapProfile(file); err != nil {
		t.Fatalf("write heap profile: %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(contents))
	if err != nil {
		t.Fatalf("heap profile is not gzip: %v", err)
	}
	profile, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read heap profile: %v", err)
	}
	if len(profile) == 0 {
		t.Fatal("heap profile is empty")
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenDebugLogAppendsWithoutTruncating(t *testing.T) {
	path := filepath.Join(t.TempDir(), "automata.log")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := openDebugLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("after\n"); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "before\nafter\n" {
		t.Fatalf("debug log contents = %q, want preserved append", contents)
	}
}

func TestStatusWatcherRecoveryRecreatesOneSharedWatcher(t *testing.T) {
	app := newTestApp(t, "")
	key := app.tree.SessionKeyOf(app.tree.AllItems()[0])
	dir := filepath.Join(app.sessionBaseDir(), key)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	app.activeSessions[key] = struct{}{}
	app.setupStatusWatcher()
	cmds := app.syncSessionWatchers()
	oldWatcher := app.statusWatcher
	oldGeneration := app.statusGeneration
	if oldWatcher == nil || app.sessionWatchers[key] != oldWatcher || len(cmds) != 1 {
		t.Fatal("test setup did not mount exactly one shared watcher")
	}
	t.Cleanup(func() { app.Close() })

	_, cmd := app.Update(statusWatcherErrorMsg{
		generation: oldGeneration,
		watcher:    oldWatcher,
		err:        errors.New("synthetic watcher failure"),
	})
	if cmd == nil {
		t.Fatal("watcher recovery did not return a re-arm command")
	}
	if app.statusWatcher == nil || app.statusWatcher == oldWatcher {
		t.Fatal("status watcher was not recreated")
	}
	if app.sessionWatchers[key] != app.statusWatcher {
		t.Fatal("session index does not point to the recreated shared watcher")
	}
	if app.statusGeneration == oldGeneration {
		t.Fatal("watcher generation was not advanced")
	}
}

func TestStatusWatcherRejectsStaleEvents(t *testing.T) {
	app := newTestApp(t, "")
	app.setupStatusWatcher()
	oldWatcher, oldGeneration := app.statusWatcher, app.statusGeneration
	app.setupStatusWatcher()
	currentWatcher, currentGeneration := app.statusWatcher, app.statusGeneration
	t.Cleanup(func() { app.Close() })

	_, cmd := app.Update(treeStatusChangedMsg{
		generation: oldGeneration,
		watcher:    oldWatcher,
		path:       filepath.Join(app.sessionBaseDir(), "stale", "status.json"),
	})
	if cmd != nil {
		t.Fatal("stale watcher event unexpectedly scheduled work")
	}
	if app.statusWatcher != currentWatcher || app.statusGeneration != currentGeneration {
		t.Fatal("stale watcher event changed the current watcher")
	}
}

func TestTreeStatusChangedMsgTriggersRefresh(t *testing.T) {
	app := newTestApp(t, "")
	key := app.tree.SessionKeyOf(app.tree.AllItems()[0])
	dir := filepath.Join(app.sessionBaseDir(), key)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "status.json"),
		[]byte(`{"action":"thinking"}`), 0o644); err != nil {
		t.Fatalf("write status: %v", err)
	}

	// Mount the shared watcher and active session directory index.
	app.activeSessions[key] = struct{}{}
	app.setupStatusWatcher()
	app.syncSessionWatchers()
	t.Cleanup(func() { app.Close() })

	_, cmd := app.Update(treeStatusChangedMsg{
		generation: app.statusGeneration,
		watcher:    app.statusWatcher,
		path:       filepath.Join(dir, "status.json"),
	})

	if got := app.tree.StatusBadge(app.tree.AllItems()[0]); got == "" {
		t.Errorf("expected badge to be recomputed, got empty")
	}
	if cmd == nil {
		t.Errorf("expected re-arm cmd, got nil")
	}
	if !app.statusWatchPending {
		t.Errorf("expected statusWatchPending=true after re-arm")
	}
}

// TestWatchSessionCmdNilWithoutWatcher asserts watchSessionCmd is inert
// when the requested key has no mounted watcher, so Update never schedules
// a goroutine that would deadlock on a closed channel.
func TestWatchSessionCmdNilWithoutWatcher(t *testing.T) {
	app := newTestApp(t, "")
	if cmd := app.watchSessionCmd("missing"); cmd != nil {
		t.Errorf("expected nil cmd for missing watcher, got %T", cmd)
	}
}

// TestSyncSessionWatchersReturnsCmdsOnlyForNewWatchers ensures the cmd
// chain is started exactly once per per-session watcher. On the first
// syncSessionWatchers, all watchers are new and produce cmds; on a second
// call with no Tree changes, no new cmds are produced.
func TestSyncSessionWatchersReturnsCmdsOnlyForNewWatchers(t *testing.T) {
	app := newTestApp(t, "")
	for _, it := range app.tree.AllItems() {
		if it.IsFolder || it.IsTerminal {
			continue
		}
		key := app.tree.SessionKeyOf(it)
		app.activeSessions[key] = struct{}{}
		dir := filepath.Join(app.sessionBaseDir(), key)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}

	app.setupStatusWatcher()
	t.Cleanup(func() {
		app.statusWatcher.Close()
		for _, w := range app.sessionWatchers {
			w.Close()
		}
	})

	first := app.syncSessionWatchers()
	if len(first) == 0 {
		t.Fatal("expected first syncSessionWatchers to produce cmds for newly attached watchers")
	}

	second := app.syncSessionWatchers()
	if len(second) != 0 {
		t.Errorf("expected no new cmds on second sync (no Tree changes), got %d", len(second))
	}
}

func waitForWatcherMessage(t *testing.T, cmd tea.Cmd, trigger func() error) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("watcher command is nil")
	}
	messages := make(chan tea.Msg, 1)
	go func() { messages <- cmd() }()
	if err := trigger(); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-messages:
		return msg
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for status watcher event")
		return nil
	}
}

func TestStatusWatcherUpdatesOnlyChangedBadgeAndFiltersFiles(t *testing.T) {
	app := newTestApp(t, "test")
	app.tree.AddChat("second")
	items := app.tree.AllItems()
	if len(items) != 2 {
		t.Fatalf("chat items = %d, want 2", len(items))
	}
	keys := make(map[string]string, 2)
	for _, item := range items {
		key := app.tree.SessionKeyOf(item)
		dir := filepath.Join(app.sessionBaseDir(), key)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		action := "read"
		if item.Name == "second" {
			action = "write"
		}
		if err := os.WriteFile(filepath.Join(dir, "status.json"), []byte(`{"action":"`+action+`"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		keys[item.Name] = key
	}
	app.recomputeTreeStatusBadges()
	app.activeSessions[keys["agent"]] = struct{}{}
	app.setupStatusWatcher()
	cmds := app.syncSessionWatchers()
	if len(cmds) != 1 || app.sessionWatchers[keys["agent"]] != app.statusWatcher {
		t.Fatal("active session directory did not mount on the shared watcher")
	}
	if _, watched := app.sessionWatchers[keys["second"]]; watched {
		t.Fatal("inactive session unexpectedly consumed a status watch")
	}
	t.Cleanup(func() { app.Close() })

	changedPath := filepath.Join(app.sessionBaseDir(), keys["agent"], "status.json")
	msg := waitForWatcherMessage(t, cmds[0], func() error {
		return os.WriteFile(changedPath, []byte(`{"action":"thinking"}`), 0o644)
	})
	statusChange, ok := msg.(treeStatusChangedMsg)
	if !ok {
		t.Fatalf("watcher message = %T, want treeStatusChangedMsg", msg)
	}
	if filepath.Clean(statusChange.path) != filepath.Clean(changedPath) {
		t.Fatalf("event path = %q, want %q", statusChange.path, changedPath)
	}
	_, rearm := app.Update(statusChange)
	for _, item := range items {
		want := "~"
		if item.Name == "second" {
			want = "W"
		}
		if got := app.tree.StatusBadge(item); got != want {
			t.Errorf("%s badge = %q, want %q", item.Name, got, want)
		}
	}

	unrelatedPath := filepath.Join(filepath.Dir(changedPath), "unrelated.txt")
	unrelated := waitForWatcherMessage(t, rearm, func() error {
		return os.WriteFile(unrelatedPath, []byte("ignored"), 0o644)
	})
	unrelatedChange, ok := unrelated.(treeStatusChangedMsg)
	if !ok {
		t.Fatalf("unrelated event message = %T, want treeStatusChangedMsg", unrelated)
	}
	_, nextReader := app.Update(unrelatedChange)
	if nextReader == nil {
		t.Fatal("unrelated event did not re-arm the sole watcher")
	}
	for _, item := range items {
		want := "~"
		if item.Name == "second" {
			want = "W"
		}
		if got := app.tree.StatusBadge(item); got != want {
			t.Errorf("unrelated file changed %s badge to %q, want %q", item.Name, got, want)
		}
	}
}

func TestStatusWatcherMountsActiveSessionDirectory(t *testing.T) {
	app := newTestApp(t, "test")
	item := app.tree.AllItems()[0]
	key := app.tree.SessionKeyOf(item)
	app.activeSessions[key] = struct{}{}
	app.setupStatusWatcher()
	cmds := app.syncSessionWatchers()
	if len(cmds) != 1 {
		t.Fatalf("initial watcher commands = %d, want 1", len(cmds))
	}
	t.Cleanup(func() { app.Close() })

	dir := filepath.Join(app.sessionBaseDir(), key)
	if app.sessionWatchers[key] != app.statusWatcher {
		t.Fatal("active session directory was not attached to the shared watcher")
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("active session directory was not materialized: %v", err)
	}

	msg := waitForWatcherMessage(t, cmds[0], func() error {
		return os.WriteFile(filepath.Join(dir, "status.json"), []byte(`{"action":"grep"}`), 0o644)
	})
	event, ok := msg.(treeStatusChangedMsg)
	if !ok {
		t.Fatalf("status event message = %T, want treeStatusChangedMsg", msg)
	}
	_, _ = app.Update(event)
	if got := app.tree.StatusBadge(item); got != "G" {
		t.Fatalf("active session badge = %q, want G", got)
	}
}

// TestSharedStatusWatcherReceivesSessionEvents verifies status.json events
// from a mounted session directory reach the single shared watcher.
func TestSharedStatusWatcherReceivesSessionEvents(t *testing.T) {
	app := newTestApp(t, "")
	it := app.tree.AllItems()[0]
	key := app.tree.SessionKeyOf(it)
	dir := filepath.Join(app.sessionBaseDir(), key)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "status.json"),
		[]byte(`{"action":"thinking"}`), 0o644); err != nil {
		t.Fatalf("write status: %v", err)
	}

	app.activeSessions[key] = struct{}{}
	app.setupStatusWatcher()
	watchCmds := app.syncSessionWatchers()
	if len(watchCmds) == 0 {
		t.Fatal("expected a session watcher command")
	}
	t.Cleanup(func() { app.Close() })

	msg := waitForWatcherMessage(t, watchCmds[0], func() error {
		return os.WriteFile(filepath.Join(dir, "status.json"),
			[]byte(`{"action":"write"}`), 0o644)
	})
	if _, ok := msg.(treeStatusChangedMsg); !ok {
		t.Errorf("expected treeStatusChangedMsg, got %T", msg)
	}
}

// TestSharedStatusWatcherRearmsAfterEvent verifies Update re-arms the one
// blocking reader after each event, so later status writes remain observable.
func TestSharedStatusWatcherRearmsAfterEvent(t *testing.T) {
	app := newTestApp(t, "")
	item := app.tree.AllItems()[0]
	key := app.tree.SessionKeyOf(item)
	dir := filepath.Join(app.sessionBaseDir(), key)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	statusPath := filepath.Join(dir, "status.json")
	if err := os.WriteFile(statusPath, []byte(`{"action":"thinking"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	app.activeSessions[key] = struct{}{}
	app.setupStatusWatcher()
	cmds := app.syncSessionWatchers()
	if len(cmds) != 1 {
		t.Fatalf("initial watcher commands = %d, want 1", len(cmds))
	}
	t.Cleanup(func() { app.Close() })

	first := waitForWatcherMessage(t, cmds[0], func() error {
		return os.WriteFile(statusPath, []byte(`{"action":"write"}`), 0o644)
	})
	firstEvent, ok := first.(treeStatusChangedMsg)
	if !ok {
		t.Fatalf("first message = %T, want treeStatusChangedMsg", first)
	}
	_, rearm := app.Update(firstEvent)

	second := waitForWatcherMessage(t, rearm, func() error {
		return os.WriteFile(statusPath, []byte(`{"action":"grep"}`), 0o644)
	})
	secondEvent, ok := second.(treeStatusChangedMsg)
	if !ok {
		t.Fatalf("second message = %T, want treeStatusChangedMsg", second)
	}
	_, nextReader := app.Update(secondEvent)
	if nextReader == nil {
		t.Fatal("second event did not re-arm the sole watcher")
	}
}

func TestStatusWatcherTracksOnlyActiveChats(t *testing.T) {
	app := newTestApp(t, "active-watch-scope")
	for i := 0; i < 20; i++ {
		app.tree.AddChat(fmt.Sprintf("inactive-%d", i))
	}
	active := app.tree.SessionKeyOf(app.tree.AllItems()[0])
	app.activeSessions[active] = struct{}{}
	app.setupStatusWatcher()
	_ = app.syncSessionWatchers()
	defer app.Close()

	if len(app.sessionWatchers) != 1 {
		t.Fatalf("status watch count = %d, want 1 active chat", len(app.sessionWatchers))
	}
	if app.sessionWatchers[active] != app.statusWatcher {
		t.Fatal("active chat is not mounted on the shared watcher")
	}
}

func TestMetadataSaveCoalescesRapidChangesIntoOneTimer(t *testing.T) {
	app := newTestApp(t, "metadata-coalesce")
	item := app.tree.AllItems()[0]
	writes := 0
	app.tree.SetSaveStateFunc(func() error {
		writes++
		if item.CWD != "/work/99" {
			t.Errorf("saved CWD = %q, want final value", item.CWD)
		}
		return nil
	})

	item.CWD = "/work/0"
	first := app.scheduleMetadataSave()
	if first == nil {
		t.Fatal("first metadata change did not schedule a timer")
	}
	for index := 1; index < 100; index++ {
		item.CWD = fmt.Sprintf("/work/%d", index)
		if cmd := app.scheduleMetadataSave(); cmd != nil {
			t.Fatalf("metadata change %d scheduled a duplicate timer", index)
		}
	}
	if writes != 0 || !app.metadataSaveDirty || !app.metadataSavePending {
		t.Fatalf("metadata changed before timer: writes=%d dirty=%t pending=%t", writes, app.metadataSaveDirty, app.metadataSavePending)
	}

	raw := first()
	saveMessage, ok := raw.(treeMetadataSaveMsg)
	if !ok {
		t.Fatalf("metadata timer message = %T, want treeMetadataSaveMsg", raw)
	}
	_, _ = app.Update(saveMessage)
	if writes != 1 || app.metadataSaveDirty || app.metadataSavePending {
		t.Fatalf("coalesced save = writes:%d dirty:%t pending:%t, want one write and clean state", writes, app.metadataSaveDirty, app.metadataSavePending)
	}
	_, _ = app.Update(saveMessage)
	if writes != 1 {
		t.Fatalf("duplicate timer wrote state again: writes=%d", writes)
	}
}

func TestEmulatorMetadataCallbacksScheduleDeferredSaves(t *testing.T) {
	piDir := installFakePi(t)
	t.Setenv("PATH", piDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	app := newTestApp(t, "metadata-callbacks")
	item := app.tree.AllItems()[0]
	em := app.newChatEmulator(app.tree.SessionKeyOf(item))
	if em == nil {
		t.Fatal("expected fake Pi emulator")
	}
	writes := 0
	app.tree.SetSaveStateFunc(func() error {
		writes++
		return nil
	})
	t.Cleanup(func() { app.Close() })

	em.OnCWDChange("/deferred/cwd")
	em.OnCommandHistoryChanged([]string{"echo deferred"})
	if item.CWD != "/deferred/cwd" || !reflect.DeepEqual(item.CommandHistory, []string{"echo deferred"}) {
		t.Fatalf("metadata callbacks did not update Tree item: cwd=%q history=%v", item.CWD, item.CommandHistory)
	}
	if writes != 0 || !app.metadataSaveDirty || !app.metadataSavePending || len(app.pendingBubbleTeaCmds) != 1 {
		t.Fatalf("callbacks wrote synchronously or failed to coalesce saves: writes=%d dirty=%t pending=%t commands=%d", writes, app.metadataSaveDirty, app.metadataSavePending, len(app.pendingBubbleTeaCmds))
	}
}

func TestPlanWidthChangeUsesDeferredMetadataSave(t *testing.T) {
	app := newTestApp(t, "plan-width-deferred")
	writes := 0
	app.tree.SetSaveStateFunc(func() error {
		writes++
		return nil
	})

	app.handlePlanWidthChange(57)
	if got := app.tree.PlanWidth(); got != 57 {
		t.Fatalf("plan width = %d, want 57", got)
	}
	if writes != 0 {
		t.Fatalf("plan width persisted synchronously: writes=%d", writes)
	}
	if !app.metadataSaveDirty || !app.metadataSavePending || len(app.pendingBubbleTeaCmds) != 1 {
		t.Fatalf("plan width did not queue one deferred save: dirty=%t pending=%t commands=%d", app.metadataSaveDirty, app.metadataSavePending, len(app.pendingBubbleTeaCmds))
	}

	cmd := app.pendingBubbleTeaCmds[0]
	app.pendingBubbleTeaCmds = nil
	raw := cmd()
	msg, ok := raw.(treeMetadataSaveMsg)
	if !ok {
		t.Fatalf("plan width timer returned %T, want treeMetadataSaveMsg", raw)
	}
	_, _ = app.Update(msg)
	if writes != 1 || app.metadataSaveDirty || app.metadataSavePending {
		t.Fatalf("deferred plan width save = writes:%d dirty:%t pending:%t", writes, app.metadataSaveDirty, app.metadataSavePending)
	}
}

func TestMetadataSaveFailureRemainsDirtyUntilRetry(t *testing.T) {
	app := newTestApp(t, "metadata-retry")
	attempts := 0
	app.tree.SetSaveStateFunc(func() error {
		attempts++
		if attempts == 1 {
			return errors.New("injected metadata save failure")
		}
		return nil
	})

	firstTimer := app.scheduleMetadataSave()
	firstMessage, ok := firstTimer().(treeMetadataSaveMsg)
	if !ok {
		t.Fatal("first timer did not return treeMetadataSaveMsg")
	}
	_, _ = app.Update(firstMessage)
	if !app.metadataSaveDirty || app.tree.LastActionError() == nil {
		t.Fatal("failed metadata save did not retain dirty state and visible warning")
	}

	secondTimer := app.scheduleMetadataSave()
	secondMessage, ok := secondTimer().(treeMetadataSaveMsg)
	if !ok {
		t.Fatal("retry timer did not return treeMetadataSaveMsg")
	}
	_, _ = app.Update(secondMessage)
	if attempts != 2 || app.metadataSaveDirty {
		t.Fatalf("retry attempts=%d dirty=%t, want two attempts and clean state", attempts, app.metadataSaveDirty)
	}
}

func TestAppCloseFlushesLatestTreeMetadata(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	app := newTestApp(t, "metadata-flush")
	item := app.tree.AllItems()[0]
	item.CWD = "/final/working-directory"
	item.CommandHistory = []string{"first", "latest"}
	app.metadataSaveDirty = true

	app.Close()

	data, err := os.ReadFile(paths.StatePath("metadata-flush"))
	if err != nil {
		t.Fatalf("read persisted Tree state: %v", err)
	}
	var state tree.TreeState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("decode persisted Tree state: %v", err)
	}
	if len(state.Items) != 1 || state.Items[0].CWD != item.CWD {
		t.Fatalf("persisted Tree state = %+v, want latest CWD %q", state.Items, item.CWD)
	}
	if got := state.Items[0].CommandHistory; !reflect.DeepEqual(got, item.CommandHistory) {
		t.Fatalf("persisted command history = %v, want %v", got, item.CommandHistory)
	}
	if app.metadataSaveDirty {
		t.Fatal("Close left metadata dirty after successful flush")
	}
}

// TestClearSessionCmdRefusesWithoutPiAgentDir is the safety net: a Clear
// with an empty piAgentDir must NOT touch any JSONL. After this fix Clear
// refuses by design — it's the only way to guarantee one profile cannot
// delete another profile's sessions.
func TestClearSessionCmdRefusesWithoutPiAgentDir(t *testing.T) {
	app := newTestApp(t, "test")
	app.piAgentDir = "" // simulate the legacy / unsafe state explicitly

	it := app.tree.AllItems()[0]
	sessionID := app.tree.SessionKeyOf(it)
	cwd := "/Users/a/Space"

	// Write a JSONL inside the agent dir the new fixture picked. With
	// piAgentDir cleared, DeleteSessionJSONL would otherwise have a shot at it.
	agentDir := filepath.Join(t.TempDir(), "agent")
	dir := filepath.Join(agentDir, "sessions", paths.EncodeCwdDir(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	target := filepath.Join(dir, "must_remain.jsonl")
	if err := os.WriteFile(target, []byte(`{"id":"x"}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	msg := app.clearSessionCmd(sessionID, cwd, nil)()
	clearErr, ok := msg.(clearSessionErrorMsg)
	if !ok || clearErr.err == nil || !strings.Contains(clearErr.err.Error(), "without piAgentDir") {
		t.Errorf("clearSessionCmd with empty piAgentDir = %#v, want visible fail-closed error", msg)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("target JSONL must be untouched, stat err: %v", err)
	}
}

func TestAppSessionBaseDirUsesCanonicalPaths(t *testing.T) {
	dataHome := t.TempDir()
	home := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	t.Setenv("HOME", home)

	tests := []struct {
		name    string
		profile string
		want    string
	}{
		{
			name:    "default profile",
			profile: "",
			want:    filepath.Join(dataHome, "profiles", "default", "sessions"),
		},
		{
			name:    "unicode profile slug",
			profile: "Проект Ω",
			want:    filepath.Join(dataHome, "profiles", "proekt-ω", "sessions"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := &App{profile: test.profile}
			if got := app.sessionBaseDir(); got != test.want {
				t.Fatalf("sessionBaseDir() = %q, want %q", got, test.want)
			}
		})
	}

	if _, err := os.Stat(filepath.Join(dataHome, "sessions")); !os.IsNotExist(err) {
		t.Fatalf("legacy sessions root must not be used, stat err: %v", err)
	}
}
