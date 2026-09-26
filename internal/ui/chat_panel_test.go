package ui

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	apptheme "github.com/HumanHorizon/automata/internal/theme"
	"github.com/Starframe/portalis"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	warp "github.com/starframe-dev/warp"
)

func TestChatPanelRendersLifecycleActionWarning(t *testing.T) {
	panel := NewChatPanel(portalis.NewEmulator("session", "Main", "/bin/sh", nil), "session", "")
	panel.SetActionWarning("cleanup failed\nrestart skipped")

	view := panel.View(100, 3)
	if !strings.Contains(view, "! action: cleanup failed; restart skipped") {
		t.Fatalf("chat panel did not render lifecycle warning: %q", view)
	}
}

func TestRenderTabBarTruncatesANSIWithoutBreakingSequences(t *testing.T) {
	panel := &ChatPanel{
		activeIdx: 0,
		sessions: []*chatSession{
			{name: "Main"},
			{name: "A very long background chat"},
		},
	}

	// One button in the tab bar: " × Clear " (8 cells) + 1 padding = 9 cells.
	const width = 30
	bar := panel.renderTabBar(width)
	if got := ansi.StringWidth(bar); got != width {
		t.Fatalf("visible width = %d, want %d", got, width)
	}
	assertCompleteSGRSequences(t, bar)
}

// strip removes SGR escape sequences; local helper so chat_panel_test.go
// stays self-contained.
func strip(s string) string {
	var b strings.Builder
	in := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == 0x1b {
			in = true
			continue
		}
		if in {
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
				in = false
			}
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// fakePanel is a minimal warp.Panel that renders a fixed set of lines.
type fakePanel struct {
	lines []string
}

func (f *fakePanel) View(width, height int) string {
	out := make([]string, 0, len(f.lines))
	out = append(out, f.lines...)
	for len(out) < height {
		out = append(out, "")
	}
	if len(out) > height {
		out = out[:height]
	}
	return strings.Join(out, "\n")
}
func (f *fakePanel) Update(tea.Msg) tea.Cmd { return nil }

type messageRecordingPanel struct {
	messages []tea.Msg
}

func (p *messageRecordingPanel) View(width, height int) string { return "" }
func (p *messageRecordingPanel) Update(msg tea.Msg) tea.Cmd {
	p.messages = append(p.messages, msg)
	return nil
}

// recordingPanel captures every warp.ResizeMsg it receives so tests can
// assert how ChatPanel resized its children.
type recordingPanel struct {
	sizes []warp.ResizeMsg
}

func (r *recordingPanel) View(width, height int) string { return "" }
func (r *recordingPanel) Update(msg tea.Msg) tea.Cmd {
	if rm, ok := msg.(warp.ResizeMsg); ok {
		r.sizes = append(r.sizes, rm)
	}
	return nil
}

func assertCompleteSGRSequences(t *testing.T, value string) {
	t.Helper()
	for offset := 0; offset < len(value); {
		relative := strings.IndexByte(value[offset:], '\x1b')
		if relative < 0 {
			return
		}
		start := offset + relative
		if start+1 >= len(value) || value[start+1] != '[' {
			t.Fatalf("incomplete escape sequence at byte %d: %q", start, value[start:])
		}
		end := strings.IndexByte(value[start+2:], 'm')
		if end < 0 {
			t.Fatalf("unterminated SGR sequence at byte %d: %q", start, value[start:])
		}
		offset = start + 2 + end + 1
	}
}

// ---- Familiar detection tests ----

// writeFamiliarsJSON writes a familiars.json file in a temp directory and
// returns the session ID. profile can be empty for the canonical default path.
func writeFamiliarsJSON(t *testing.T, dir, profile string, familiars []FamiliarState) string {
	t.Helper()
	sessionID := "test"
	if profile == "" {
		profile = "default"
	}
	sessionDir := filepath.Join(dir, ".ai", "automata", "profiles", profile, "sessions", sessionID)
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	data, err := json.Marshal(familiars)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "familiars.json"), data, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return sessionID
}

func TestCheckFamiliarsDetectsNew(t *testing.T) {
	tempDir := t.TempDir()
	sessionID := writeFamiliarsJSON(t, tempDir, "", []FamiliarState{
		{ID: "expert", SessionID: "test__expert", Created: "2024-01-01"},
	})

	// Temporarily override HOME so familiarStatePath resolves inside tempDir.
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tempDir)
	defer os.Setenv("HOME", origHome)

	cp := &ChatPanel{
		sessionID: sessionID,
		known:     make(map[string]bool),
	}

	cmds := cp.checkFamiliars()
	if len(cmds) != 1 {
		t.Fatalf("expected 1 cmd (detected), got %d", len(cmds))
	}

	// Execute the cmd to get the message.
	msg := cmds[0]()
	detected, ok := msg.(familiarDetectedMsg)
	if !ok {
		t.Fatalf("expected familiarDetectedMsg, got %T", msg)
	}
	if detected.id != "expert" {
		t.Fatalf("expected id 'expert', got %q", detected.id)
	}
	if detected.familiarID != "test__expert" {
		t.Fatalf("expected familiarID 'test__expert', got %q", detected.familiarID)
	}

	// Second call should detect nothing (already known).
	cmds2 := cp.checkFamiliars()
	if len(cmds2) != 0 {
		t.Fatalf("expected 0 cmds on second call, got %d", len(cmds2))
	}
}

