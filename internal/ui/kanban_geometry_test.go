package ui

import (
	"testing"

	"github.com/HumanHorizon/automata/internal/kanban"
	tea "github.com/charmbracelet/bubbletea"
)

func TestKanbanGeometryHelpers(t *testing.T) {
	for _, test := range []struct {
		width int
		want  int
	}{
		{width: 0, want: 20},
		{width: 1, want: 4},
		{width: 15, want: 4},
		{width: 80, want: 20},
		{width: 100, want: 25},
	} {
		if got := kanbanColumnWidth(test.width); got != test.want {
			t.Errorf("kanbanColumnWidth(%d) = %d, want %d", test.width, got, test.want)
		}
	}

	if got := pickerChatIndex(2, 3, 2); got != -1 {
		t.Fatalf("picker index above list = %d, want -1", got)
	}
	if got := pickerChatIndex(3, 3, 2); got != 0 {
		t.Fatalf("picker first item index = %d, want 0", got)
	}
	if got := pickerChatIndex(5, 3, 2); got != -1 {
		t.Fatalf("picker index below list = %d, want -1", got)
	}

	tasks := []kanban.Task{{Title: "One", Status: "todo"}, {Title: "Two", Status: "todo"}}
	for _, test := range []struct {
		name       string
		y          int
		x          int
		scroll     int
		wantRow    int
		wantButton string
	}{
		{name: "header", y: 0, x: 1, wantRow: -1},
		{name: "card border", y: 1, x: 1, wantRow: 0},
		{name: "title body", y: 2, x: 1, wantRow: 0},
		{name: "delete control", y: 2, x: 17, wantRow: 0, wantButton: "delete"},
		{name: "status action", y: 3, x: 1, wantRow: 0, wantButton: "pending"},
		{name: "inter-card gap", y: 6, x: 1, wantRow: -1},
		{name: "scrolled status action", y: 1, x: 1, scroll: 2, wantRow: 0, wantButton: "pending"},
	} {
		t.Run(test.name, func(t *testing.T) {
			row, button := hitTestKanbanColumn(tasks, 20, test.scroll, test.y, test.x)
			if row != test.wantRow || button != test.wantButton {
				t.Fatalf("hitTest = (%d, %q), want (%d, %q)", row, button, test.wantRow, test.wantButton)
			}
		})
	}

	if got := kanbanMaxScrollOffset(tasks, 20); got != 0 {
		t.Fatalf("max scroll for fitting cards = %d, want 0", got)
	}
	if got := kanbanMaxScrollOffset(tasks, 4); got <= 0 {
		t.Fatalf("max scroll for overflowing cards = %d, want positive", got)
	}
}

func TestKanbanMouseRoutesColumnBoundariesAndBoardYOffset(t *testing.T) {
	panel := NewKanbanPanel("default")
	panel.colPanels[1].setTasks([]kanban.Task{{Title: "One", Status: "todo"}})
	panel.View(80, 20)

	panel.handleMouse(tea.MouseMsg{X: 20, Y: 6, Button: tea.MouseButtonNone})
	if panel.colPanels[1].hoverRow != 0 || panel.colPanels[1].hoverBtn != "pending" {
		t.Fatalf("routed hover = (%d, %q), want column 1 pending action", panel.colPanels[1].hoverRow, panel.colPanels[1].hoverBtn)
	}

	panel.handleMouse(tea.MouseMsg{X: 20, Y: 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if panel.activeCol != 1 {
		t.Fatalf("active column at x=20 is %d, want 1", panel.activeCol)
	}
	panel.handleMouse(tea.MouseMsg{X: 80, Y: 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if panel.activeCol != 1 {
		t.Fatalf("out-of-range x changed active column to %d", panel.activeCol)
	}
}
