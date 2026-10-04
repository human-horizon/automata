package ui

import (
	"strconv"
	"strings"

	akactions "github.com/HumanHorizon/automata/internal/ai-knowledge/actions"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func (k *KnowledgePanel) openActionConfirmation(action akactions.Action) {
	copy := action
	k.pendingAction = &copy
	k.actionConfirmScroll = 0
	k.actionConfirmButtonY = -1
	k.actionConfirmRunRange = terminalCellRange{}
	k.actionConfirmNoRange = terminalCellRange{}
}

func (k *KnowledgePanel) cancelPendingAction() {
	k.pendingAction = nil
	k.actionConfirmScroll = 0
	k.actionConfirmButtonY = -1
	k.actionConfirmRunRange = terminalCellRange{}
	k.actionConfirmNoRange = terminalCellRange{}
}

func (k *KnowledgePanel) confirmPendingAction() tea.Cmd {
	if k.pendingAction == nil {
		return nil
	}
	action := *k.pendingAction
	profile, domain := k.profile, k.domain
	k.actionRunGeneration++
	generation := k.actionRunGeneration
	runner := k.actionRunner
	if runner == nil {
		runner = func(action akactions.Action) error {
			return executeSystemAction(action, runShellAction)
		}
	}
	k.cancelPendingAction()
	k.actionStatus = "Running: " + action.Name
	k.actionStatusIsError = false
	return func() tea.Msg {
		return actionRunCompletedMsg{
			generation: generation,
			profile:    profile,
			domain:     domain,
			name:       action.Name,
			err:        runner(action),
		}
	}
}

func actionConfirmationLines(action akactions.Action, width int) []string {
	lines := []string{
		"Confirm this folder action?",
		"Name (quoted):",
	}
	lines = append(lines, wrapActionText("  ", strconv.QuoteToGraphic(action.Name), width)...)
	lines = append(lines, "Exact command (quoted):")
	lines = append(lines, wrapActionText("  ", strconv.QuoteToGraphic(action.Command), width)...)
	lines = append(lines, "Working directory (quoted):")
	lines = append(lines, wrapActionText("  ", strconv.QuoteToGraphic(action.CWD), width)...)
	return lines
}

func wrapActionText(prefix, text string, width int) []string {
	if width <= 0 {
		width = 80
	}
	prefixWidth := lipgloss.Width(prefix)
	if prefixWidth >= width {
		prefix = ""
		prefixWidth = 0
	}
	lines := make([]string, 0, 1)
	line := prefix
	lineWidth := prefixWidth
	for _, r := range text {
		runeText := string(r)
		runeWidth := lipgloss.Width(runeText)
		if lineWidth+runeWidth > width && lineWidth > prefixWidth {
			lines = append(lines, line)
			line = strings.Repeat(" ", prefixWidth)
			lineWidth = prefixWidth
		}
		line += runeText
		lineWidth += runeWidth
	}
	if line != "" || len(lines) == 0 {
		lines = append(lines, line)
	}
	return lines
}

func (k *KnowledgePanel) renderActionConfirmation(header string) string {
	if k.height < 2 {
		return header
	}
	lines := actionConfirmationLines(*k.pendingAction, k.width)
	contentHeight := k.height - 2
	maxOffset := len(lines) - contentHeight
	if maxOffset < 0 {
		maxOffset = 0
	}
	if k.actionConfirmScroll > maxOffset {
		k.actionConfirmScroll = maxOffset
	}
	if k.actionConfirmScroll < 0 {
		k.actionConfirmScroll = 0
	}
	start := k.actionConfirmScroll
	end := start + contentHeight
	if end > len(lines) {
		end = len(lines)
	}
	body := append([]string(nil), lines[start:end]...)
	for len(body) < contentHeight {
		body = append(body, strings.Repeat(" ", k.width))
	}

	runLabel, cancelLabel := "[Run]", "[Cancel]"
	runStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(k.palette.Success))
	cancelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(k.palette.Error))
	buttonLine := runStyle.Render(runLabel) + "  " + cancelStyle.Render(cancelLabel)
	body = append(body, ansi.Truncate(buttonLine, k.width, "…"))
	k.actionConfirmButtonY = k.height - 1
	runWidth := ansi.StringWidth(runLabel)
	k.actionConfirmRunRange = visibleCellRange(0, runWidth, k.width)
	k.actionConfirmNoRange = visibleCellRange(runWidth+2, ansi.StringWidth(cancelLabel), k.width)
	return header + "\n" + strings.Join(body, "\n")
}
