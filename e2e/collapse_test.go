package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/starframe-dev/cue-tty/pkg/cue"
)

// TestChatResizesOnTreeCollapse verifies that clicking the "<" collapse
// button on the tree border rebroadcasts a ResizeMsg so the chat terminal
// expands into the freed space and the application stays alive.
// Regression test for "chat does not resize when tree panel is collapsed".
func TestChatResizesOnTreeCollapse(t *testing.T) {
	binary := os.Getenv("AUTOMATA_BIN")
	if binary == "" {
		binary = "../automata"
	}

	// Clean profile so repeated test runs do not accumulate duplicates.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("user home dir: %v", err)
	}
	profileDir := filepath.Join(home, ".ai", "automata", "profiles", "cue-test-collapse")
	_ = os.RemoveAll(profileDir)

	app, err := cue.Launch(binary,
		cue.WithArgs("--profile", "cue-test-collapse"),
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
			page.SaveArtifact("test-artifacts", "ChatCollapse")
		}
	})

	// Create a chat via the toolbar.
	page.MouseClick(17, 0)
	page.WaitStable(200 * time.Millisecond)
	page.MouseClick(18, 2)
	if err := page.WaitFor("Chat name", 2*time.Second); err != nil {
		t.Fatalf("chat creation modal did not open: %v", err)
	}
	page.Type("collapse_chat")
	page.WaitStable(100 * time.Millisecond)
	page.Press("Enter")
	if err := page.WaitFor("collapse_chat", 2*time.Second); err != nil {
		t.Fatalf("chat was not created: %v", err)
	}

	// Open the chat (first item is at row 1).
	page.MouseClick(4, 1)
	page.WaitStable(2 * time.Second)

	// Wait for the chat layout (vertical border between chat and knowledge).
	if err := page.WaitFor("│", 5*time.Second); err != nil {
		t.Fatalf("chat layout missing vertical border: %v", err)
	}

	// Snapshot the screen BEFORE collapse.
	before, _ := page.Text()
	t.Logf("screen before collapse:\n%s", before)

	// Measure the chat panel width by locating the chat/knowledge border
	// (the column of "│Knowledge" in the header row). Subtract the tree
	// width: 30 chars when the tree is expanded, 1 when collapsed. This
	// yields the chat width. With the fix, after collapse the chat grows
	// from ~88 chars to ~118 chars.
	beforeBorderCol := rightmostKnowledgeBorder(before)
	treeWidthExpanded := 30
	beforeWidth := beforeBorderCol - treeWidthExpanded
	t.Logf("chat width BEFORE collapse: %d chars (border at col %d)", beforeWidth, beforeBorderCol)

	// Click the "<" collapse button on row 0. From the rendered layout
	// the button sits right after the toolbar "+" — we probe x=29 which
	// is the column where "<" actually renders for this profile name.
	page.MouseClick(29, 0)
	page.WaitStable(500 * time.Millisecond)

	// Snapshot AFTER collapse.
	after, _ := page.Text()
	t.Logf("screen after collapse:\n%s", after)

	// Application must still be alive (knowledge panel header still
	// visible) — this catches the regression where a nil pointer or
	// broken resize chain caused a crash on toggle.
	if err := page.WaitFor("Knowledge", 2*time.Second); err != nil {
		text, _ := page.Text()
		t.Fatalf("app appears crashed after collapse: %v\n%s", err, text)
	}

	afterBorderCol := rightmostKnowledgeBorder(after)
	treeWidthCollapsed := 1
	afterWidth := afterBorderCol - treeWidthCollapsed
	t.Logf("chat width AFTER collapse: %d chars (border at col %d)", afterWidth, afterBorderCol)

	if beforeBorderCol < 0 {
		t.Fatalf("could not locate chat/knowledge border before collapse")
	}
	if afterBorderCol < 0 {
		t.Fatalf("could not locate chat/knowledge border after collapse")
	}

	// Without the ResizeMsg broadcast fix, warp renders the collapsed
	// outer layout but the inner chat/knowledge split keeps its
	// pre-collapse fraction, so the chat grows by only ~9 chars
	// instead of ~30. With the fix it grows by ~28 chars.
	grewBy := afterWidth - beforeWidth
	if grewBy < 20 {
		t.Fatalf("chat did not expand fully after collapse: width %d -> %d (grew by only %d chars, expected ~30)", beforeWidth, afterWidth, grewBy)
	}

	// Verify the application is still alive after collapse. This
	// catches nil-pointer or render-chain regressions that could
	// crash the app on toggle.
	if err := page.WaitFor("Knowledge", 2*time.Second); err != nil {
		text, _ := page.Text()
		t.Fatalf("app appears crashed after collapse: %v\n%s", err, text)
	}
}

// leftEdgeOfChatContent returns the rune column at which needle first
// appears on any line of the screen text, or -1 if not found. The chat
// starts after the tree panel so the needle's column equals the chat's
// left edge: ~TreeWidth+1 when the tree is expanded, ~1 when collapsed.
func leftEdgeOfChatContent(screen, needle string) int {
	lines := strings.Split(screen, "\n")
	best := -1
	for _, line := range lines {
		idx := strings.Index(line, needle)
		if idx < 0 {
			continue
		}
		col := utf8RuneCount(line[:idx])
		if best < 0 || col < best {
			best = col
		}
	}
	return best
}

// rightmostKnowledgeBorder returns the rune column of the rightmost
// occurrence of "│Knowledge" in the screen text, or -1 if not found.
// This is the chat/knowledge separator; the chat panel ends at this
// column.
func rightmostKnowledgeBorder(screen string) int {
	const sep = "│Knowledge"
	best := -1
	searchFrom := 0
	for searchFrom < len(screen) {
		idx := strings.Index(screen[searchFrom:], sep)
		if idx < 0 {
			break
		}
		absIdx := searchFrom + idx
		col := utf8RuneCount(screen[:absIdx])
		if col > best {
			best = col
		}
		searchFrom = absIdx + len(sep)
	}
	return best
}

// decodeRune decodes the first UTF-8 rune from b and returns it with its
// byte size. Falls back to a single byte for invalid sequences.
func decodeRune(b string) (rune, int) {
	if len(b) == 0 {
		return 0, 0
	}
	switch {
	case b[0]&0x80 == 0:
		return rune(b[0]), 1
	case b[0]&0xE0 == 0xC0 && len(b) >= 2:
		return rune(b[0]&0x1F)<<6 | rune(b[1]&0x3F), 2
	case b[0]&0xF0 == 0xE0 && len(b) >= 3:
		return rune(b[0]&0x0F)<<12 | rune(b[1]&0x3F)<<6 | rune(b[2]&0x3F), 3
	case b[0]&0xF8 == 0xF0 && len(b) >= 4:
		return rune(b[0]&0x07)<<18 | rune(b[1]&0x3F)<<12 | rune(b[2]&0x3F)<<6 | rune(b[3]&0x3F), 4
	}
	return rune(b[0]), 1
}

// utf8RuneCount counts the number of runes in s.
func utf8RuneCount(s string) int {
	n := 0
	for i := 0; i < len(s); {
		_, size := decodeRune(s[i:])
		i += size
		n++
	}
	return n
}
