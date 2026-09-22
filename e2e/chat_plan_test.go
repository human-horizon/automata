package e2e

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/starframe-dev/cue-tty/pkg/cue"
)

func TestChatOpensWithPlanPane(t *testing.T) {
	binary := os.Getenv("AUTOMATA_BIN")
	if binary == "" {
		binary = "../automata"
	}

	// Use a clean profile directory so repeated test runs do not accumulate
	// duplicate items in state.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("user home dir: %v", err)
	}
	profileDir := filepath.Join(home, ".ai", "automata", "profiles", "cue-test-plan")
	_ = os.RemoveAll(profileDir)

	app, err := cue.Launch(binary,
		cue.WithArgs("--profile", "cue-test-plan"),
		cue.WithSize(160, 55),
		cue.WithEnv("TERM=xterm-256color", "PI_SKIP_VERSION_CHECK=1"),
	)
	if err != nil {
		t.Fatalf("launch automata: %v", err)
	}
	defer app.Close()

	page := app.Page()
	page.WaitStable(100 * time.Millisecond)
	page.EnableMouse()
	page.WaitStable(500 * time.Millisecond)

	t.Cleanup(func() {
		if t.Failed() {
			if err := saveTestArtifact(page, "ChatPlanPane"); err != nil {
				t.Logf("save ChatPlanPane artifact: %v", err)
			}
		}
	})

	// Create a chat via the "+" button (after title, approx x=17).
	// Clicking it opens a popover at (17, 0); then click the "+ Chat" option
	// (second item, at x=18, y=2).
	page.MouseClick(17, 0)
	page.WaitStable(200 * time.Millisecond)
	page.MouseClick(18, 2)
	if err := page.WaitFor("Chat name", 2*time.Second); err != nil {
		t.Fatalf("chat creation modal did not open: %v", err)
	}
	page.Type("plan_chat_test")
	page.WaitStable(100 * time.Millisecond)
	page.Press("Enter")
	if err := page.WaitFor("plan_chat_test", 2*time.Second); err != nil {
		t.Fatalf("chat was not created: %v", err)
	}

	// Click the chat to open it.
	// Header is one row, so the first item is at row 1.
	page.MouseClick(4, 1)
	page.WaitStable(2 * time.Second)

	// Wait longer for ai-knowledge to render its first frame.
	if err := page.WaitFor("│", 5*time.Second); err != nil {
		t.Fatalf("chat layout missing vertical border: %v", err)
	}
	if err := page.WaitFor("Plans", 5*time.Second); err != nil {
		text, _ := page.Text()
		t.Fatalf("plan pane did not show Plans: %v\n%s", err, text)
	}
}
