package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/starframe-dev/cue-tty/pkg/cue"
)

func projectRoot() string {
	_, file, _, _ := runtime.Caller(0)
	root, err := filepath.Abs(filepath.Join(filepath.Dir(file), ".."))
	if err != nil {
		panic(err)
	}
	return root
}

// sessionStateItem mirrors internal/tree.StateItem for test fixtures.
type sessionStateItem struct {
	Name     string             `json:"name"`
	IsFolder bool               `json:"is_folder"`
	Expanded bool               `json:"expanded,omitempty"`
	Items    []sessionStateItem `json:"items,omitempty"`
}

// sessionState mirrors internal/tree.TreeState for test fixtures.
type sessionState struct {
	Version int                `json:"version"`
	Items   []sessionStateItem `json:"items"`
}

// writeSessionFixture creates a clean profile and writes a known tree state.
func writeSessionFixture(t *testing.T, profile string, items []sessionStateItem) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("user home dir: %v", err)
	}
	dir := filepath.Join(home, ".ai", "automata", "profiles", profile)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove profile dir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create profile dir: %v", err)
	}
	state := sessionState{Version: 1, Items: items}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	statePath := filepath.Join(dir, "state.json")
	if err := os.WriteFile(statePath, data, 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}
	return dir
}

func launchAutomataForSession(t *testing.T, profile string) (*cue.App, *cue.Page) {
	t.Helper()
	root := projectRoot()
	app, err := cue.Launch(filepath.Join(root, "automata"),
		cue.WithArgs("--profile", profile),
		cue.WithDir(root),
		cue.WithSize(120, 40),
		cue.WithEnv("TERM=xterm-256color", "PI_SKIP_VERSION_CHECK=1", "PI_CMD=/bin/bash"),
	)
	if err != nil {
		t.Fatalf("launch automata: %v", err)
	}
	page := app.Page()
	page.WaitStable(500 * time.Millisecond)
	return app, page
}

func waitForTerminalPrompt(t *testing.T, page *cue.Page) {
	t.Helper()
	const maxWait = 10
	for i := 0; i < maxWait; i++ {
		text, _ := page.Text()
		if strings.Contains(text, "bash") {
			t.Logf("Terminal detected at attempt %d", i+1)
			return
		}
		if maxWait-i-1 > 0 {
			page.WaitStable(500 * time.Millisecond)
		}
	}
	t.Fatalf("terminal prompt did not appear")
}

func writeSessionMarker(page *cue.Page) {
	const marker = "/tmp/automata-e2e-session-id.txt"
	cmd := `echo "$AUTOMATA_SESSION_ID" > ` + marker + "\n"
	page.Type(cmd)
	page.WaitStable(300 * time.Millisecond)
	page.Press("Enter")
	page.WaitStable(1 * time.Second)
}

func readSessionMarker(t *testing.T) string {
	t.Helper()
	const marker = "/tmp/automata-e2e-session-id.txt"
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read session marker: %v", err)
	}
	return strings.TrimSpace(string(data))
}

func TestRenameChatWithF2(t *testing.T) {
	profile := "cue-test-rename"
	profileDir := writeSessionFixture(t, profile, []sessionStateItem{
		{Name: "Old Chat", IsFolder: false},
	})

	app, page := launchAutomataForSession(t, profile)
	defer app.Close()

	page.Press("F6")
	page.WaitStable(100 * time.Millisecond)
	page.Press("Down")
	page.WaitStable(100 * time.Millisecond)
	page.Press("F2")
	page.WaitStable(100 * time.Millisecond)
	for range "Old Chat" {
		page.Press("Backspace")
	}
	page.Type("Renamed Chat")
	page.Press("Enter")
	page.WaitStable(300 * time.Millisecond)

	text, err := page.Text()
	if err != nil {
		t.Fatalf("read screen after rename: %v", err)
	}
	if !strings.Contains(text, "Renamed Chat") {
		t.Fatalf("renamed chat is not visible:\n%s", text)
	}

	data, err := os.ReadFile(filepath.Join(profileDir, "state.json"))
	if err != nil {
		t.Fatalf("read renamed state: %v", err)
	}
	var state sessionState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("decode renamed state: %v", err)
	}
	if len(state.Items) != 1 || state.Items[0].Name != "Renamed Chat" {
		t.Fatalf("state name = %#v, want Renamed Chat", state.Items)
	}
}

