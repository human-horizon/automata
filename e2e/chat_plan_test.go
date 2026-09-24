package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
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
	if err := page.WaitFor("Automata", 5*time.Second); err != nil {
		text, _ := page.Text()
		t.Fatalf("Automata header did not render: %v\n%s", err, text)
	}

	t.Cleanup(func() {
		if t.Failed() {
			if err := saveTestArtifact(page, "ChatPlanPane"); err != nil {
				t.Logf("save ChatPlanPane artifact: %v", err)
			}
		}
	})

	// Find the visible toolbar button and menu item from terminal output so a
	// long profile name cannot invalidate fixed screen coordinates.
	lines, err := page.Lines()
	if err != nil || len(lines) == 0 {
		t.Fatalf("read Automata header: lines=%v, err=%v", lines, err)
	}
	plusByteColumn := strings.LastIndex(lines[0], "+")
	if plusByteColumn < 0 {
		t.Fatalf("toolbar + button is missing from header:\n%s", strings.Join(lines, "\n"))
	}
	plusColumn := ansi.StringWidth(lines[0][:plusByteColumn])
	page.MouseClick(plusColumn, 0)
	if err := page.WaitFor("+ Chat", 2*time.Second); err != nil {
		text, _ := page.Text()
		t.Fatalf("chat creation menu did not open: %v\n%s", err, text)
	}
	lines, err = page.Lines()
	if err != nil {
		t.Fatalf("read chat creation menu: %v", err)
	}
	chatRow, chatColumn := -1, -1
	for row, line := range lines {
		if byteColumn := strings.Index(line, "+ Chat"); byteColumn >= 0 {
			chatRow, chatColumn = row, ansi.StringWidth(line[:byteColumn])
			break
		}
	}
	if chatRow < 0 {
		t.Fatalf("+ Chat option is missing from menu:\n%s", strings.Join(lines, "\n"))
	}
	page.MouseClick(chatColumn+1, chatRow)
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
