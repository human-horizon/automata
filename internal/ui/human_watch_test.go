package ui

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/HumanHorizon/automata/internal/ai-knowledge/human"
	"github.com/Starframe/portalis"
	tea "github.com/charmbracelet/bubbletea"
)

func TestHumanReadMessagesAreGenerationBoundAndCoalesced(t *testing.T) {
	h := newHumanChat()
	h.active, h.generation = true, 3
	h.profile, h.sessionID = "test", "owner"
	reads := 0
	h.reader = func(profile, sessionID string) (*human.Data, error) {
		reads++
		if profile != "test" || sessionID != "owner" {
			t.Fatalf("reader identity = %q %q", profile, sessionID)
		}
		return humanTestData(40), nil
	}
	first := h.refreshCommand()
	if first == nil || h.refreshCommand() != nil || !h.readAgain {
		t.Fatal("concurrent reads are not coalesced")
	}
	initial := first().(humanProjectionLoadedMsg)
	second := h.acceptLoaded(initial)
	if second == nil || reads != 1 {
		t.Fatal("dirty refresh was not rearmed")
	}
	stale := initial
	stale.generation--
	stale.data = &human.Data{Epoch: "wrong"}
	h.acceptLoaded(stale)
	if h.data.Epoch != "epoch-1" {
		t.Fatal("stale generation overwrote data")
	}
	h.acceptLoaded(second().(humanProjectionLoadedMsg))
	if reads != 2 || h.readPending {
		t.Fatal("coalesced read did not settle")
	}
	h.deactivate()
	h.acceptLoaded(initial)
	if h.active || h.watcher != nil {
		t.Fatal("late load reactivated a hidden panel")
	}
}

