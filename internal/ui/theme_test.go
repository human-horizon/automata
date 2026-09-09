package ui

import (
	"testing"

	apptheme "github.com/HumanHorizon/automata/internal/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type themePanelStub struct{}

func (themePanelStub) View(int, int) string { return "" }

func (themePanelStub) Update(tea.Msg) tea.Cmd { return nil }

func TestNativeTextStylesAvoidPinkAndPurple(t *testing.T) {
	palette := apptheme.Default()
	forbidden := []string{palette.Pink, palette.PinkMuted, palette.Purple, palette.PurpleMuted}

	context := newContextStyles(palette)
	kanban := newKanbanStyles(palette)
	markdown := newMarkdownStyles(palette)
	styles := []struct {
		name  string
		style lipgloss.Style
	}{
		{"context.tabActive", context.tabActive},
		{"context.tabInactive", context.tabInactive},
		{"context.title", context.title},
		{"context.sectionHeader", context.sectionHeader},
		{"context.sectionActive", context.sectionActive},
		{"context.empty", context.empty},
		{"kanban.columns[0]", kanban.columns[0]},
		{"kanban.columns[1]", kanban.columns[1]},
		{"kanban.columns[2]", kanban.columns[2]},
		{"kanban.columns[3]", kanban.columns[3]},
		{"kanban.title", kanban.title},
		{"kanban.item", kanban.item},
		{"kanban.cardPath", kanban.cardPath},
		{"kanban.assigned", kanban.assigned},
		{"kanban.substatus", kanban.substatus},
		{"kanban.delete", kanban.delete},
		{"kanban.transit", kanban.transit},
		{"kanban.transitHover", kanban.transitHover},
		{"kanban.button", kanban.button},
		{"kanban.buttonHover", kanban.buttonHover},
		{"kanban.border", kanban.border},
		{"markdown.heading", markdown.heading},
		{"markdown.subheading", markdown.subheading},
		{"markdown.text", markdown.text},
		{"markdown.code", markdown.code},
		{"markdown.inlineCode", markdown.inlineCode},
		{"markdown.quote", markdown.quote},
		{"markdown.rule", markdown.rule},
	}

	for _, item := range styles {
		foreground, ok := item.style.GetForeground().(lipgloss.Color)
		if !ok {
			continue
		}
		for _, color := range forbidden {
			if string(foreground) == color {
				t.Fatalf("%s uses accent text color %q", item.name, color)
			}
		}
	}

	linkForeground, ok := newMarkdownStyles(palette).link.GetForeground().(lipgloss.Color)
	if !ok || string(linkForeground) != palette.Pink {
		t.Fatalf("markdown links use %q, want pink %q", linkForeground, palette.Pink)
	}
}

func TestContainerSetThemePropagatesToNativePanels(t *testing.T) {
	container := NewContainer(themePanelStub{})
	chat := NewChatPanel(nil, "session", "profile")
	container.SetChat(chat, "session")

	palette := apptheme.Default()
	palette.ID = "test-theme"
	palette.Pink = "#ff66aa"
	container.SetTheme(palette)

	if container.palette.ID != palette.ID {
		t.Fatalf("container theme = %q, want %q", container.palette.ID, palette.ID)
	}
	if container.knowledgePanel.palette.ID != palette.ID {
		t.Fatalf("knowledge theme = %q, want %q", container.knowledgePanel.palette.ID, palette.ID)
	}
	if chat.palette.ID != palette.ID || chat.palette.Pink != palette.Pink {
		t.Fatalf("chat theme = %#v, want %#v", chat.palette, palette)
	}
}