func TestCheckFamiliarsRegistryErrorsPreserveKnownAndTabs(t *testing.T) {
	invalidRegistries := []struct {
		name string
		data string
	}{
		{name: "malformed JSON", data: `{"id":`},
		{name: "object instead of array", data: `{"id":"expert"}`},
		{name: "null instead of array", data: `null`},
		{name: "missing required fields", data: `[{"id":"expert"}]`},
		{name: "duplicate id", data: `[{"id":"expert","sessionId":"test__one"},{"id":"expert","sessionId":"test__two"}]`},
		{name: "foreign session", data: `[{"id":"expert","sessionId":"other__expert"}]`},
		{name: "path-like session", data: `[{"id":"expert","sessionId":"test__../outside"}]`},
		{name: "owner reused as familiar", data: `[{"id":"expert","sessionId":"test"}]`},
	}
	for _, testCase := range invalidRegistries {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			sessionID := writeFamiliarsJSON(t, home, "", []FamiliarState{{ID: "expert", SessionID: "test__expert"}})
			cp := &ChatPanel{
				sessionID: sessionID,
				known:     map[string]bool{"expert": true},
				sessions: []*chatSession{
					{name: "Main", panel: &fakePanel{}},
					{name: "expert", panel: &fakePanel{}, familiarID: "test__expert"},
				},
			}
			if err := os.WriteFile(cp.familiarStatePath(), []byte(testCase.data), 0o644); err != nil {
				t.Fatal(err)
			}
			if cmds := cp.checkFamiliars(); len(cmds) != 0 {
				t.Fatalf("invalid registry produced %d commands", len(cmds))
			}
			if !cp.known["expert"] || len(cp.sessions) != 2 {
				t.Fatalf("invalid registry removed familiar state: known=%v sessions=%v", cp.known, sessionNames(cp.sessions))
			}
			if cp.familiarError == "" {
				t.Fatal("invalid registry did not set diagnostic")
			}
			if !strings.Contains(strip(cp.renderTabBar(100)), "! familiar registry:") {
				t.Fatal("registry failure is not visible in tab bar")
			}

			if err := os.WriteFile(cp.familiarStatePath(), []byte(`[]`), 0o644); err != nil {
				t.Fatal(err)
			}
			cmds := cp.checkFamiliars()
			if cp.familiarError != "" || len(cmds) != 1 {
				t.Fatalf("valid retry failed to recover: error=%q commands=%d", cp.familiarError, len(cmds))
			}
		})
	}
}

func TestCheckFamiliarsUnreadableRegistryPreservesKnownAndTabs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sessionID := writeFamiliarsJSON(t, home, "", []FamiliarState{{ID: "expert", SessionID: "test__expert"}})
	cp := &ChatPanel{
		sessionID: sessionID,
		known:     map[string]bool{"expert": true},
		sessions: []*chatSession{
			{name: "Main", panel: &fakePanel{}},
			{name: "expert", panel: &fakePanel{}, familiarID: "test__expert"},
		},
	}
	registryPath := cp.familiarStatePath()
	if err := os.Remove(registryPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(registryPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if cmds := cp.checkFamiliars(); len(cmds) != 0 {
		t.Fatalf("unreadable registry produced %d commands", len(cmds))
	}
	if !cp.known["expert"] || len(cp.sessions) != 2 || cp.familiarError == "" {
		t.Fatalf("unreadable registry changed state: known=%v sessions=%v error=%q", cp.known, sessionNames(cp.sessions), cp.familiarError)
	}
}

func TestExternalFamiliarRemovalRetriesCleanupFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sessionID := writeFamiliarsJSON(t, home, "", []FamiliarState{{ID: "expert", SessionID: "test__expert"}})
	cp := &ChatPanel{
		sessionID:        sessionID,
		known:            map[string]bool{"expert": true},
		familiarSessions: map[string]string{"expert": "test__expert"},
		sessions: []*chatSession{
			{name: "Main", panel: &fakePanel{}},
			{name: "expert", panel: &fakePanel{}, familiarID: "test__expert"},
		},
		active: true,
	}
	registryPath := cp.familiarStatePath()
	if err := os.WriteFile(registryPath, []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}
	preflightErr := errors.New("job identity is unknown")
	calls := 0
	cp.SetOnRemoveFamiliar(func(familiarID string, _ *portalis.Emulator) error {
		calls++
		if familiarID != "test__expert" {
			t.Fatalf("cleanup familiar ID = %q", familiarID)
		}
		if calls == 1 {
			return preflightErr
		}
		return nil
	})

	cmds := cp.checkFamiliars()
	if len(cmds) != 1 {
		t.Fatalf("initial removal commands = %d, want 1", len(cmds))
	}
	cp.Update(cmds[0]())
	if !cp.known["expert"] || len(cp.sessions) != 2 || cp.removalPending["expert"] {
		t.Fatalf("preflight failure lost tracking/tab: known=%v sessions=%v pending=%v", cp.known, sessionNames(cp.sessions), cp.removalPending)
	}
	if cp.familiarCleanupError == "" {
		t.Fatal("preflight failure is not exposed")
	}
	if !strings.Contains(strip(cp.renderTabBar(100)), "cleanup: "+preflightErr.Error()) {
		t.Fatal("preflight failure is not visible in tab bar")
	}

	cmds = cp.checkFamiliars()
	if len(cmds) != 1 {
		t.Fatalf("retry commands = %d, want 1", len(cmds))
	}
	cp.Update(cmds[0]())
	if calls != 2 || cp.known["expert"] || len(cp.sessions) != 1 || cp.familiarCleanupError != "" {
		t.Fatalf("retry did not commit cleanup: calls=%d known=%v sessions=%v error=%q", calls, cp.known, sessionNames(cp.sessions), cp.familiarCleanupError)
	}
	data, err := os.ReadFile(registryPath)
	if err != nil || string(data) != `[]` {
		t.Fatalf("externally updated registry was changed: data=%q err=%v", data, err)
	}
}

