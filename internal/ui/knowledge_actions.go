package ui

import (
	"context"
	"errors"
	"fmt"
	"log"
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

// actionRunKey identifies one folder action; the same action can run in
// different profiles or folder-domains without sharing its state.
type actionRunKey struct {
	profile  string
	domain   string
	actionID string
}

// actionRun tracks one in-flight folder action command.
type actionRun struct {
	token    uint64
	name     string
	cancel   context.CancelFunc
	stopping bool
}

func (k *KnowledgePanel) confirmPendingAction() tea.Cmd {
	if k.pendingAction == nil {
		return nil
	}
	action := *k.pendingAction
	key := actionRunKey{profile: k.profile, domain: k.domain, actionID: action.ID}
	if _, running := k.actionRuns[key]; running {
		k.cancelPendingAction()
		return nil
	}
	runner := k.actionRunner
	if runner == nil {
		runner = func(ctx context.Context, action akactions.Action) error {
			return executeSystemAction(ctx, action, runShellAction)
		}
	}
	k.cancelPendingAction()
	if k.actionRuns == nil {
		k.actionRuns = make(map[actionRunKey]*actionRun)
	}
	k.actionRunToken++
	token := k.actionRunToken
	ctx, cancel := context.WithCancel(context.Background())
	k.actionRuns[key] = &actionRun{token: token, name: action.Name, cancel: cancel}
	k.actionStatus = "Running: " + action.Name
	k.actionStatusIsError = false
	return func() tea.Msg {
		return actionRunCompletedMsg{
			token: token,
			key:   key,
			name:  action.Name,
			err:   runner(ctx, action),
		}
	}
}

// isActionRunning reports whether the action in the current profile and
// folder-domain has an in-flight command.
func (k *KnowledgePanel) isActionRunning(action akactions.Action) bool {
	_, running := k.actionRuns[actionRunKey{profile: k.profile, domain: k.domain, actionID: action.ID}]
	return running
}

// runningActionIDs returns the IDs of the current folder's running actions.
func (k *KnowledgePanel) runningActionIDs() map[string]bool {
	ids := make(map[string]bool)
	for key := range k.actionRuns {
		if key.profile == k.profile && key.domain == k.domain {
			ids[key.actionID] = true
		}
	}
	return ids
}

// stopAction cancels a running action command. The button stays highlighted
// until the runner returns, so the status reflects the real process state.
func (k *KnowledgePanel) stopAction(action akactions.Action) {
	run := k.actionRuns[actionRunKey{profile: k.profile, domain: k.domain, actionID: action.ID}]
	if run == nil || run.stopping {
		return
	}
	run.stopping = true
	run.cancel()
	k.actionStatus = "Stopping: " + action.Name
	k.actionStatusIsError = false
}

// handleActionRunCompleted clears the in-flight state for a finished run and
// reports its outcome when the run belongs to the visible folder.
func (k *KnowledgePanel) handleActionRunCompleted(msg actionRunCompletedMsg) {
	run := k.actionRuns[msg.key]
	if run == nil || run.token != msg.token {
		return
	}
	delete(k.actionRuns, msg.key)
	run.cancel()
	if msg.key.profile != k.profile || msg.key.domain != k.domain {
		return
	}
	k.actionStatusIsError = false
	switch {
	case errors.Is(msg.err, context.Canceled):
		k.actionStatus = fmt.Sprintf("Stopped: %s", msg.name)
	case msg.err != nil:
		k.actionStatusIsError = true
		k.actionStatus = fmt.Sprintf("Failed: %s: %v", msg.name, msg.err)
		log.Printf("automata: folder action %q failed: %v", msg.name, msg.err)
	default:
		k.actionStatus = fmt.Sprintf("Completed: %s", msg.name)
	}
}

// cancelActionRuns stops every in-flight folder action owned by the panel.
func (k *KnowledgePanel) cancelActionRuns() {
	for key, run := range k.actionRuns {
		run.cancel()
		delete(k.actionRuns, key)
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
