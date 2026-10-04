package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	warp "github.com/starframe-dev/warp"
)

func TestHumanLocalGeometrySurvivesGlobalWindowCompatibilityBroadcast(t *testing.T) {
	chat := newHumanTestChat()
	container := NewContainer(chat)
	container.Update(warp.ResizeMsg{Width: 93, Height: 36})
	container.View(93, 36)
	localWidth, localHeight := chat.width, chat.height
	if localWidth >= 93 || localWidth < 1 || localHeight < 1 {
		t.Fatalf("chat has no allocated local viewport: %dx%d", localWidth, localHeight)
	}
	container.Update(tea.WindowSizeMsg{Width: 125, Height: 42})
	chat.Update(tea.WindowSizeMsg{Width: 125, Height: 42})
	if container.width != 93 || container.height != 36 || chat.width != localWidth || chat.height != localHeight {
		t.Fatalf("global compatibility event overwrote local PTY geometry: container=%dx%d chat=%dx%d", container.width, container.height, chat.width, chat.height)
	}
	container.Update(warp.ResizeMsg{Width: 101, Height: 38})
	container.View(101, 38)
	if chat.width <= localWidth || chat.height <= localHeight {
		t.Fatalf("subsequent local resize was ignored: %dx%d", chat.width, chat.height)
	}
}

func TestHumanStandalonePanelStillAcceptsInitialWindowSize(t *testing.T) {
	chat := newHumanTestChat()
	chat.Update(tea.WindowSizeMsg{Width: 70, Height: 30})
	if chat.width != 70 || chat.height != 30 {
		t.Fatal("standalone window-size compatibility was lost")
	}
}
