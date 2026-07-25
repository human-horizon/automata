package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Starframe/portalis"
	tea "github.com/charmbracelet/bubbletea"
	warp "github.com/starframe-dev/warp"
	"github.com/charmbracelet/x/ansi"
)

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
// returns the session ID. profile can be empty for no-profile path.
func writeFamiliarsJSON(t *testing.T, dir, profile string, familiars []FamiliarState) string {
	t.Helper()
	sessionID := "test-session"
	var sessionDir string
	if profile != "" {
		sessionDir = filepath.Join(dir, ".ai", "automata", "profiles", profile, "sessions", sessionID)
	} else {
		sessionDir = filepath.Join(dir, ".ai", "automata", "sessions", sessionID)
	}
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

func TestAddFamiliarCreatesTab(t *testing.T) {
	cp := &ChatPanel{
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
