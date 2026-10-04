package ui

import (
	"strings"
	"testing"

	"github.com/Starframe/portalis"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func newHumanTestChat() *ChatPanel {
	em := portalis.NewEmulator("owner", "Main", "/bin/sh", nil)
	em.SetStartEnv([]string{"AUTOMATA_HUMAN_EXPORT=1"})
	panel := NewChatPanel(em, "owner", "test")
	panel.width, panel.height = 70, 12
	return panel
}

func TestHumanButtonIsBesideClearAndHasIndependentHitbox(t *testing.T) {
	panel := newHumanTestChat()
	clears := 0
	panel.SetOnClearSession(func(string, string) tea.Cmd {
		clears++
		return nil
	})
	bar := ansi.Strip(panel.renderTabBar(panel.width))
	if !strings.Contains(bar, " ○ Human  × Clear ") || panel.sessions[0].humanEnabled {
		t.Fatalf("initial Human button = %q", bar)
	}
	panel.handleTabBarClick(tea.MouseMsg(tea.MouseEvent{
		X: panel.width - ansi.StringWidth(" × Clear ") - 3,
		Y: panel.height - 1,
	}))
	if !panel.sessions[0].humanEnabled || clears != 0 {
		t.Fatalf("Human click: enabled=%v, clears=%d", panel.sessions[0].humanEnabled, clears)
	}
	panel.handleTabBarClick(tea.MouseMsg(tea.MouseEvent{X: panel.width - 2, Y: panel.height - 1}))
	if clears != 1 || !panel.sessions[0].humanEnabled {
		t.Fatalf("Clear click: enabled=%v, clears=%d", panel.sessions[0].humanEnabled, clears)
	}
}

func TestHumanButtonDoesNotAppearOnShellOrTooNarrowPanel(t *testing.T) {
	panel := NewChatPanel(portalis.NewEmulator("shell", "Main", "/bin/sh", nil), "shell", "")
	if strings.Contains(panel.renderTabBar(70), "Human") {
		t.Fatal("shell exposes Human")
	}
	panel = newHumanTestChat()
	for width := 1; width < ansi.StringWidth(" ○ Human  × Clear "); width++ {
		bar := panel.renderTabBar(width)
		if strings.Contains(bar, "Human") || ansi.StringWidth(bar) != width {
			t.Fatalf("narrow footer width %d = %q (%d)", width, bar, ansi.StringWidth(bar))
		}
	}
}

func TestHumanToggleIsPerTab(t *testing.T) {
	panel := newHumanTestChat()
	em := portalis.NewEmulator("owner__worker", "Worker", "/bin/sh", nil)
	em.SetStartEnv([]string{"AUTOMATA_HUMAN_EXPORT=1"})
	panel.sessions = append(panel.sessions, &chatSession{name: "Worker", em: em, panel: &fakePanel{}})
	panel.toggleHuman()
	panel.activeIdx = 1
	if panel.sessions[1].humanEnabled || !strings.Contains(panel.humanButtonLabel(panel.width), "○") {
		t.Fatal("Main toggle leaked into Worker")
	}
	panel.toggleHuman()
	panel.toggleHuman()
	panel.activeIdx = 0
	if !panel.sessions[0].humanEnabled || !strings.Contains(panel.humanButtonLabel(panel.width), "●") {
		t.Fatal("Worker toggle changed Main state")
	}
}
