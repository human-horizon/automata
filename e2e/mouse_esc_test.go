package e2e

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/starframe-dev/cue-tty/pkg/cue"
)

func TestMouseESCDebug(t *testing.T) {
	binary := os.Getenv("AUTOMATA_BIN")
	if binary == "" {
		binary = "../automata"
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("user home dir: %v", err)
	}
	profileDir := filepath.Join(home, ".ai", "automata", "profiles", "cue-test-esc")
	_ = os.RemoveAll(profileDir)

	app, err := cue.Launch(binary,
		cue.WithArgs("--profile", "cue-test-esc"),
		cue.WithSize(160, 55),
		cue.WithEnv("TERM=xterm-256color", "PI_SKIP_VERSION_CHECK=1"),
	)
	if err != nil {
		t.Fatalf("launch automata: %v", err)
	}
	defer func() {
		if err := app.Close(); err != nil {
			t.Errorf("close automata: %v", err)
		}
	}()

	page := app.Page()

	// Wait for the initial render without calling EnableMouse explicitly.
	// This simulates a real terminal where Bubble Tea emits its own mouse sequences.
	page.WaitStable(1 * time.Second)

	// Check initial render
	text, _ := page.Text()
	t.Logf("Initial screen (no mouse enable):\n%s", text)

	// Try clicking without EnableMouse; the terminal should rely only on
	// Bubble Tea's emitted mouse-mode escape sequences.
	t.Log("Clicking + button at (17, 0) WITHOUT explicit mouse enable")
	if err := page.MouseClick(17, 0); err != nil {
		t.Fatalf("click add button without explicit mouse enable: %v", err)
	}
	page.WaitStable(200 * time.Millisecond)
	// Click "+ Chat" popover item (second item, at x=18, y=2).
	if err := page.MouseClick(18, 2); err != nil {
		t.Fatalf("click chat option without explicit mouse enable: %v", err)
	}
	page.WaitStable(500 * time.Millisecond)

	text, _ = page.Text()
	t.Logf("After click without mouse enable:\n%s", text)

	// Check if modal opened
	if err := page.WaitFor("Chat name", 2*time.Second); err != nil {
		t.Logf("Chat name modal NOT found — mouse click didn't work without EnableMouse()")
		t.Logf("This means Bubbletea's WithMouseCellMotion() is NOT sending the right sequences")
	} else {
		t.Log("Chat creation modal opened — mouse click works without EnableMouse()!")
	}

	// Now enable mouse and try again
	if err := page.EnableMouse(); err != nil {
		t.Fatalf("enable mouse: %v", err)
	}
	page.WaitStable(500 * time.Millisecond)

	t.Log("Clicking + button at (17, 0) WITH explicit mouse enable")
	if err := page.MouseClick(17, 0); err != nil {
		t.Fatalf("click add button with explicit mouse enable: %v", err)
	}
	page.WaitStable(200 * time.Millisecond)
	// Click "+ Chat" popover item (second item, at x=18, y=2).
	if err := page.MouseClick(18, 2); err != nil {
		t.Fatalf("click chat option with explicit mouse enable: %v", err)
	}
	page.WaitStable(500 * time.Millisecond)

	text, _ = page.Text()
	t.Logf("After click with mouse enable:\n%s", text)

	if err := page.WaitFor("Chat name", 2*time.Second); err != nil {
		t.Logf("Chat name modal NOT found even with EnableMouse()")
	} else {
		t.Log("Chat creation modal opened with EnableMouse()!")
	}
}
