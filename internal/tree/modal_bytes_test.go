package tree

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestModalWidthFitsPanel(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	tr := New()
	tr.width = 40
	tr.height = 24
	tr.inputMode = true
	tr.inputPrompt = "Folder name"
	tr.inputValue = ""

	// Modal rendering is now handled by warp, not tree.
	// Tree's View() should NOT contain modal borders.
	out := tr.View(40, 24)
	lines := strings.Split(out, "\n")

	for _, line := range lines {
		if lipgloss.Width(line) > 40 {
			t.Errorf("line wider than panel: %d > 23: %q", lipgloss.Width(line), line)
		}
		if strings.Contains(line, "╭") {
			t.Errorf("modal border found in tree View() — modal should be rendered by warp, not tree: %q", line)
		}
	}
}