func TestRootChatSessionID(t *testing.T) {
	_ = writeSessionFixture(t, "cue-test-session", []sessionStateItem{
		{Name: "Общие вопросы", IsFolder: false},
	})

	app, page := launchAutomataForSession(t, "cue-test-session")
	defer app.Close()

	// Select the only chat and open it.
	page.Press("Down")
	page.WaitStable(100 * time.Millisecond)
	page.Press("Enter")
	page.WaitStable(500 * time.Millisecond)

	waitForTerminalPrompt(t, page)
	writeSessionMarker(page)

	want := "cue-test-session__obschie-voprosy"
	got := readSessionMarker(t)
	if got != want {
		t.Errorf("Session ID = %q, want %q", got, want)
	}
}

func TestFolderChatSessionID(t *testing.T) {
	_ = writeSessionFixture(t, "cue-test-session-folder", []sessionStateItem{
		{
			Name:     "Сегодня",
			IsFolder: true,
			Expanded: true,
			Items: []sessionStateItem{
				{Name: "My Chat", IsFolder: false},
			},
		},
	})

	app, page := launchAutomataForSession(t, "cue-test-session-folder")
	defer app.Close()

	// Select "My Chat" inside "Сегодня" (first and only child).
	page.Press("Down")
	page.WaitStable(100 * time.Millisecond)
	page.Press("Down")
	page.WaitStable(100 * time.Millisecond)
	page.Press("Enter")
	page.WaitStable(500 * time.Millisecond)

	waitForTerminalPrompt(t, page)
	writeSessionMarker(page)

	want := "cue-test-session-folder__segodnya.my-chat"
	got := readSessionMarker(t)
	if got != want {
		t.Errorf("Session ID = %q, want %q", got, want)
	}
}

func TestUniqueSessionID(t *testing.T) {
	_ = writeSessionFixture(t, "cue-test-session-unique", []sessionStateItem{
		{
			Name:     "Сегодня",
			IsFolder: true,
			Expanded: true,
			Items: []sessionStateItem{
				{Name: "My Chat", IsFolder: false},
			},
		},
		{
			Name:     "Проекты",
			IsFolder: true,
			Expanded: true,
			Items: []sessionStateItem{
				{Name: "My Chat", IsFolder: false},
			},
		},
	})

	app, page := launchAutomataForSession(t, "cue-test-session-unique")
	defer app.Close()

	// Flat list: Сегодня(0), My Chat in Сегодня(1), Проекты(2), My Chat in Проекты(3).

	// Open first "My Chat" (Сегодня is row 0, My Chat is row 1).
	page.Press("Down")
	page.WaitStable(100 * time.Millisecond)
	page.Press("Down")
	page.WaitStable(100 * time.Millisecond)
	page.Press("Enter")
	page.WaitStable(500 * time.Millisecond)

	waitForTerminalPrompt(t, page)
	writeSessionMarker(page)
	first := readSessionMarker(t)

	// Return focus to tree and click the second "My Chat".
	// Flat list rows: header(0), Сегодня(1), My Chat(2), Проекты(3), My Chat(4).
	page.Press("F6")
	page.WaitStable(200 * time.Millisecond)
	page.MouseClick(4, 4)
	page.WaitStable(200 * time.Millisecond)
	page.Press("Enter")
	page.WaitStable(500 * time.Millisecond)

	waitForTerminalPrompt(t, page)
	writeSessionMarker(page)
	second := readSessionMarker(t)

	if first == second {
		t.Fatalf("expected different session IDs, got %q twice", first)
	}

	wantFirst := "cue-test-session-unique__segodnya.my-chat"
	wantSecond := "cue-test-session-unique__proekty.my-chat"
	if first != wantFirst {
		t.Errorf("first session ID = %q, want %q", first, wantFirst)
	}
	if second != wantSecond {
		t.Errorf("second session ID = %q, want %q", second, wantSecond)
	}
}
