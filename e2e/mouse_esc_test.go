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
	defer app.Close()

	page := app.Page()

	// Wait for initial render WITHOUT enabling mouse via page.EnableMouse().
	// This simulates a real terminal where Bubbletea sends the mouse sequences.
	page.WaitStable(1 * time.Second)

	// Check initial render
	text, _ := page.Text()
	t.Logf("Initial screen (no mouse enable):\n%s", text)

	// Try clicking WITHOUT page.EnableMouse() — this simulates a real terminal
	// where the user clicks and the terminal sends mouse events based on
	// Bubbletea's ESC sequences.
	t.Log("Clicking + button at (17, 0) WITHOUT explicit mouse enable")
	page.MouseClick(17, 0)
	page.WaitStable(200 * time.Millisecond)
	// Click "+ Chat" popover item (second item, at x=18, y=2).
	page.MouseClick(18, 2)
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
	page.EnableMouse()
	page.WaitStable(500 * time.Millisecond)

	t.Log("Clicking + button at (17, 0) WITH explicit mouse enable")
	page.MouseClick(17, 0)
	page.WaitStable(200 * time.Millisecond)
	// Click "+ Chat" popover item (second item, at x=18, y=2).
	page.MouseClick(18, 2)
	page.WaitStable(500 * time.Millisecond)

	text, _ = page.Text()
	t.Logf("After click with mouse enable:\n%s", text)

	if err := page.WaitFor("Chat name", 2*time.Second); err != nil {
		t.Logf("Chat name modal NOT found even with EnableMouse()")
	} else {
		t.Log("Chat creation modal opened with EnableMouse()!")
	}
}
