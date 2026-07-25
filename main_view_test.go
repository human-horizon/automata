package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HumanHorizon/automata/internal/paths"
	"github.com/HumanHorizon/automata/internal/tree"
	"github.com/HumanHorizon/automata/internal/ui"
	"github.com/Starframe/portalis"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	warp "github.com/starframe-dev/warp"
)

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

	t.Log("И: эмулятор несёт PI_CODING_AGENT_DIR в startEnv, так что любой Start сохраняет окружение")
	wantEnv := "PI_CODING_AGENT_DIR=" + piAgentDir
	gotEnv := restarted.StartEnv()
	if len(gotEnv) != 1 || gotEnv[0] != wantEnv {
		t.Fatalf("start environment = %#v, want [%q]", gotEnv, wantEnv)
	}
}

func TestPiLaunchSetsAgentDirAndProfile(t *testing.T) {
	t.Setenv("PI_CMD", "")
	home := t.TempDir()
	t.Setenv("HOME", home)

	geticDir := filepath.Join(home, ".ai", "getic", "pi")
	app := &App{profile: "Getic", piAgentDir: geticDir}

	t.Log("Когда: резолвер запуска pi вызывается для профиля Getic с --pi getic")
	cmd, args, env := app.piLaunch("getic__chat")

	t.Log("Тогда: запускается /usr/local/bin/pi с --session-id")
	if cmd != "/usr/local/bin/pi" {
		t.Fatalf("cmd = %q, want /usr/local/bin/pi", cmd)
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
}

func TestCreateChatEmulatorWithoutPiCommandReturnsNil(t *testing.T) {
	t.Setenv("PI_CMD", "")
	t.Setenv("PATH", t.TempDir()) // no just-pi on PATH
	app := &App{tree: tree.New()}  // no piAgentDir

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
			if !handled {
				t.Fatalf("cached PTY message %q was not handled", test.name)
			}
		})
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
	famDir := filepath.Join(home, ".ai", "automata", "sessions", mainSID)
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

	// 8. Main session restarted and remains in activeSessions; familiars do not.
	if _, ok := app.emulatorCache[mainSID]; !ok {
		t.Error("main emulator missing from emulatorCache after restart")
	}
	if _, ok := app.activeSessions[mainSID]; !ok {
		t.Error("main not in activeSessions after restart")
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

// TestClearKillsFamiliarsRespectsProfile verifies that ClearFamiliarsJSONL
// resolves familiars.json under the profile-scoped path, not the default one.
func TestClearKillsFamiliarsRespectsProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	const (
		profile  = "humanhorizon"
		mainSID  = "humanhorizon__chat"
		famSID   = "humanhorizon__chat__expert"
		cwd      = "/Users/a/Space"
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
