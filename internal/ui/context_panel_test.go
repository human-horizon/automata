package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HumanHorizon/automata/internal/ai-knowledge/memory"
	"github.com/HumanHorizon/automata/internal/paths"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

type testNotesClipboard struct {
	readText  string
	readErr   error
	readCount int
	written   string
	writeErr  error
}

func (c *testNotesClipboard) Read() (string, error) {
	c.readCount++
	return c.readText, c.readErr
}

func (c *testNotesClipboard) Write(text string) error {
	c.written = text
	return c.writeErr
}

func clickNotesToolbar(t *testing.T, cp *ContextPanel, action notesClipboardAction) {
	t.Helper()
	x := -1
	for candidate := 0; candidate < 100; candidate++ {
		if cp.notesToolbarActionAt(candidate) == action {
			x = candidate
			break
		}
	}
	if x < 0 {
		t.Fatalf("toolbar action %q is not hit-testable", action)
	}
	cmd := cp.handleMouse(tea.MouseMsg{
		X:      x,
		Y:      notesToolbarRow,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	if cmd != nil {
		cp.Update(cmd())
	}
}

func confirmNotesPaste(t *testing.T, cp *ContextPanel) {
	t.Helper()
	if cp.notesPasteModal == nil {
		t.Fatal("expected paste confirmation modal")
	}
	cmd := cp.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("confirmation did not start clipboard read")
	}
	cp.Update(cmd())
}

// TestContextPanelEmptyDomain verifies the placeholder for a missing domain.
func TestContextPanelEmptyDomain(t *testing.T) {
	cp := NewContextPanel("")
	cp.SetDomain("")
	out := cp.View(40, 10)
	if out == "" {
		t.Fatalf("expected non-empty placeholder view")
	}
	if !strings.Contains(out, "No domain context") {
		t.Fatalf("expected 'No domain context' placeholder, got:\n%s", out)
	}
}

// TestContextPanelNoNotes verifies the placeholder when domain has no notes.
func TestContextPanelNoNotes(t *testing.T) {
	cp := NewContextPanel("test-profile")
	cp.SetDomain("my-domain")
	out := cp.View(40, 10)
	if !strings.Contains(out, "No notes") {
		t.Fatalf("expected 'No notes' placeholder, got:\n%s", out)
	}
}

func TestContextPanelNotesToolbarFitsNarrowWidth(t *testing.T) {
	cp := NewContextPanel("profile")
	view := cp.View(15, 5)
	lines := strings.Split(view, "\n")
	if len(lines) != 5 {
		t.Fatalf("expected five rendered lines, got %d:\n%s", len(lines), view)
	}
	if got := ansi.StringWidth(ansi.Strip(lines[notesToolbarRow])); got > 15 {
		t.Fatalf("toolbar width = %d, want <= 15: %q", got, lines[notesToolbarRow])
	}
}

func TestContextPanelNotesToolbarHasVerticalPadding(t *testing.T) {
	cp := NewContextPanel("profile")
	lines := strings.Split(ansi.Strip(cp.View(40, 8)), "\n")
	if len(lines) != 8 {
		t.Fatalf("expected eight rendered lines, got %d", len(lines))
	}
	if strings.TrimSpace(lines[notesToolbarRow-1]) != "" {
		t.Fatalf("expected blank row above toolbar, got %q", lines[notesToolbarRow-1])
	}
	if !strings.Contains(lines[notesToolbarRow], "Copy") {
		t.Fatalf("expected toolbar on row %d, got %q", notesToolbarRow, lines[notesToolbarRow])
	}
	if strings.TrimSpace(lines[notesToolbarRow+1]) != "" {
		t.Fatalf("expected blank row below toolbar, got %q", lines[notesToolbarRow+1])
	}
}

func TestContextPanelNotesToolbarRendersAndCopiesAllNotes(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	profile := "ctx-clipboard-copy-profile"
	domain := "ctx-clipboard-copy-domain"
	domainDir := paths.DomainDir(profile, domain)
	if err := os.MkdirAll(domainDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	notes := `[{"title":"First","sections":[{"title":"Decision","content":"keep"}]},{"title":"Second","sections":[{"title":"Code","content":"copy me"}]}]`
	if err := os.WriteFile(filepath.Join(domainDir, "notes.json"), []byte(notes), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}

	cp := NewContextPanel(profile)
	clipboard := &testNotesClipboard{}
	cp.notesClipboard = clipboard
	cp.SetDomain(domain)
	t.Cleanup(func() { cp.closeNotesWatcher() })

	plain := ansi.Strip(cp.View(80, 8))
	for _, want := range []string{"Copy", "Paste +", "Paste replace"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("toolbar is missing %q:\n%s", want, plain)
		}
	}

	clickNotesToolbar(t, cp, notesClipboardCopy)
	if !strings.Contains(clipboard.written, `"First"`) || !strings.Contains(clipboard.written, `"Second"`) {
		t.Fatalf("copy did not include all notes: %s", clipboard.written)
	}
	if !strings.Contains(clipboard.written, `"sections"`) || !strings.Contains(clipboard.written, `"Decision"`) {
		t.Fatalf("copy did not preserve sections: %s", clipboard.written)
	}
	if cp.expandedNotes["note-0"] {
		t.Fatal("clicking Copy unexpectedly expanded a note")
	}
	if cp.notesStatus != "✓ Copied" {
		t.Fatalf("copy status = %q, want success status", cp.notesStatus)
	}
}

func TestContextPanelPasteAddAndReplaceNotes(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	profile := "ctx-clipboard-paste-profile"
	domain := "ctx-clipboard-paste-domain"
	domainDir := paths.DomainDir(profile, domain)
	if err := os.MkdirAll(domainDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	initial := `[{"title":"Existing","sections":[{"content":"old"}]}]`
	if err := os.WriteFile(filepath.Join(domainDir, "notes.json"), []byte(initial), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}

	cp := NewContextPanel(profile)
	clipboard := &testNotesClipboard{
		readText: `[{"title":"Incoming","sections":[{"content":"new"}]}]`,
	}
	cp.notesClipboard = clipboard
	cp.SetDomain(domain)
	cp.closeNotesWatcher()
	t.Cleanup(func() { cp.closeNotesWatcher() })

	clickNotesToolbar(t, cp, notesClipboardAdd)
	if clipboard.readCount != 0 {
		t.Fatalf("paste add read clipboard before confirmation: %d", clipboard.readCount)
	}
	confirmNotesPaste(t, cp)
	data, err := memory.Read(profile, domain)
	if err != nil {
		t.Fatalf("read after add: %v", err)
	}
	if len(data.Notes) != 2 || data.Notes[0].Title != "Existing" || data.Notes[1].Title != "Incoming" {
		t.Fatalf("add result = %+v", data.Notes)
	}

	clipboard.readText = `[{"title":"Replacement","sections":[{"content":"replace"}]}]`
	clickNotesToolbar(t, cp, notesClipboardReplace)
	if clipboard.readCount != 1 {
		t.Fatalf("replace read clipboard before confirmation: %d", clipboard.readCount)
	}
	confirmNotesPaste(t, cp)
	data, err = memory.Read(profile, domain)
	if err != nil {
		t.Fatalf("read after replace: %v", err)
	}
	if len(data.Notes) != 1 || data.Notes[0].Title != "Replacement" {
		t.Fatalf("replace result = %+v", data.Notes)
	}
	if cp.notesStatus != "✓ Replaced" {
		t.Fatalf("replace status = %q, want success status", cp.notesStatus)
	}
}

func TestContextPanelPasteConfirmationCancelsWithoutReading(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	profile := "ctx-clipboard-confirmation-profile"
	domain := "ctx-clipboard-confirmation-domain"
	domainDir := paths.DomainDir(profile, domain)
	if err := os.MkdirAll(domainDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(domainDir, "notes.json")
	initial := `[{"title":"Keep"}]`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}

	clipboard := &testNotesClipboard{readText: `[{"title":"Incoming"}]`}
	cp := NewContextPanel(profile)
	cp.notesClipboard = clipboard
	cp.SetDomain(domain)
	cp.closeNotesWatcher()
	t.Cleanup(func() { cp.closeNotesWatcher() })

	clickNotesToolbar(t, cp, notesClipboardAdd)
	if cp.notesPasteModal == nil || !strings.Contains(cp.notesPasteModal.Content, "Add notes") {
		t.Fatal("expected add confirmation")
	}
	modalView := ansi.Strip(cp.View(60, 10))
	for _, want := range []string{"Add notes", "[Yes]", "[No]"} {
		if !strings.Contains(modalView, want) {
			t.Fatalf("confirmation view is missing %q:\n%s", want, modalView)
		}
	}
	cp.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if cp.notesPasteModal != nil {
		t.Fatal("N should close add confirmation")
	}

	clickNotesToolbar(t, cp, notesClipboardReplace)
	if cp.notesPasteModal == nil || !strings.Contains(cp.notesPasteModal.Content, "Replace") {
		t.Fatal("expected replace confirmation")
	}
	cp.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cp.notesPasteModal != nil {
		t.Fatal("Esc should close replace confirmation")
	}

	clickNotesToolbar(t, cp, notesClipboardAdd)
	if cp.notesPasteModal == nil {
		t.Fatal("expected confirmation for No callback")
	}
	cp.notesPasteModal.Buttons[1].Action()
	if cp.notesPasteModal != nil {
		t.Fatal("No should close confirmation")
	}

	clickNotesToolbar(t, cp, notesClipboardReplace)
	cp.View(80, 10)
	cp.Update(tea.MouseMsg{X: 0, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if cp.notesPasteModal != nil {
		t.Fatal("outside click should close confirmation")
	}
	if clipboard.readCount != 0 {
		t.Fatalf("cancelled paste read clipboard %d times", clipboard.readCount)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read notes after cancellation: %v", err)
	}
	if string(got) != initial {
		t.Fatalf("cancelled paste changed notes: %s", got)
	}
}

func TestContextPanelClipboardErrorsAndEmptyReplace(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	profile := "ctx-clipboard-errors-profile"
	domain := "ctx-clipboard-errors-domain"
	domainDir := paths.DomainDir(profile, domain)
	if err := os.MkdirAll(domainDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(domainDir, "notes.json")
	if err := os.WriteFile(path, []byte(`[{"title":"Keep"}]`), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}

	cp := NewContextPanel(profile)
	cp.SetDomain(domain)
	cp.closeNotesWatcher()
	t.Cleanup(func() { cp.closeNotesWatcher() })

	cp.notesClipboard = &testNotesClipboard{writeErr: os.ErrPermission}
	clickNotesToolbar(t, cp, notesClipboardCopy)
	if cp.notesStatus != "✗ Clipboard error" {
		t.Fatalf("copy error status = %q", cp.notesStatus)
	}

	cp.notesClipboard = &testNotesClipboard{readErr: os.ErrPermission}
	clickNotesToolbar(t, cp, notesClipboardAdd)
	confirmNotesPaste(t, cp)
	if cp.notesStatus != "✗ Clipboard error" {
		t.Fatalf("paste error status = %q", cp.notesStatus)
	}

	cp.notesClipboard = &testNotesClipboard{readText: "[]"}
	clickNotesToolbar(t, cp, notesClipboardReplace)
	confirmNotesPaste(t, cp)
	data, err := memory.Read(profile, domain)
	if err != nil {
		t.Fatalf("read after empty replace: %v", err)
	}
	if len(data.Notes) != 0 {
		t.Fatalf("empty replace left notes: %+v", data.Notes)
	}
}

func TestContextPanelInvalidPasteLeavesNotesUnchanged(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	profile := "ctx-clipboard-invalid-profile"
	domain := "ctx-clipboard-invalid-domain"
	domainDir := paths.DomainDir(profile, domain)
	if err := os.MkdirAll(domainDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(domainDir, "notes.json")
	initial := `[{"title":"Keep","sections":[{"content":"unchanged"}]}]`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}

	clipboard := &testNotesClipboard{readText: "not json"}
	cp := NewContextPanel(profile)
	cp.notesClipboard = clipboard
	cp.SetDomain(domain)
	cp.closeNotesWatcher()
	t.Cleanup(func() { cp.closeNotesWatcher() })

	clickNotesToolbar(t, cp, notesClipboardAdd)
	if clipboard.readCount != 0 {
		t.Fatalf("invalid paste read clipboard before confirmation: %d", clipboard.readCount)
	}
	confirmNotesPaste(t, cp)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read notes after invalid paste: %v", err)
	}
	if string(got) != initial {
		t.Fatalf("invalid paste changed notes: %s", got)
	}
	if cp.notesStatus != "✗ Invalid notes" {
		t.Fatalf("invalid paste status = %q", cp.notesStatus)
	}
}

// TestContextPanelRendersNotes verifies domain notes are rendered.
func TestContextPanelRendersNotes(t *testing.T) {
	profile := "ctx-test-profile"
	domain := "ctx-test-domain"

	// Set up a temporary data home so we don't touch real notes.
	tmpDir := t.TempDir()
	os.Setenv("AI_DATA_HOME", tmpDir)
	os.Setenv("AI_PROFILE", profile)
	defer os.Unsetenv("AI_DATA_HOME")
	defer os.Unsetenv("AI_PROFILE")

	domainDir := filepath.Join(tmpDir, "profiles", "ctx-test-profile", "domains", domain)
	if err := os.MkdirAll(domainDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	notesPath := filepath.Join(domainDir, "notes.json")
	notes := `[{"title":"Project notes","notes":["first note","second note"]}]`
	if err := os.WriteFile(notesPath, []byte(notes), 0644); err != nil {
		t.Fatalf("write notes: %v", err)
	}

	cp := NewContextPanel(profile)
	cp.SetDomain(domain)
	initial := cp.View(40, 20)
	if strings.Contains(initial, "first note") {
		t.Fatalf("legacy section should be collapsed initially, got:\n%s", initial)
	}
	cp.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	out := cp.View(40, 20)
	if !strings.Contains(out, "Project notes") {
		t.Fatalf("expected note title, got:\n%s", out)
	}
	if !strings.Contains(out, "first note") {
		t.Fatalf("expected first note, got:\n%s", out)
	}
}

func TestContextPanelCanonicalUnicodePath(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	profile := "Проект Ω"
	domain := "proekt-ω__knowledge"
	canonicalDir := paths.DomainDir(profile, domain)
	if err := os.MkdirAll(canonicalDir, 0o755); err != nil {
		t.Fatalf("mkdir canonical domain: %v", err)
	}
	if err := os.WriteFile(filepath.Join(canonicalDir, "notes.json"), []byte(`[{"title":"Canonical title"}]`), 0o644); err != nil {
		t.Fatalf("write canonical notes: %v", err)
	}

	legacyDir := filepath.Join(dataHome, "profiles", profile, "domains", domain)
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatalf("mkdir legacy domain: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "notes.json"), []byte(`[{"title":"Legacy title"}]`), 0o644); err != nil {
		t.Fatalf("write legacy notes: %v", err)
	}

	cp := NewContextPanel(profile)
	t.Cleanup(func() { cp.closeNotesWatcher() })
	cp.SetDomain(domain)
	if got := cp.domainDirForActive(); got != canonicalDir {
		t.Fatalf("domain directory = %q, want %q", got, canonicalDir)
	}
	if cp.notesWatcher == nil {
		t.Fatal("expected notes watcher")
	}
	if cp.data == nil || len(cp.data.Notes) != 1 || cp.data.Notes[0].Title != "Canonical title" {
		t.Fatalf("expected canonical title, got %+v", cp.data)
	}
	if cp.data.Notes[0].Title == "Legacy title" {
		t.Fatal("loaded legacy title")
	}
}

func TestContextPanelRendersOneNoteSectionAndMarkdown(t *testing.T) {
	profile := "ctx-sections-profile"
	domain := "ctx-sections-domain"
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	t.Setenv("AI_PROFILE", profile)

	domainDir := filepath.Join(dataHome, "profiles", profile, "domains", domain)
	if err := os.MkdirAll(domainDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	notes := `[{"title":"Project","sections":[{"title":"Decision","content":"**Use sections**\n\n- Keep Markdown"}]}]`
	if err := os.WriteFile(filepath.Join(domainDir, "notes.json"), []byte(notes), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}

	cp := NewContextPanel(profile)
	cp.SetDomain(domain)
	out := cp.View(60, 20)
	_, hits := cp.renderContentLayout(60)
	if len(hits) != 1 {
		t.Fatalf("expected one interactive hit for one note, got %d", len(hits))
	}
	plain := ansi.Strip(out)
	if !strings.Contains(plain, "\n ▸ Project") {
		t.Fatalf("expected one-space collapsed note marker, got:\n%s", out)
	}
	if strings.Contains(out, "Use sections") {
		t.Fatalf("section should be collapsed initially, got:\n%s", out)
	}
	cp.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	out = cp.View(60, 20)
	_, hits = cp.renderContentLayout(60)
	if len(hits) != 1 || strings.Count(out, "▾") != 1 {
		t.Fatalf("expected one interactive expanded note section, hits=%d, output:\n%s", len(hits), out)
	}
	plain = ansi.Strip(out)
	for _, want := range []string{"▾ Project", "Decision", "Use sections", "• Keep Markdown", "│"} {
		if !strings.Contains(plain, want) {
			t.Errorf("expected %q in rendered sections, got:\n%s", want, out)
		}
	}
	var noteLine, contentLine string
	for _, line := range strings.Split(plain, "\n") {
		if strings.Contains(line, "▾ Project") {
			noteLine = line
		}
		if strings.Contains(line, "│") && strings.Contains(line, "Decision") {
			contentLine = line
		}
	}
	if noteLine == "" || contentLine == "" || strings.Index(noteLine, "▾") != strings.Index(contentLine, "│") {
		t.Fatalf("expected opening line under marker, note=%q content=%q", noteLine, contentLine)
	}
	if !strings.Contains(contentLine, "│  Decision") {
		t.Fatalf("expected exactly two spaces after opening line, content=%q", contentLine)
	}
	if strings.Contains(plain, "**Use sections**") {
		t.Errorf("raw Markdown markers leaked into ContextPanel:\n%s", out)
	}
}

func TestContextPanelNoteMouseToggle(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	profile := "ctx-mouse-profile"
	domain := "ctx-mouse-domain"
	domainDir := filepath.Join(dataHome, "profiles", profile, "domains", domain)
	if err := os.MkdirAll(domainDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(domainDir, "notes.json"), []byte(`[{"title":"Project","sections":[{"title":"Section","content":"content"}]}]`), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}

	cp := NewContextPanel(profile)
	cp.SetDomain(domain)
	t.Cleanup(func() { cp.closeNotesWatcher() })
	out := cp.View(60, 10)
	if !strings.Contains(out, "▸ Project") || strings.Contains(out, "content") {
		t.Fatalf("expected collapsed note, got:\n%s", out)
	}

	cp.handleMouse(tea.MouseMsg{Y: notesContentStartRow, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	out = cp.View(60, 10)
	if !strings.Contains(out, "▾ Project") || !strings.Contains(out, "content") || !strings.Contains(out, "│") {
		t.Fatalf("expected expanded note after mouse click, got:\n%s", out)
	}
}

func TestContextPanelNoteKeyboardAndDomainReset(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	profile := "ctx-keyboard-profile"
	for _, domain := range []string{"first", "second"} {
		domainDir := filepath.Join(dataHome, "profiles", profile, "domains", domain)
		if err := os.MkdirAll(domainDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		content := domain + " content"
		notes := `[{"title":"Project","sections":[{"title":"Section","content":"` + content + `"}]}]`
		if err := os.WriteFile(filepath.Join(domainDir, "notes.json"), []byte(notes), 0o644); err != nil {
			t.Fatalf("write notes: %v", err)
		}
	}

	cp := NewContextPanel(profile)
	cp.SetDomain("first")
	t.Cleanup(func() { cp.closeNotesWatcher() })
	_ = cp.View(60, 10)
	cp.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if out := cp.View(60, 10); !strings.Contains(out, "first content") {
		t.Fatalf("expected expanded first note, got:\n%s", out)
	}
	cp.handleKey(tea.KeyMsg{Type: tea.KeyLeft})
	if out := cp.View(60, 10); strings.Contains(out, "first content") {
		t.Fatalf("expected left key to collapse note, got:\n%s", out)
	}
	cp.handleKey(tea.KeyMsg{Type: tea.KeyRight})
	if out := cp.View(60, 10); !strings.Contains(out, "first content") {
		t.Fatalf("expected right key to expand note, got:\n%s", out)
	}

	cp.SetDomain("second")
	out := cp.View(60, 10)
	if !strings.Contains(out, "▸ Project") || strings.Contains(out, "second content") {
		t.Fatalf("expected domain switch to reset collapsed state, got:\n%s", out)
	}
}

func TestContextPanelNoteNavigation(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	profile := "ctx-navigation-profile"
	domain := "ctx-navigation-domain"
	domainDir := filepath.Join(dataHome, "profiles", profile, "domains", domain)
	if err := os.MkdirAll(domainDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := `[{"title":"First","sections":[{"title":"Details","content":"first content"}]},{"title":"Second","sections":[{"title":"Details","content":"second content"}]}]`
	if err := os.WriteFile(filepath.Join(domainDir, "notes.json"), []byte(content), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}

	cp := NewContextPanel(profile)
	cp.SetDomain(domain)
	t.Cleanup(func() { cp.closeNotesWatcher() })
	_ = cp.View(60, 12)
	cp.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	cp.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	out := cp.View(60, 12)
	if !strings.Contains(out, "▾ Second") || !strings.Contains(out, "second content") || strings.Contains(out, "first content") {
		t.Fatalf("expected Down + Enter to expand second note only, got:\n%s", out)
	}
}

// TestContextPanelUpdateWindowSize verifies the panel stores dimensions.
func TestContextPanelUpdateWindowSize(t *testing.T) {
	cp := NewContextPanel("")
	cp.Update(tea.WindowSizeMsg{Width: 60, Height: 30})
	if cp.width != 60 || cp.height != 30 {
		t.Fatalf("size not stored: w=%d h=%d", cp.width, cp.height)
	}
}

// TestContextPanelNotesWatcherReactive proves that updating notes.json on
// disk propagates into cp.data without any manual Refresh call — the
// regression for "agent updated notes, need to restart to see".
func TestContextPanelNotesWatcherReactive(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("AI_DATA_HOME", tmpDir)
	const profile = "ctx-watcher-profile"
	const domain = "ctx-watcher-domain"

	cp := NewContextPanel(profile)
	cp.SetDomain(domain)
	t.Cleanup(func() { cp.closeNotesWatcher() })

	notesPath := filepath.Join(tmpDir, "profiles", profile, "domains", domain, "notes.json")
	// Sanity: the watcher was created in SetDomain and the domain dir exists.
	if cp.notesWatcher == nil {
		t.Fatal("expected notesWatcher to be created in SetDomain")
	}
	if _, err := os.Stat(filepath.Dir(notesPath)); err != nil {
		t.Fatalf("domain dir not created: %v", err)
	}

	// Simulate the agent writing notes.json on disk.
	if err := os.WriteFile(notesPath,
		[]byte(`[{"title":"From agent","notes":["hello"]}]`), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}

	// Inject a notesChangedMsg directly. In production this arrives from
	// the goroutine blocking on watcher.Events; here we skip the channel
	// round-trip because fsnotify's Events channel is unbuffered and the
	// test process can't both write to it and consume via Update in a
	// race-free way without an extra goroutine.
	cmd := cp.Update(notesChangedMsg{})

	if cp.data == nil || len(cp.data.Notes) != 1 || cp.data.Notes[0].Title != "From agent" {
		t.Fatalf("expected notes to be re-read after notesChangedMsg, got %+v", cp.data)
	}
	if !cp.notesWatchPending {
		t.Errorf("expected notesWatchPending=true after re-arm, got false")
	}
	if cmd == nil {
		t.Errorf("expected re-arm cmd, got nil")
	}
}

// TestContextPanelNotesWatcherClosesOnDomainChange confirms SetDomain
// does not leak fsnotify descriptors — switching to a new domain closes
// the previous watcher and opens a fresh one.
func TestContextPanelNotesWatcherClosesOnDomainChange(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("AI_DATA_HOME", tmpDir)
	const profile = "ctx-switch-profile"

	cp := NewContextPanel(profile)
	cp.SetDomain("first")
	first := cp.notesWatcher
	if first == nil {
		t.Fatal("expected first watcher")
	}

	cp.SetDomain("second")
	second := cp.notesWatcher
	if second == nil {
		t.Fatal("expected second watcher")
	}
	if first == second {
		t.Errorf("expected a new watcher instance after SetDomain, got the same one")
	}

	// The first watcher's Events channel must be closed so the goroutine
	// that may be blocking on it returns. fsnotify.Watcher.Close() does
	// this. We can't poke the closed channel directly, but reading from a
	// closed channel returns the zero value immediately with !ok — which
	// is what watchNotesCmd checks to bail out.
	deadline := time.Now().Add(time.Second)
	for {
		select {
		case _, ok := <-first.Events:
			if ok {
				t.Errorf("expected first watcher Events to be closed, but got an event")
			}
			goto watcherClosed
		default:
			if time.Now().After(deadline) {
				t.Fatal("expected first watcher Events to close after SetDomain")
			}
			time.Sleep(time.Millisecond)
		}
	}

watcherClosed:

	cp.closeNotesWatcher()
}

// TestContextPanelNotesWatcherNilWhenNoDomain ensures we don't attach a
// watcher when SetDomain is called with an empty string (the panel
// renders a "No domain context" placeholder in that case).
func TestContextPanelNotesWatcherNilWhenNoDomain(t *testing.T) {
	cp := NewContextPanel("ctx-empty")
	cp.SetDomain("anywhere")
	if cp.notesWatcher == nil {
		t.Fatal("setup: expected watcher")
	}
	cp.SetDomain("")
	if cp.notesWatcher != nil {
		t.Errorf("expected watcher to be cleared when domain becomes empty, got %v", cp.notesWatcher)
	}
}

func TestContextPanelScrollClamp(t *testing.T) {
	cp := NewContextPanel("")
	cp.SetDomain("test-domain")
	cp.width = 40
	cp.height = 5 // tab bar takes 1, so 4 body lines visible

	// Simulate scrolling way past the end.
	cp.scrollOffset = 1000
	_ = cp.View(40, 5)

	if cp.scrollOffset != 0 {
		// If there is no body content (empty domain), offset resets to 0.
		// If there IS content, offset should clamp to maxOffset = len(lines)-4.
		// Either way it must not be 1000.
		t.Errorf("scrollOffset not clamped: got %d", cp.scrollOffset)
	}
}