func TestHumanWatcherFollowsDirectoryCreationAndIgnoresClosedGeneration(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AI_DATA_HOME", root)
	h := newHumanChat()
	h.reader = func(string, string) (*human.Data, error) { return humanTestData(40), nil }
	cmd := h.activate("test", "owner")
	defer h.deactivate()
	if cmd == nil || h.watchPath != root {
		t.Fatalf("initial watch path = %q", h.watchPath)
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("initial commands = %T", cmd())
	}
	initial := batch[0]().(humanProjectionLoadedMsg)
	h.acceptLoaded(initial)
	result := make(chan tea.Msg, 1)
	go func() { result <- batch[1]() }()
	directory := human.Directory("test", "owner")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	select {
	case raw := <-result:
		changed := raw.(humanProjectionChangedMsg)
		h.acceptChanged(changed)
		if h.watchPath != directory {
			t.Fatalf("watcher did not descend to Human: %q", h.watchPath)
		}
		if h.acceptChanged(changed) != nil {
			t.Fatal("closed watcher generation was reused")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("directory creation did not wake Human watcher")
	}
	h.deactivate()
	h.acceptLoaded(initial)
	if h.watcher != nil || h.active {
		t.Fatal("hidden Human watcher leaked")
	}
}

func TestHumanWatcherRejectsSymlinkAncestor(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	if err := os.Symlink(external, root+"/profiles"); err != nil {
		t.Fatal(err)
	}
	if _, err := humanWatchDirectory(root+"/profiles/test/sessions/owner/human", root); err == nil {
		t.Fatal("Human watcher followed external symlink")
	}
}

func TestHumanWatcherFailureInvalidatesInflightReadAndRecovers(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AI_DATA_HOME", root)
	h := newHumanChat()
	h.reader = func(string, string) (*human.Data, error) { return humanTestData(40), nil }
	h.activate("test", "owner")
	defer h.deactivate()
	oldRequest := h.request
	changed := humanProjectionChangedMsg{target: h, generation: h.generation, watcher: h.watcher, err: errors.New("watch lost")}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if h.acceptChanged(changed) == nil || h.request != oldRequest+1 {
		t.Fatal("failed watcher left an obsolete read blocking all refreshes")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if h.activate("test", "owner") == nil || h.watcher == nil || h.watchPath != root {
		t.Fatal("watcher did not recover when the owned root returned")
	}
}

func TestHumanBackwardsRevisionsNeverReplaceAValidSnapshot(t *testing.T) {
	for _, kind := range []string{"history", "live", "input"} {
		t.Run(kind, func(t *testing.T) {
			h := newHumanChat()
			h.active, h.generation, h.request = true, 3, 1
			current := humanTestData(40)
			current.HistoryRevision, current.LiveRevision, current.Input.Revision = 10, 10, 10
			h.data = current
			candidate := humanTestData(40)
			candidate.HistoryRevision, candidate.LiveRevision, candidate.Input.Revision = 10, 10, 10
			switch kind {
			case "history":
				candidate.HistoryRevision--
			case "live":
				candidate.LiveRevision--
			case "input":
				candidate.Input.Revision--
			}
			h.acceptLoaded(humanProjectionLoadedMsg{target: h, generation: 3, request: 1, data: candidate})
			if h.data != current || h.diagnostic == "" {
				t.Fatal("backwards revision replaced a valid snapshot")
			}
		})
	}
}

func TestHumanRetiredEpochCannotReplaceNewProjection(t *testing.T) {
	h := newHumanChat()
	h.active, h.generation, h.request = true, 7, 1
	h.data = humanTestData(40)
	h.selected = "chain-1"
	next := humanTestData(40)
	next.Epoch = "epoch-2"
	h.acceptLoaded(humanProjectionLoadedMsg{target: h, generation: 7, request: 1, data: next})
	if h.selected != "" || h.data != next {
		t.Fatal("new epoch did not reset the old chain selection")
	}
	h.request++
	h.acceptLoaded(humanProjectionLoadedMsg{target: h, generation: 7, request: 2, data: humanTestData(40)})
	if h.data != next || h.diagnostic == "" {
		t.Fatal("retired epoch overwrote the newer projection")
	}
}

func TestHumanRuntimeReplacementRejectsOldExporterAndDropsSelection(t *testing.T) {
	panel := newHumanTestChat()
	h := newHumanChat()
	h.profile, h.sessionID = "test", "owner"
	h.data = humanTestData(40)
	h.selected = "chain-1"
	panel.sessions[0].human = h
	fresh := portalis.NewEmulator("owner", "Main", "/bin/sh", nil)
	panel.sessions[0].SetEm(fresh)
	if h.data != nil || h.selected != "" || h.active {
		t.Fatal("runtime replacement retained the old view or watcher")
	}
	h.active, h.request = true, 1
	h.acceptLoaded(humanProjectionLoadedMsg{target: h, generation: h.generation, request: 1, data: humanTestData(40)})
	if h.data != nil || h.diagnostic == "" {
		t.Fatal("the old exporter was accepted after runtime replacement")
	}
}

func TestHumanMissingRootDoesNotCreateReadRenderPollingLoop(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AI_DATA_HOME", root)
	h := newHumanChat()
	h.active, h.profile, h.sessionID = true, "test", "owner"
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	defer h.deactivate()
	for range 5 {
		if h.activate("test", "owner") != nil || h.request != 0 {
			t.Fatal("missing watcher root started a render/read feedback loop")
		}
	}
}

func TestHumanRenameInvalidatesCachedIdentity(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	panel := newHumanTestChat()
	panel.active, panel.sessions[0].humanEnabled = true, true
	h := newHumanChat()
	h.active, h.profile, h.sessionID = true, "test", "owner"
	h.data = humanTestData(panel.width)
	panel.sessions[0].human = h
	panel.sessions[0].panel = &humanNativeRecordingPanel{}
	defer panel.Close()
	panel.RenameSessionIDs(map[string]string{"owner": "renamed"})
	if h.data != nil || h.sessionID != "renamed" || !h.active {
		t.Fatal("renamed Human retained old identity data")
	}
	if strings.Contains(panel.View(panel.width, 30), "user-visible") {
		t.Fatal("old projection leaked after rename")
	}
}

type humanNativeRecordingPanel struct {
	messages []tea.Msg
}

func (p *humanNativeRecordingPanel) View(int, int) string { return "native-terminal-or-dialog" }
func (p *humanNativeRecordingPanel) Update(msg tea.Msg) tea.Cmd {
	p.messages = append(p.messages, msg)
	return func() tea.Msg { return nil }
}

func TestHumanKeepsNativeKeyboardPastePtyAndDialogFallback(t *testing.T) {
	panel := newHumanTestChat()
	native := &humanNativeRecordingPanel{}
	panel.sessions[0].panel = native
	panel.sessions[0].humanEnabled = true
	h := newHumanChat()
	h.active = true
	h.data = humanTestData(panel.width)
	panel.sessions[0].human = h
	view := panel.View(panel.width, 30)
	if !strings.Contains(view, "user-visible") || strings.Contains(view, "native-terminal-or-dialog") {
		t.Fatalf("Human projection not used: %q", view)
	}
	messages := []tea.Msg{
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("text")},
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line 1\nline 2"), Paste: true},
		portalis.PtyOutputMsg{SessionID: "owner"},
	}
	for _, msg := range messages {
		panel.Update(msg)
	}
	if len(native.messages) != len(messages) {
		t.Fatalf("native input/PTY messages = %d, want %d", len(native.messages), len(messages))
	}
	h.data.Input.Native = true
	if view := panel.View(panel.width, 30); !strings.Contains(view, "native-terminal-or-dialog") {
		t.Fatal("native selector was hidden")
	}
	h.data.Input.Native = false
	h.diagnostic = "invalid projection"
	if view := panel.View(panel.width, 30); !strings.Contains(view, "native-terminal-or-dialog") || !strings.Contains(view, "Human: invalid projection") {
		t.Fatal("corrupt projection has no diagnostic native fallback")
	}
	panel.toggleHuman()
	if h.active || h.selected != "" || panel.sessions[0].humanEnabled {
		t.Fatal("disabling Human did not close split and reader")
	}
}