func TestExternalFamiliarRemovalDropsTabAfterCommittedWarning(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sessionID := writeFamiliarsJSON(t, home, "", []FamiliarState{{ID: "expert", SessionID: "test__expert"}})
	cp := &ChatPanel{
		sessionID:        sessionID,
		known:            map[string]bool{"expert": true},
		familiarSessions: map[string]string{"expert": "test__expert"},
		sessions: []*chatSession{
			{name: "Main", panel: &fakePanel{}},
			{name: "expert", panel: &fakePanel{}, familiarID: "test__expert"},
		},
		active: true,
	}
	if err := os.WriteFile(cp.familiarStatePath(), []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}
	warning := errors.New("active-state persistence failed after runtime stop")
	cp.SetOnRemoveFamiliar(func(string, *portalis.Emulator) error {
		return &CommittedCleanupError{Err: warning}
	})
	cmds := cp.checkFamiliars()
	if len(cmds) != 1 {
		t.Fatalf("removal commands = %d, want 1", len(cmds))
	}
	cp.Update(cmds[0]())
	if cp.known["expert"] || len(cp.sessions) != 1 {
		t.Fatalf("committed cleanup warning retained familiar: known=%v sessions=%v", cp.known, sessionNames(cp.sessions))
	}
	if !strings.Contains(cp.familiarCleanupError, warning.Error()) {
		t.Fatalf("committed cleanup warning not retained: %q", cp.familiarCleanupError)
	}
}

func TestCheckFamiliarsDetectsRemoved(t *testing.T) {
	tempDir := t.TempDir()
	sessionID := writeFamiliarsJSON(t, tempDir, "", []FamiliarState{})

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tempDir)
	defer os.Setenv("HOME", origHome)

	cp := &ChatPanel{
		sessionID: sessionID,
		known:     map[string]bool{"expert": true, "helper": true},
	}

	cmds := cp.checkFamiliars()
	if len(cmds) != 2 {
		t.Fatalf("expected 2 cmds (2 removed), got %d", len(cmds))
	}

	removedIDs := make(map[string]bool)
	for _, cmd := range cmds {
		msg := cmd()
		rm, ok := msg.(familiarRemovedMsg)
		if !ok {
			t.Fatalf("expected familiarRemovedMsg, got %T", msg)
		}
		removedIDs[rm.id] = true
	}
	if !removedIDs["expert"] || !removedIDs["helper"] {
		t.Fatalf("expected both 'expert' and 'helper' removed, got %v", removedIDs)
	}
}

