package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/HumanHorizon/automata/internal/paths"
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
		"AUTOMATA_PROFILE=default",
	} {
		if !containsString(gotEnv, wantEnv) {
			t.Fatalf("start environment = %#v, missing %q", gotEnv, wantEnv)
		}
	}
	if len(gotEnv) != 2 {
		t.Fatalf("start environment = %#v, want exactly both profile variables", gotEnv)
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

	t.Log("И: env содержит PI_CODING_AGENT_DIR=getic и AUTOMATA_PROFILE=getic")
	want := map[string]bool{
		"PI_CODING_AGENT_DIR=" + geticDir: false,
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
	if !containsString(env, "AUTOMATA_PROFILE=keller") {
		t.Fatalf("PI_CMD environment = %#v, want canonical profile", env)
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
			app := &App{profile: test.profile}
			cmd, _, env := app.piLaunch("chat")
			if cmd != "/bin/bash" {
				t.Fatalf("cmd = %q, want PI_CMD /bin/bash", cmd)
			}
			wantEnv := "AUTOMATA_PROFILE=" + test.want
			count := 0
			for _, item := range env {
				if strings.HasPrefix(item, "AUTOMATA_PROFILE=") {
					count++
					if item != wantEnv {
						t.Fatalf("profile environment = %q, want %q", item, wantEnv)
					}
				}
			}
			if count != 1 {
				t.Fatalf("AUTOMATA_PROFILE entries = %d in %#v, want exactly one", count, env)
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

	if msg := app.clearSessionCmd(sessionID, "", nil)(); msg != nil {
		t.Fatalf("failed restart returned %T, want nil", msg)
	}
	if _, ok := app.emulatorCache[sessionID]; ok {
		t.Fatal("failed restart remained in emulator cache")
	}
	if _, ok := app.activeSessions[sessionID]; ok {
		t.Fatal("failed restart remained active")
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

func TestKnowledgeRefreshDoesNotForkBlinkChain(t *testing.T) {
	t.Log("Given the existing blink chain already scheduled the next tick")
	app := &App{container: &ui.Container{}}

	t.Log("When the asynchronous knowledge refresh result arrives")
	_, cmd := app.Update(ui.KnowledgeRefreshMsg{})

	t.Log("Then it applies data without scheduling another independent blink chain")
	if cmd != nil {
		t.Fatal("KnowledgeRefreshMsg returned a command; this forks the blink timer chain and makes idle CPU grow over time")
	}
}

func TestWindowResizeDoesNotForkBlinkChain(t *testing.T) {
	t.Log("Given a minimal app whose Warp layout has no resize command")
	w := warp.New()
	w.SetRoot(nil)
	app := &App{
		warp: w,
		tree: tree.New(),
	}

	t.Log("When Bubble Tea reports a zero-sized startup resize")
	_, cmd := app.Update(tea.WindowSizeMsg{})

	t.Log("Then resize does not schedule an independent blink chain")
	if cmd != nil {
		t.Fatal("WindowSizeMsg returned a command despite Warp returning nil; the extra command is a leaked blink timer")
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
		tree:            tr,
		profile:         profile,
		piAgentDir:      filepath.Join(home, ".ai", profile, "pi"),
		statusReader:    status.NewCachedReader(profile),
		sessionWatchers: make(map[string]*fsnotify.Watcher),
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

func TestWatcherRecoveryRecreatesClosedStatusAndSessionWatchers(t *testing.T) {
	app := newTestApp(t, "")
	key := app.tree.SessionKeyOf(app.tree.AllItems()[0])
	dir := filepath.Join(app.sessionBaseDir(), key)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	app.setupStatusWatcher()
	app.syncSessionWatchers()
	oldStatus := app.statusWatcher
	oldSession := app.sessionWatchers[key]
	if oldStatus == nil || oldSession == nil {
		t.Fatal("test setup did not create both status watchers")
	}
	t.Cleanup(func() { app.Close() })

	statusCmd := app.watchTreeStatusCmd()
	if statusCmd == nil {
		t.Fatal("status watcher command is nil")
	}
	statusMessages := make(chan tea.Msg, 1)
	go func() { statusMessages <- statusCmd() }()
	go func() { oldStatus.Errors <- errors.New("synthetic status watcher error") }()
	select {
	case msg := <-statusMessages:
		errorMsg, ok := msg.(statusWatcherErrorMsg)
		if !ok || errorMsg.err == nil {
			t.Fatalf("status watcher error message = %#v", msg)
		}
		_, cmd := app.Update(msg)
		if cmd == nil {
			t.Fatal("status watcher recovery did not return a re-arm command")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("closed status watcher did not report recovery")
	}
	if app.statusWatcher == nil || app.statusWatcher == oldStatus {
		t.Fatal("status watcher was not recreated")
	}

	app.sessionWatchPending[key] = false
	sessionCmd := app.watchSessionCmd(key)
	if sessionCmd == nil {
		t.Fatal("session watcher command is nil")
	}
	sessionMessages := make(chan tea.Msg, 1)
	go func() { sessionMessages <- sessionCmd() }()
	go func() { oldSession.Errors <- errors.New("synthetic session watcher error") }()
	select {
	case msg := <-sessionMessages:
		errorMsg, ok := msg.(sessionWatcherErrorMsg)
		if !ok || errorMsg.err == nil {
			t.Fatalf("session watcher error message = %#v", msg)
		}
		_, cmd := app.Update(msg)
		if cmd == nil {
			t.Fatal("session watcher recovery did not return a re-arm command")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("closed session watcher did not report recovery")
	}
	if app.sessionWatchers[key] == nil || app.sessionWatchers[key] == oldSession {
		t.Fatal("session watcher was not recreated")
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

	// Mount the watcher (skipping Init's full wiring) so watchTreeStatusCmd
	// has something to return.
	app.setupStatusWatcher()
	t.Cleanup(func() {
		app.statusWatcher.Close()
		for _, w := range app.sessionWatchers {
			w.Close()
		}
	})
	app.statusWatchPending = false // allow the first re-arm

	_, cmd := app.Update(treeStatusChangedMsg{})

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
		dir := filepath.Join(app.sessionBaseDir(), app.tree.SessionKeyOf(it))
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

// TestPerSessionWatcherCmdChainReceivesEvents writes status.json inside a
// session directory and asserts that watchSessionCmd for that key returns
// treeStatusChangedMsg within a short timeout. This is the regression
// test for the bug where per-session watchers were mounted but never
// had a goroutine reading their Events channel.
func TestPerSessionWatcherCmdChainReceivesEvents(t *testing.T) {
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

	app.setupStatusWatcher()
	watchCmds := app.syncSessionWatchers()
	if len(watchCmds) == 0 {
		t.Fatal("expected a session watcher command")
	}
	t.Cleanup(func() {
		app.statusWatcher.Close()
		for _, w := range app.sessionWatchers {
			w.Close()
		}
	})

	cmd := watchCmds[0]
	if cmd == nil {
		t.Fatal("expected cmd for mounted watcher")
	}

	done := make(chan tea.Msg, 1)
	go func() {
		done <- cmd()
	}()

	// Trigger an event in a separate goroutine to give FSEvents/kqueue
	// time to deliver the first read.
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = os.WriteFile(filepath.Join(dir, "status.json"),
			[]byte(`{"action":"write"}`), 0o644)
	}()

	select {
	case msg := <-done:
		if _, ok := msg.(treeStatusChangedMsg); !ok {
			t.Errorf("expected treeStatusChangedMsg, got %T", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for per-session watcher event")
	}
}

// TestPerSessionWatcherRearmsAfterEvent verifies the cmd-chain keeps
// firing after the first event. Without re-arming (see rearmSessionWatchers
// in main.go) the blocking read is one-shot: subsequent writes to status.json
// would be silently lost and the Tree badge would freeze.
func TestPerSessionWatcherRearmsAfterEvent(t *testing.T) {
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

	app.setupStatusWatcher()
	watchCmds := app.syncSessionWatchers()
	if len(watchCmds) == 0 {
		t.Fatal("expected a session watcher command")
	}
	t.Cleanup(func() {
		app.statusWatcher.Close()
		for _, w := range app.sessionWatchers {
			w.Close()
		}
	})

	// Drain the first event to confirm the chain works once.
	first := watchCmds[0]
	if first == nil {
		t.Fatal("expected first cmd")
	}
	firstDone := make(chan tea.Msg, 1)
	go func() { firstDone <- first() }()

	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(dir, "status.json"),
		[]byte(`{"action":"write"}`), 0o644); err != nil {
		t.Fatalf("write status (1st trigger): %v", err)
	}
	select {
	case msg := <-firstDone:
		if _, ok := msg.(treeStatusChangedMsg); !ok {
			t.Fatalf("expected treeStatusChangedMsg from first event, got %T", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first event did not arrive")
	}

	// Now simulate what Update does: take the re-arm cmd and run it.
	// The Update path returns tea.Batch(allCmds...), but rearmSessionWatchers
	// is the relevant slice; if any of its entries fires on a fresh event,
	// the chain is correctly re-armed.
	app.sessionWatchPending[key] = false
	rearm := app.rearmSessionWatchers()
	if len(rearm) == 0 {
		t.Fatal("expected rearmSessionWatchers to return at least one cmd")
	}
	for _, c := range rearm {
		if c == nil {
			t.Fatal("expected non-nil re-arm cmd")
		}
	}

	// Pick the cmd for our specific key and verify it can fire on a new event.
	target := rearm[0]
	if target == nil {
		t.Fatal("expected re-arm cmd for target key")
	}
	secondDone := make(chan tea.Msg, 1)
	go func() { secondDone <- target() }()

	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(dir, "status.json"),
		[]byte(`{"action":"grep"}`), 0o644); err != nil {
		t.Fatalf("write status (2nd trigger): %v", err)
	}

	select {
	case msg := <-secondDone:
		if _, ok := msg.(treeStatusChangedMsg); !ok {
			t.Errorf("expected treeStatusChangedMsg from re-armed chain, got %T", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out — per-session cmd-chain did not re-arm after first event")
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
	if msg != nil {
		t.Errorf("clearSessionCmd with empty piAgentDir must return nil, got %T", msg)
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
