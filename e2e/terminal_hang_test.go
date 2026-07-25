package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/starframe-dev/cue-tty/pkg/cue"
)

type stateFile struct {
	Items []struct {
		Name           string   `json:"name"`
		IsTerminal     bool     `json:"is_terminal"`
		CWD            string   `json:"cwd"`
		CommandHistory []string `json:"command_history"`
	} `json:"items"`
}

func TestTerminalDoesNotHang(t *testing.T) {
	binary := os.Getenv("AUTOMATA_BIN")
	if binary == "" {
		binary = "../automata"
	}

	// Use a clean profile directory so repeated test runs do not accumulate
	// duplicate terminal items in state.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("user home dir: %v", err)
	}
	profileDir := filepath.Join(home, ".ai", "automata", "profiles", "cue-test")
	_ = os.RemoveAll(profileDir)

	app, err := cue.Launch(binary,
		cue.WithArgs("--profile", "cue-test"),
		cue.WithSize(160, 55),
		cue.WithEnv("TERM=xterm-256color", "PI_SKIP_VERSION_CHECK=1"),
	)
	if err != nil {
		t.Fatalf("launch automata: %v", err)
	}
	defer app.Close()

	page := app.Page()
	page.EnableMouse()
	page.WaitStable(500 * time.Millisecond)

	t.Cleanup(func() {
		if t.Failed() {
			page.SaveArtifact("test-artifacts", "TerminalHang")
		}
	})

	// Click "+" toolbar button (after title, approx x=17). Clicking it opens
	// a popover at (17, 0); then click the "+ Terminal" option (third item,
	// at x=18, y=3).
	page.MouseClick(17, 0)
	page.WaitStable(200 * time.Millisecond)
	page.MouseClick(18, 3)
	page.WaitStable(200 * time.Millisecond)

	// Type terminal name and confirm.
	page.Type("hangtest")
	page.WaitStable(100 * time.Millisecond)
	page.Press("Enter")
	page.WaitStable(500 * time.Millisecond)

	// Click on the newly created terminal item to open it.
	// Header is one row, so the first item is at row 1.
	page.MouseClick(4, 1)
	page.WaitStable(1 * time.Second)

	// The terminal should be created and selected; the right panel should
	// contain either a bash prompt or the terminal placeholder.
	text, _ := page.Text()
	t.Logf("screen after opening terminal:\n%s", text)

	// Give it a bit more time if bash is still starting.
	page.WaitStable(2 * time.Second)

	text, _ = page.Text()
	if len(text) == 0 {
		t.Fatal("empty screen after opening terminal")
	}

	// Try typing a command in the terminal panel.
	page.Type("echo ok")
	page.WaitStable(200 * time.Millisecond)
	page.Press("Enter")
	page.WaitStable(1 * time.Second)

	text, _ = page.Text()
	t.Logf("screen after typing command:\n%s", text)

	if err := cue.Expect(page).ToContain("ok"); err != nil {
		t.Fatalf("terminal did not echo command output: %v", err)
	}
}

func TestTerminalRestoresCWD(t *testing.T) {
	binary := os.Getenv("AUTOMATA_BIN")
	if binary == "" {
		binary = "../automata"
	}

	// Use a clean profile directory so repeated test runs do not accumulate
	// duplicate terminal items in state.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("user home dir: %v", err)
	}
	profileDir := filepath.Join(home, ".ai", "automata", "profiles", "cue-test-cwd")
	_ = os.RemoveAll(profileDir)

	app, err := cue.Launch(binary,
		cue.WithArgs("--profile", "cue-test-cwd"),
		cue.WithSize(160, 55),
		cue.WithEnv("TERM=xterm-256color", "PI_SKIP_VERSION_CHECK=1"),
	)
	if err != nil {
		t.Fatalf("launch automata: %v", err)
	}
	defer app.Close()

	page := app.Page()
	page.EnableMouse()
	page.WaitStable(500 * time.Millisecond)

	t.Cleanup(func() {
		if t.Failed() {
			page.SaveArtifact("test-artifacts", "TerminalCWD")
		}
	})

	// Create a terminal via the toolbar. The "+" button is after the title
	// (approx x=17). Clicking it opens a popover at (17, 0); then click the
	// "+ Terminal" option (third item, at x=18, y=3).
	page.MouseClick(17, 0)
	page.WaitStable(200 * time.Millisecond)
	page.MouseClick(18, 3)
	page.WaitStable(200 * time.Millisecond)
	page.Type("cwdtest")
	page.WaitStable(100 * time.Millisecond)
	page.Press("Enter")
	page.WaitStable(500 * time.Millisecond)

	// Open the terminal. With a fresh profile the only item is on row 1.
	page.MouseClick(4, 1)
	page.WaitStable(2 * time.Second)

	// Change directory and hit Enter.
	page.Type("cd /tmp")
	page.WaitStable(200 * time.Millisecond)
	page.Press("Enter")
	page.WaitStable(1 * time.Second)

	text, _ := page.Text()
	t.Logf("screen after cd:\n%s", text)
	if err := cue.Expect(page).ToContain("/tmp"); err != nil {
		t.Fatalf("cd /tmp did not update prompt: %v", err)
	}

	// Exit the shell with Ctrl+D.
	page.Press("Ctrl+D")
	page.WaitStable(2 * time.Second)

	text, _ = page.Text()
	t.Logf("screen after exit:\n%s", text)
	if err := cue.Expect(page).ToContain("AI"); err != nil {
		t.Logf("expected ASCII-art placeholder after stop, but not found: %v", err)
	}

	// Give the debounced save time to flush.
	page.WaitStable(2 * time.Second)

	// Verify state file persisted cwd and command history.
	statePath := filepath.Join(profileDir, "state.json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	var state stateFile
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("unmarshal state: %v", err)
	}

	var found bool
	for _, it := range state.Items {
		if it.Name == "cwdtest" && it.IsTerminal {
			found = true
			if it.CWD != "/tmp" {
				t.Fatalf("expected cwd /tmp, got %q", it.CWD)
			}
			if len(it.CommandHistory) == 0 || it.CommandHistory[len(it.CommandHistory)-1] != "cd /tmp" {
				t.Fatalf("expected command history to end with cd /tmp, got %v", it.CommandHistory)
			}
		}
	}
	if !found {
		t.Fatalf("terminal cwdtest not found in state: %s", string(data))
	}

	// Re-open the terminal and verify the shell starts in /tmp.
	// Return focus to the tree first because the terminal panel was focused.
	page.Press("F6")
	page.WaitStable(200 * time.Millisecond)
	page.MouseClick(4, 1)
	page.WaitStable(2 * time.Second)

	text, _ = page.Text()
	t.Logf("screen after reopen:\n%s", text)
	// The default macOS bash prompt shortens the path to the directory name.
	if err := cue.Expect(page).ToContain("Pro:tmp a$"); err != nil {
		t.Fatalf("terminal did not restore cwd /tmp: %v", err)
	}
}