func TestChatPanelDeactivateClosesWatcherAndPreservesTabs(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	cp := NewChatPanel(portalis.NewEmulator("owner", "Main", "/bin/sh", nil), "owner", "")
	cp.sessions = append(cp.sessions, &chatSession{
		name:       "expert",
		panel:      &fakePanel{},
		familiarID: "owner__expert",
	})
	cp.activeIdx = 1
	cp.known["expert"] = true
	cp.familiarSessions["expert"] = "owner__expert"
	registryPath := cp.familiarStatePath()
	if err := os.MkdirAll(filepath.Dir(registryPath), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal([]FamiliarState{{ID: "expert", SessionID: "owner__expert"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(registryPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	cp.Activate()
	oldWatcher := cp.familiarWatcher
	oldGeneration := cp.familiarGeneration
	if oldWatcher == nil {
		t.Fatal("Activate did not create a familiar watcher")
	}
	cp.Deactivate()
	if cp.active || cp.familiarWatcher != nil || cp.familiarGeneration <= oldGeneration {
		t.Fatalf("Deactivate did not invalidate watcher: active=%v watcher=%v generation=%d", cp.active, cp.familiarWatcher, cp.familiarGeneration)
	}
	if cp.activeIdx != 1 || len(cp.sessions) != 2 || !cp.known["expert"] {
		t.Fatalf("Deactivate discarded UI state: activeIdx=%d sessions=%d known=%v", cp.activeIdx, len(cp.sessions), cp.known)
	}

	cp.Update(familiarRegistryChangedMsg{generation: oldGeneration, watcher: oldWatcher, path: registryPath})
	if len(cp.sessions) != 2 || !cp.known["expert"] {
		t.Fatalf("stale watcher event changed familiar state: sessions=%d known=%v", len(cp.sessions), cp.known)
	}

	cp.Activate()
	if !cp.active || cp.familiarWatcher == nil || cp.familiarWatcher == oldWatcher {
		t.Fatalf("reactivation did not create a fresh watcher: active=%v watcher=%v", cp.active, cp.familiarWatcher)
	}
	cp.Close()
}

func TestFamiliarOwnershipRejectsExternalActions(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	cp := &ChatPanel{
		sessionID: "owner",
		known:     map[string]bool{"expert": true},
		familiarSessions: map[string]string{
			"expert": "foreign__expert",
		},
		removalPending: map[string]bool{"expert": true},
		sessions: []*chatSession{
			{name: "Main", panel: &fakePanel{}},
			{name: "expert", panel: &fakePanel{}, familiarID: "foreign__expert"},
		},
	}
	registryPath := cp.familiarStatePath()
	if err := os.MkdirAll(filepath.Dir(registryPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(registryPath, []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}
	createCalls, closeCalls, removeCalls := 0, 0, 0
	cp.SetCreateFamiliarEmulator(func(string) (*portalis.Emulator, []string) {
		createCalls++
		return nil, nil
	})
	cp.SetOnCloseFamiliar(func(string, *portalis.Emulator) error {
		closeCalls++
		return nil
	})
	cp.SetOnRemoveFamiliar(func(string, *portalis.Emulator) error {
		removeCalls++
		return nil
	})

	if cmd := cp.addFamiliar("other", "foreign__other"); cmd != nil {
		t.Fatal("foreign familiar produced a command")
	}
	if createCalls != 0 || len(cp.sessions) != 2 || cp.familiarError == "" {
		t.Fatalf("invalid add changed state: creates=%d sessions=%v error=%q", createCalls, sessionNames(cp.sessions), cp.familiarError)
	}
	if !strings.Contains(strip(cp.renderTabBar(100)), "! familiar registry:") {
		t.Fatal("invalid familiar add did not show a diagnostic")
	}

	cp.closeFamiliarByID("foreign__expert")
	if closeCalls != 0 || len(cp.sessions) != 2 {
		t.Fatalf("invalid close invoked cleanup or removed tab: closes=%d sessions=%v", closeCalls, sessionNames(cp.sessions))
	}

	cp.handleExternalFamiliarRemoval(familiarRemovedMsg{id: "expert", familiarID: "foreign__expert"})
	if removeCalls != 0 || len(cp.sessions) != 2 || cp.familiarCleanupError == "" {
		t.Fatalf("invalid external removal changed state: removes=%d sessions=%v error=%q", removeCalls, sessionNames(cp.sessions), cp.familiarCleanupError)
	}
}

func TestAddFamiliarCreatesTab(t *testing.T) {
	cp := &ChatPanel{
		sessionID: "test",
		sessions: []*chatSession{
			{name: "Main", panel: &fakePanel{}},
		},
		activeIdx: 0,
	}
	cp.SetCreateFamiliarEmulator(func(sessionID string) (*portalis.Emulator, []string) {
		em := portalis.NewEmulator(sessionID, sessionID, "/bin/sh", nil)
		return em, nil
	})

	cmd := cp.addFamiliar("expert", "test__expert")
	if cmd == nil {
		t.Fatal("expected non-nil cmd from addFamiliar")
	}

	if len(cp.sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(cp.sessions))
	}
	if cp.sessions[1].name != "expert" {
		t.Fatalf("expected session name 'expert', got %q", cp.sessions[1].name)
	}
	if cp.sessions[1].familiarID != "test__expert" {
		t.Fatalf("expected familiarID 'test__expert', got %q", cp.sessions[1].familiarID)
	}
	if cp.sessions[1].em == nil {
		t.Fatal("expected non-nil em for familiar session (now uses TermPanel)")
	}

	// Adding the same familiar again should be a no-op.
	cmd2 := cp.addFamiliar("expert", "test__expert")
	if cmd2 != nil {
		t.Fatal("expected nil cmd for duplicate familiar")
	}
	if len(cp.sessions) != 2 {
		t.Fatalf("expected still 2 sessions, got %d", len(cp.sessions))
	}
}

func TestRemoveFamiliarRemovesTab(t *testing.T) {
	cp := &ChatPanel{
		sessions: []*chatSession{
			{name: "Main", panel: &fakePanel{}},
			{name: "expert", panel: &fakePanel{}},
			{name: "helper", panel: &fakePanel{}},
		},
		activeIdx: 1, // expert is active
	}

	cp.removeFamiliar("expert")
	if len(cp.sessions) != 2 {
		t.Fatalf("expected 2 sessions after remove, got %d", len(cp.sessions))
	}
	if cp.sessions[0].name != "Main" || cp.sessions[1].name != "helper" {
		t.Fatalf("unexpected sessions: %v", sessionNames(cp.sessions))
	}
	// activeIdx stays at 1, now pointing to "helper" (shifted down).
	if cp.activeIdx != 1 {
		t.Fatalf("expected activeIdx=1 (helper), got %d", cp.activeIdx)
	}
}

func TestRemoveFamiliarLastTab(t *testing.T) {
	cp := &ChatPanel{
		sessions: []*chatSession{
			{name: "Main", panel: &fakePanel{}},
			{name: "expert", panel: &fakePanel{}},
		},
		activeIdx: 1, // expert is active
	}

	cp.removeFamiliar("expert")
	if len(cp.sessions) != 1 {
		t.Fatalf("expected 1 session after remove, got %d", len(cp.sessions))
	}
	if cp.sessions[0].name != "Main" {
		t.Fatalf("expected 'Main', got %q", cp.sessions[0].name)
	}
	if cp.activeIdx != 0 {
		t.Fatalf("expected activeIdx=0, got %d", cp.activeIdx)
	}
}

func TestRemoveFamiliarStopsPanel(t *testing.T) {
	// Create a recording panel that tracks if Stop was called.
	stopCalled := false
	fp := &stopRecordingPanel{stopFn: func() { stopCalled = true }}

	cp := &ChatPanel{
		sessions: []*chatSession{
			{name: "Main", panel: &fakePanel{}},
			{name: "expert", panel: fp},
		},
		activeIdx: 0,
	}

	cp.removeFamiliar("expert")
	if !stopCalled {
		t.Fatal("expected Stop() to be called on familiar panel")
	}
}

// stopRecordingPanel implements stopper for testing Stop() calls.
type stopRecordingPanel struct {
	stopFn func()
}

func (s *stopRecordingPanel) View(width, height int) string { return "" }
func (s *stopRecordingPanel) Update(msg tea.Msg) tea.Cmd    { return nil }
func (s *stopRecordingPanel) Stop()                         { s.stopFn() }

// sessionNames extracts names from a slice of chatSession pointers.
func sessionNames(sessions []*chatSession) []string {
	names := make([]string, len(sessions))
	for i, s := range sessions {
		names[i] = s.name
	}
	return names
}

func TestFamiliarSessionIDsReturnsNonMainOnly(t *testing.T) {
	cp := &ChatPanel{
		sessions: []*chatSession{
			{name: "Main", panel: &fakePanel{}, familiarID: ""},
			{name: "expert", panel: &fakePanel{}, familiarID: "f1"},
			{name: "helper", panel: &fakePanel{}, familiarID: "f2"},
		},
	}

	got := cp.FamiliarSessionIDs()
	if len(got) != 2 {
		t.Fatalf("expected 2 familiar ids, got %d (%v)", len(got), got)
	}
	set := map[string]bool{}
	for _, id := range got {
		set[id] = true
	}
	if !set["f1"] || !set["f2"] {
		t.Fatalf("expected f1 and f2, got %v", got)
	}
}

func TestFamiliarSessionIDsEmptyForMainOnly(t *testing.T) {
	cp := &ChatPanel{
		sessions: []*chatSession{
			{name: "Main", panel: &fakePanel{}, familiarID: ""},
		},
	}

	got := cp.FamiliarSessionIDs()
	if len(got) != 0 {
		t.Fatalf("expected 0 familiar ids for Main-only, got %v", got)
	}
}

func TestSessionsExporter(t *testing.T) {
	cp := &ChatPanel{
		sessions: []*chatSession{
			{name: "Main", panel: &fakePanel{}},
			{name: "expert", panel: &fakePanel{}, familiarID: "f1"},
		},
	}

	all := cp.Sessions()
	if len(all) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(all))
	}
	if all[0].Em() != nil {
		t.Fatal("Main em should be nil")
	}
	if all[1].FamiliarID() != "f1" {
		t.Fatalf("expected f1, got %q", all[1].FamiliarID())
	}
}

// TestRenderTabBarColorsFamiliarGrey verifies familiar tabs use semantic
// surface/raised theme colors rather than the main selection colors.
func TestRenderTabBarColorsFamiliarGrey(t *testing.T) {
	palette := apptheme.Default()
	inactive := familiarTabStyle(palette, false)
	active := familiarTabStyle(palette, true)

	inactiveBG, ok := inactive.GetBackground().(lipgloss.Color)
	if !ok || string(inactiveBG) != palette.Surface {
		t.Fatalf("inactive familiar background = %q, want %q", inactiveBG, palette.Surface)
	}
	activeBG, ok := active.GetBackground().(lipgloss.Color)
	if !ok || string(activeBG) != palette.Raised {
		t.Fatalf("active familiar background = %q, want %q", activeBG, palette.Raised)
	}
}

// TestRenderTabBarFamiliarHasCloseButton ensures the × button is appended
// to familiar tabs. Main tabs must not have it.
func TestRenderTabBarFamiliarHasCloseButton(t *testing.T) {
	cp := &ChatPanel{
		sessions: []*chatSession{
			{name: "Main"},
			{name: "expert", familiarID: "f1", em: portalis.NewEmulator("f1", "f1", "/bin/sh", nil)},
		},
		activeIdx: 0,
		width:     60,
	}
	bar := strip(cp.renderTabBar(60))
	if got := strings.Count(bar, "×"); got != 2 {
		t.Fatalf("expected familiar × plus main × Clear, got %d\n%s", got, bar)
	}
	// The Main tab has padding " Main " — familiar's × should appear AFTER
	// "expert", while the main Clear button's × appears at the right edge.
	mainIdx := strings.Index(bar, "Main")
	expertIdx := strings.Index(bar, "expert")
	closeIdx := strings.Index(bar[expertIdx:], "×")
	if mainIdx < 0 || expertIdx < 0 || closeIdx < 0 {
		t.Fatalf("expected Main, expert and familiar × in tab bar\n%s", bar)
	}
	closeIdx += expertIdx
	if closeIdx < expertIdx+len("expert") || closeIdx < mainIdx+len("Main")+2 {
		t.Errorf("familiar × should sit after the expert tab label\n%s", bar)
	}
	if !strings.Contains(bar[expertIdx:], "expert  × ") {
		t.Errorf("familiar close control must render as × after the label\n%s", bar)
	}
}

// TestHandleMouseFamiliarCloseButtonTriggersConfirm clicks the × region
// of a familiar tab and verifies pendingCloseFamiliar is set.
func TestHandleMouseUsesTerminalCellWidthsForUnicodeTabs(t *testing.T) {
	for _, name := range []string{"界", "e\u0301"} {
		t.Run(name, func(t *testing.T) {
			cp := &ChatPanel{
				sessions: []*chatSession{
					{name: "Main", panel: &fakePanel{}},
					{name: name, panel: &fakePanel{}, familiarID: "familiar-session"},
					{name: "Target", panel: &fakePanel{}},
				},
				activeIdx: 0,
				width:     80,
				height:    10,
			}
			targetX := ansi.StringWidth(" Main ") + 1 + ansi.StringWidth(" "+name+" ") + ansi.StringWidth(" ×") + 1
			cp.handleMouse(tea.MouseMsg{X: targetX, Y: 9, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
			if cp.activeIdx != 2 {
				t.Fatalf("cell %d after %q selected tab %d, want third tab", targetX, name, cp.activeIdx)
			}
		})
	}
}

func TestHandleMouseDoesNotHitTruncatedTabsOrWarningText(t *testing.T) {
	t.Run("truncated tab", func(t *testing.T) {
		cp := &ChatPanel{
			sessions: []*chatSession{
				{name: "Main", panel: &fakePanel{}},
				{name: "Hidden", panel: &fakePanel{}, familiarID: "familiar-session"},
			},
			activeIdx: 0,
			width:     17,
			height:    10,
		}
		cp.handleMouse(tea.MouseMsg{X: 7, Y: 9, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
		if cp.activeIdx != 0 {
			t.Fatalf("truncated tab was clickable: activeIdx=%d", cp.activeIdx)
		}
	})

	t.Run("warning prefix", func(t *testing.T) {
		cp := &ChatPanel{
			sessions: []*chatSession{
				{name: "Main", panel: &fakePanel{}},
				{name: "Target", panel: &fakePanel{}},
			},
			activeIdx:     1,
			width:         80,
			height:        10,
			familiarError: "registry is malformed",
		}
		cp.handleMouse(tea.MouseMsg{X: 0, Y: 9, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
		if cp.activeIdx != 1 {
			t.Fatalf("warning text selected a tab: activeIdx=%d", cp.activeIdx)
		}
	})
}

func TestHandleMouseFamiliarCloseButtonTriggersConfirm(t *testing.T) {
	cp := &ChatPanel{
		sessions: []*chatSession{
			{name: "Main"},
			{name: "expert", familiarID: "f1", em: portalis.NewEmulator("f1", "f1", "/bin/sh", nil)},
		},
		activeIdx: 0,
		width:     60,
		height:    5,
		active:    true,
		known:     map[string]bool{},
	}
	// Tab layout: " Main " (6) + space (1) + " expert " (8) + " ×" (2) = 17.
	// × region starts at column 6 + 1 + 8 = 15. Click anywhere within 15..16.
	msg := tea.MouseMsg{X: 16, Y: 4, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	cp.Update(msg)
	if cp.pendingCloseFamiliar != "f1" {
		t.Fatalf("expected pendingCloseFamiliar=\"f1\", got %q", cp.pendingCloseFamiliar)
	}
	if cp.closeFamiliarModal == nil {
		t.Fatal("expected closeFamiliarModal to be created")
	}
}

// TestConfirmYesDropsTabAndCallsCallback confirms via Y and verifies the
// tab is removed plus the onCloseFamiliar callback fires with the right id.
func TestConfirmYesDropsTabAndCallsCallback(t *testing.T) {
	var capturedID string
	var capturedEm *portalis.Emulator
	cp := &ChatPanel{
		sessionID: "owner",
		sessions: []*chatSession{
			{name: "Main"},
			{name: "expert", familiarID: "owner__f1", em: portalis.NewEmulator("owner__f1", "owner__f1", "/bin/sh", nil), panel: &fakePanel{}},
		},
		activeIdx: 0,
		active:    true,
		known:     map[string]bool{},
	}
	cp.SetOnCloseFamiliar(func(id string, em *portalis.Emulator) error {
		capturedID = id
		capturedEm = em
		return nil
	})
	cp.pendingCloseFamiliar = "owner__f1"
	cp.openCloseFamiliarModal("expert")

	// closeFamiliarByID is now synchronous; Update may return nil or a
	// poll cmd. Either is acceptable — the cleanup must have already run.
	_ = cp.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if len(cp.sessions) != 1 {
		t.Fatalf("expected 1 session after close, got %d", len(cp.sessions))
	}
	if cp.sessions[0].familiarID != "" {
		t.Errorf("main tab must remain, got familiarID=%q", cp.sessions[0].familiarID)
	}
	if cp.pendingCloseFamiliar != "" {
		t.Errorf("pendingCloseFamiliar should be cleared, got %q", cp.pendingCloseFamiliar)
	}
	if cp.closeFamiliarModal != nil {
		t.Error("closeFamiliarModal should be cleared")
	}
	if capturedID != "owner__f1" {
		t.Errorf("onCloseFamiliar called with %q, want owner__f1", capturedID)
	}
	if capturedEm == nil {
		t.Error("onCloseFamiliar should receive the familiar's emulator")
	}
}

func TestConfirmYesRemovesTabAfterCommittedCleanupWarning(t *testing.T) {
	familiar := portalis.NewEmulator("owner__f1", "owner__f1", "/bin/sh", nil)
	cp := &ChatPanel{
		sessionID: "owner",
		sessions: []*chatSession{
			{name: "Main", panel: &fakePanel{}},
			{name: "expert", familiarID: "owner__f1", em: familiar, panel: &fakePanel{}},
		},
		activeIdx: 0,
		active:    true,
		known:     map[string]bool{"expert": true},
	}
	cleanupErr := errors.New("active-state persistence failed after stop")
	var observedErr error
	cp.SetOnCloseFamiliar(func(string, *portalis.Emulator) error {
		observedErr = &CommittedCleanupError{Err: cleanupErr}
		return observedErr
	})
	cp.pendingCloseFamiliar = "owner__f1"
	cp.openCloseFamiliarModal("expert")

	_ = cp.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if len(cp.sessions) != 1 || cp.sessions[0].familiarID != "" {
		t.Fatalf("sessions after committed cleanup = %+v, want only Main", cp.sessions)
	}
	if !errors.Is(observedErr, cleanupErr) {
		t.Fatalf("observable committed warning = %v, want wrapped cleanup error", observedErr)
	}
}

func TestConfirmYesKeepsTabWhenHostCleanupFails(t *testing.T) {
	familiar := portalis.NewEmulator("owner__f1", "owner__f1", "/bin/sh", nil)
	cp := &ChatPanel{
		sessionID: "owner",
		sessions: []*chatSession{
			{name: "Main", panel: &fakePanel{}},
			{name: "expert", familiarID: "owner__f1", em: familiar, panel: &fakePanel{}},
		},
		activeIdx: 0,
		active:    true,
		known:     map[string]bool{"expert": true},
	}
	cleanupErr := errors.New("unknown PID identity")
	cp.SetOnCloseFamiliar(func(string, *portalis.Emulator) error {
		return cleanupErr
	})
	cp.pendingCloseFamiliar = "owner__f1"
	cp.openCloseFamiliarModal("expert")

	_ = cp.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if len(cp.sessions) != 2 {
		t.Fatalf("sessions after failed cleanup = %d, want 2", len(cp.sessions))
	}
	if cp.sessions[1].em != familiar || cp.sessions[1].familiarID != "owner__f1" {
		t.Fatal("familiar tab/emulator was removed after failed cleanup")
	}
	if !cp.known["expert"] {
		t.Fatal("failed cleanup removed familiar tracking state")
	}
}

// TestClickOutsideCloseModalCancels verifies that a click outside the modal
// dismisses the confirmation without changing the session list.
func TestClickOutsideCloseModalCancels(t *testing.T) {
	cp := &ChatPanel{
		sessions: []*chatSession{
			{name: "Main", panel: &fakePanel{}},
			{name: "expert", panel: &fakePanel{}, familiarID: "f1", em: portalis.NewEmulator("f1", "f1", "/bin/sh", nil)},
		},
		activeIdx: 0,
		width:     60,
		height:    10,
		active:    true,
		known:     map[string]bool{},
	}
	cp.pendingCloseFamiliar = "f1"
	cp.openCloseFamiliarModal("expert")

	cp.Update(tea.MouseMsg{X: 0, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})

	if len(cp.sessions) != 2 {
		t.Fatalf("expected 2 sessions after outside click, got %d", len(cp.sessions))
	}
	if cp.pendingCloseFamiliar != "" {
		t.Errorf("pendingCloseFamiliar should be cleared, got %q", cp.pendingCloseFamiliar)
	}
	if cp.closeFamiliarModal != nil {
		t.Error("closeFamiliarModal should be cleared after outside click")
	}
}

// TestRemoveFamiliarBeforeActiveKeepsActiveTab verifies that removing a tab
// before the active one preserves the active session rather than shifting to
// the following tab.
func TestRemoveFamiliarBeforeActiveKeepsActiveTab(t *testing.T) {
	cp := &ChatPanel{
		sessions: []*chatSession{
			{name: "Main"},
			{name: "expert", familiarID: "f1"},
			{name: "helper", familiarID: "f2"},
			{name: "reviewer", familiarID: "f3"},
		},
		activeIdx: 2,
	}

	cp.removeFamiliar("expert")

	if len(cp.sessions) != 3 {
		t.Fatalf("expected 3 sessions after remove, got %d", len(cp.sessions))
	}
	if cp.activeIdx != 1 {
		t.Fatalf("expected activeIdx=1 after removing earlier tab, got %d", cp.activeIdx)
	}
	if got := cp.sessions[cp.activeIdx].familiarID; got != "f2" {
		t.Fatalf("active familiar changed to %q, want f2", got)
	}
}

// TestDeadFamiliarPtyExitRemovesMatchingTab verifies that a familiar PTY
// exit still removes only the matching familiar and updates activeIdx.
func TestDeadFamiliarPtyExitRemovesMatchingTab(t *testing.T) {
	mainEm := portalis.NewEmulator("main-session", "Main", "/bin/sh", nil)
	familiarEm := portalis.NewEmulator("familiar-session", "expert", "/bin/sh", nil)
	cp := &ChatPanel{
		sessions: []*chatSession{
			{name: "Main", em: mainEm, panel: &fakePanel{}},
			{name: "expert", familiarID: "f1", em: familiarEm, panel: &fakePanel{}},
		},
		activeIdx: 1,
		active:    true,
		known:     map[string]bool{"expert": true},
	}

	cp.Update(portalis.PtyExitMsg{SessionID: "familiar-session"})

	if len(cp.sessions) != 1 || cp.sessions[0].name != "Main" {
		t.Fatalf("matching familiar exit left unexpected sessions: %#v", cp.sessions)
	}
	if cp.activeIdx != 0 {
		t.Fatalf("expected activeIdx=0 after familiar exit, got %d", cp.activeIdx)
	}
	if cp.known["expert"] {
		t.Fatal("dead familiar remained in known set")
	}
}

// TestMainPtyExitStaysInMainTabAndRoutesToPanel verifies that a main-session
// PTY exit neither removes Main nor gets swallowed by familiar cleanup.
func TestMainPtyExitStaysInMainTabAndRoutesToPanel(t *testing.T) {
	mainEm := portalis.NewEmulator("main-session", "Main", "/bin/sh", nil)
	familiarEm := portalis.NewEmulator("familiar-session", "expert", "/bin/sh", nil)
	mainPanel := &messageRecordingPanel{}
	cp := &ChatPanel{
		sessions: []*chatSession{
			{name: "Main", em: mainEm, panel: mainPanel},
			{name: "expert", familiarID: "f1", em: familiarEm, panel: &fakePanel{}},
		},
		activeIdx: 0,
		active:    true,
		known:     map[string]bool{"expert": true},
	}

	cp.Update(portalis.PtyExitMsg{SessionID: "main-session"})

	if len(cp.sessions) != 2 {
		t.Fatalf("main PTY exit removed a tab, got %d sessions", len(cp.sessions))
	}
	if cp.sessions[0].familiarID != "" || cp.sessions[0].name != "Main" {
		t.Fatalf("main tab was replaced: %#v", cp.sessions[0])
	}
	if len(mainPanel.messages) != 1 {
		t.Fatalf("main PTY exit was not routed to Main panel, got %d messages", len(mainPanel.messages))
	}
	if _, ok := mainPanel.messages[0].(portalis.PtyExitMsg); !ok {
		t.Fatalf("main panel received %T, want portalis.PtyExitMsg", mainPanel.messages[0])
	}
}

// TestConfirmEscLeavesTabIntact presses Escape and verifies the familiar tab
// stays put.
func TestConfirmEscLeavesTabIntact(t *testing.T) {
	cp := &ChatPanel{
		sessions: []*chatSession{
			{name: "Main", panel: &fakePanel{}},
			{name: "expert", familiarID: "f1", em: portalis.NewEmulator("f1", "f1", "/bin/sh", nil), panel: &fakePanel{}},
		},
		activeIdx: 0,
		active:    true,
		known:     map[string]bool{},
	}
	cp.pendingCloseFamiliar = "f1"
	cp.openCloseFamiliarModal("expert")

	cp.Update(tea.KeyMsg{Type: tea.KeyEsc})

	if len(cp.sessions) != 2 {
		t.Fatalf("expected 2 sessions after Escape, got %d", len(cp.sessions))
	}
	if cp.pendingCloseFamiliar != "" {
		t.Errorf("pendingCloseFamiliar should be cleared, got %q", cp.pendingCloseFamiliar)
	}
	if cp.closeFamiliarModal != nil {
		t.Error("closeFamiliarModal should be cleared after Escape")
	}
}

// TestConfirmNoLeavesTabIntact presses N and verifies the familiar tab
// stays put.
func TestConfirmNoLeavesTabIntact(t *testing.T) {
	cp := &ChatPanel{
		sessions: []*chatSession{
			{name: "Main"},
			{name: "expert", familiarID: "f1", em: portalis.NewEmulator("f1", "f1", "/bin/sh", nil)},
		},
		activeIdx: 0,
		active:    true,
		known:     map[string]bool{},
	}
	cp.pendingCloseFamiliar = "f1"
	cp.openCloseFamiliarModal("expert")

	cp.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})

	if len(cp.sessions) != 2 {
		t.Fatalf("expected 2 sessions after cancel, got %d", len(cp.sessions))
	}
	if cp.pendingCloseFamiliar != "" {
		t.Errorf("pendingCloseFamiliar should be cleared, got %q", cp.pendingCloseFamiliar)
	}
	if cp.closeFamiliarModal != nil {
		t.Error("closeFamiliarModal should be cleared")
	}
}
