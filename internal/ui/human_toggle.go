package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func (cp *ChatPanel) humanButtonLabel(width int) string {
	if cp.activeIdx < 0 || cp.activeIdx >= len(cp.sessions) {
		return ""
	}
	session := cp.sessions[cp.activeIdx]
	if session == nil || session.em == nil {
		return ""
	}
	capable := false
	for _, value := range session.em.StartEnv() {
		if value == "AUTOMATA_HUMAN_EXPORT=1" {
			capable = true
			break
		}
	}
	if !capable {
		return ""
	}
	label := " ○ Human "
	if session.humanEnabled {
		label = " ● Human "
	}
	if width < ansi.StringWidth(label)+ansi.StringWidth(" × Clear ") {
		return ""
	}
	return label
}

func (cp *ChatPanel) toggleHuman() tea.Cmd {
	if cp.humanButtonLabel(cp.width) == "" {
		return nil
	}
	session := cp.sessions[cp.activeIdx]
	session.humanEnabled = !session.humanEnabled
	if !session.humanEnabled {
		if session.human != nil {
			session.human.selected = ""
			session.human.deactivate()
		}
		return nil
	}
	return cp.activateHuman()
}

func (cp *ChatPanel) humanWarning() string {
	if cp.activeIdx < 0 || cp.activeIdx >= len(cp.sessions) {
		return ""
	}
	session := cp.sessions[cp.activeIdx]
	if session == nil || !session.humanEnabled || session.human == nil {
		return ""
	}
	h := session.human
	for _, warning := range []string{h.diagnostic, h.watchError, h.fallback} {
		if warning != "" {
			return strings.ReplaceAll(strings.ReplaceAll(humanText(warning), "\n", "; "), "\t", " ")
		}
	}
	return ""
}
