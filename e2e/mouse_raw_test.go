package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/starframe-dev/cue-tty/pkg/cue"
)

func TestMouseRawDebug(t *testing.T) {
	binary := os.Getenv("AUTOMATA_BIN")
	if binary == "" {
		binary = "../automata"
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("user home dir: %v", err)
	}
	profileDir := filepath.Join(home, ".ai", "automata", "profiles", "cue-test-raw")
	_ = os.RemoveAll(profileDir)

	app, err := cue.Launch(binary,
		cue.WithArgs("--profile", "cue-test-raw"),
		cue.WithSize(160, 55),
		cue.WithEnv("TERM=xterm-256color", "PI_SKIP_VERSION_CHECK=1"),
	)
	if err != nil {
		t.Fatalf("launch automata: %v", err)
	}
	defer app.Close()

	page := app.Page()

	// Wait for initial render — Bubbletea should send mouse enable sequences
	// during initialization (WithMouseCellMotion).
	page.WaitStable(1 * time.Second)

	// Check the raw log to see if Bubbletea sent the mouse enable sequences.
	rawLog := string(app.RawLog())
	t.Logf("Raw log length: %d bytes", len(rawLog))

	// Check for mouse enable sequences
	has1002h := strings.Contains(rawLog, "\x1b[?1002h")
	has1006h := strings.Contains(rawLog, "\x1b[?1006h")
	has1000h := strings.Contains(rawLog, "\x1b[?1000h")

	t.Logf("Has ESC[?1002h (cell motion): %v", has1002h)
	t.Logf("Has ESC[?1006h (SGR mode): %v", has1006h)
	t.Logf("Has ESC[?1000h (normal tracking): %v", has1000h)

	if !has1002h {
		t.Log("WARNING: ESC[?1002h NOT found in raw log!")
		t.Log("Bubbletea's WithMouseCellMotion() may not be sending the sequence.")
		// Show first 500 bytes of raw log for debugging
		preview := rawLog
		if len(preview) > 500 {
			preview = preview[:500]
		}
		t.Logf("Raw log preview: %q", preview)
	}
	if !has1006h {
		t.Log("WARNING: ESC[?1006h NOT found in raw log!")
		t.Log("SGR mouse mode may not be enabled.")
	}

	// Now test mouse clicks WITHOUT page.EnableMouse().
	// In a real terminal, Bubbletea's sequences should have enabled mouse tracking.
	// cue-tty's MouseClick sends SGR sequences directly, so it should work
	// regardless of whether mouse tracking was enabled.
	t.Log("")
	t.Log("=== Testing mouse click WITHOUT EnableMouse() ===")
	page.MouseClick(17, 0)
	page.WaitStable(200 * time.Millisecond)
	// Click "+ Chat" popover item (second item, at x=18, y=2).
	page.MouseClick(18, 2)
	page.WaitStable(500 * time.Millisecond)

	text, _ := page.Text()
	if strings.Contains(text, "Chat name") {
		t.Log("✅ Mouse click works WITHOUT EnableMouse()!")
	} else {
		t.Log("❌ Mouse click did NOT work without EnableMouse()")
		t.Log("Current screen:")
		t.Log(text)
	}

	// Now test WITH page.EnableMouse()
	t.Log("")
	t.Log("=== Testing mouse click WITH EnableMouse() ===")
	page.EnableMouse()
	page.WaitStable(500 * time.Millisecond)

	page.MouseClick(17, 0)
	page.WaitStable(200 * time.Millisecond)
	// Click "+ Chat" popover item (second item, at x=18, y=2).
	page.MouseClick(18, 2)
	page.WaitStable(500 * time.Millisecond)

	text, _ = page.Text()
	if strings.Contains(text, "Chat name") {
		t.Log("✅ Mouse click works WITH EnableMouse()!")
	} else {
		t.Log("❌ Mouse click did NOT work with EnableMouse()")
		t.Log("Current screen:")
		t.Log(text)
	}

	// Save artifacts for debugging
	if err := saveTestArtifact(page, "MouseRawDebug"); err != nil {
		t.Logf("save MouseRawDebug artifact: %v", err)
	}
}
