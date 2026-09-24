package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HumanHorizon/automata/internal/paths"
	"github.com/charmbracelet/x/ansi"
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

	// Isolate all profile and diagnostic data from the user's home.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AI_DATA_HOME", t.TempDir())
	if os.Getenv("AUTOMATA_E2E_ARTIFACTS_DIR") == "" {
		t.Setenv("AUTOMATA_E2E_ARTIFACTS_DIR", filepath.Join(t.TempDir(), "artifacts"))
	}
	const profile = "cue-test-collapse"
	statePath := paths.StatePath(profile)
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("create isolated profile: %v", err)
	}
	const stateFixture = `{"version":2,"items":[{"name":"collapse_chat","is_folder":false}]}`
	if err := os.WriteFile(statePath, []byte(stateFixture), 0o644); err != nil {
		t.Fatalf("seed isolated Tree state: %v", err)
	}

	piCommand := filepath.Join(t.TempDir(), "fake-pi")
	const piScript = "#!/bin/sh\nprintf 'AUTOMATA_CHAT_RESIZE_MARKER\\n'\nexec /bin/sh\n"
	if err := os.WriteFile(piCommand, []byte(piScript), 0o755); err != nil {
		t.Fatalf("write fake pi command: %v", err)
	}

	app, err := cue.Launch(binary,
		cue.WithArgs("--profile", profile),
		cue.WithSize(160, 55),
		cue.WithEnv("TERM=xterm-256color", "PI_SKIP_VERSION_CHECK=1", "PI_CMD="+piCommand),
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
			if err := saveTestArtifact(page, "ChatCollapse"); err != nil {
				t.Logf("save ChatCollapse artifact: %v", err)
			}
		}
	})

	if err := page.WaitFor("collapse_chat", 2*time.Second); err != nil {
		t.Fatalf("seeded chat was not rendered: %v", err)
	}

	// Open the seeded chat (first item is at row 1).
	page.MouseClick(4, 1)
	if err := page.WaitFor("AUTOMATA_CHAT_RESIZE_MARKER", 5*time.Second); err != nil {
		text, _ := page.Text()
		t.Fatalf("fake chat process did not start: %v\n%s", err, text)
	}
	page.WaitStable(200 * time.Millisecond)

	// Wait for the chat layout (vertical border between chat and knowledge).
	if err := page.WaitFor("│", 5*time.Second); err != nil {
		t.Fatalf("chat layout missing vertical border: %v", err)
	}

	// Snapshot the rendered layout before collapse. The fake chat marker's
	// terminal-cell column tracks the actual inner chat panel's left edge.
	before, _ := page.Text()
	beforeLeftEdge := leftEdgeOfChatContent(before, "AUTOMATA_CHAT_RESIZE_MARKER")
	beforeBorderCol := rightmostKnowledgeBorder(before)
	if beforeLeftEdge < 0 || beforeBorderCol < 0 {
		t.Fatalf("could not measure rendered chat layout before collapse: left=%d border=%d\n%s", beforeLeftEdge, beforeBorderCol, before)
	}
	beforeWidth := beforeBorderCol - beforeLeftEdge
	t.Logf("rendered chat BEFORE collapse: left=%d border=%d width=%d cells", beforeLeftEdge, beforeBorderCol, beforeWidth)

	// Click the collapse affordance on the expanded Tree border.
	page.MouseClick(29, 0)
	page.WaitStable(100 * time.Millisecond)

	if err := page.WaitFor("Knowledge", 2*time.Second); err != nil {
		text, _ := page.Text()
		t.Fatalf("app appears crashed after collapse: %v\n%s", err, text)
	}
	// Warp's parent resize and the nested chat/knowledge split redraw
	// asynchronously. Wait for the rendered geometry to converge instead of
	// sampling a transient frame between the outer collapse and inner resize.
	deadline := time.Now().Add(2 * time.Second)
	stableSamples := 0
	lastLeftEdge, lastBorderCol := -1, -1
	var after string
	var afterLeftEdge, afterBorderCol, afterWidth int
	for time.Now().Before(deadline) {
		after, _ = page.Text()
		afterLeftEdge = leftEdgeOfChatContent(after, "AUTOMATA_CHAT_RESIZE_MARKER")
		afterBorderCol = rightmostKnowledgeBorder(after)
		afterWidth = afterBorderCol - afterLeftEdge
		releasedWidth := beforeLeftEdge - afterLeftEdge
		grewBy := afterWidth - beforeWidth
		qualifies := afterLeftEdge >= 0 && afterBorderCol >= 0 &&
			releasedWidth > 0 && afterWidth > beforeWidth &&
			grewBy*100 >= releasedWidth*80
		if qualifies {
			if afterLeftEdge == lastLeftEdge && afterBorderCol == lastBorderCol {
				stableSamples++
			} else {
				stableSamples = 1
			}
		} else {
			stableSamples = 0
		}
		lastLeftEdge, lastBorderCol = afterLeftEdge, afterBorderCol
		if stableSamples >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	grewBy := afterWidth - beforeWidth
	releasedWidth := beforeLeftEdge - afterLeftEdge
	if stableSamples < 2 {
		t.Fatalf("inner chat split did not converge after Tree collapse: width %d -> %d, left edge %d -> %d, border=%d, growth=%d cells for %d released cells (need at least 80%%)\n%s", beforeWidth, afterWidth, beforeLeftEdge, afterLeftEdge, afterBorderCol, grewBy, releasedWidth, after)
	}
	t.Logf("rendered chat AFTER collapse: left=%d border=%d width=%d cells, growth=%d/%d released cells", afterLeftEdge, afterBorderCol, afterWidth, grewBy, releasedWidth)

	// Verify the application is still alive after collapse. This
	// catches nil-pointer or render-chain regressions that could
	// crash the app on toggle.
	if err := page.WaitFor("Knowledge", 2*time.Second); err != nil {
		text, _ := page.Text()
		t.Fatalf("app appears crashed after collapse: %v\n%s", err, text)
	}
}

// leftEdgeOfChatContent returns the terminal-cell column of the marker's first
// rendered occurrence, or -1 if it is absent.
func leftEdgeOfChatContent(screen, marker string) int {
	best := -1
	for _, line := range strings.Split(screen, "\n") {
		idx := strings.Index(line, marker)
		if idx < 0 {
			continue
		}
		col := ansi.StringWidth(line[:idx])
		if best < 0 || col < best {
			best = col
		}
	}
	return best
}

// rightmostKnowledgeBorder returns the terminal-cell column of the rightmost
// rendered "│Knowledge" separator, or -1 if it is absent.
func rightmostKnowledgeBorder(screen string) int {
	const separator = "│Knowledge"
	best := -1
	for _, line := range strings.Split(screen, "\n") {
		searchFrom := 0
		for searchFrom < len(line) {
			idx := strings.Index(line[searchFrom:], separator)
			if idx < 0 {
				break
			}
			absoluteIndex := searchFrom + idx
			col := ansi.StringWidth(line[:absoluteIndex])
			if col > best {
				best = col
			}
			searchFrom = absoluteIndex + len(separator)
		}
	}
	return best
}
