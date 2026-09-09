package ui

import (
	"strings"
	"testing"

	"github.com/HumanHorizon/automata/internal/kanban"
)

// TestCardRendersSubstatus verifies that a task with a Substatus shows the
// substatus line inside the card box.
func TestCardRendersSubstatus(t *testing.T) {
	c := newKanbanColPanel(0)
	c.width = 28
	c.parent = &KanbanPanel{sessionNames: map[string]string{}}
	task := kanban.Task{
		Title:      "Substatus test",
		Status:     "progress",
		AssignedTo: "s1",
		Substatus:  "analyze",
		Path:       "/tmp/x.md",
	}

	lines := c.renderCard(task, false)
	joined := stripAnsi(strings.Join(lines, "\n"))
	if !strings.Contains(joined, "analyze") {
		t.Errorf("expected substatus 'analyze' to appear in card, got:\n%s", joined)
	}
}

// TestCardOmitsSubstatusWhenEmpty verifies no substatus row is rendered
// when Substatus is empty — keeps the card compact for tasks without an
// active sub-status.
func TestCardOmitsSubstatusWhenEmpty(t *testing.T) {
	c := newKanbanColPanel(0)
	c.width = 28
	c.parent = &KanbanPanel{sessionNames: map[string]string{}}
	task := kanban.Task{
		Title:     "No substatus",
		Status:    "todo",
		Path:      "/tmp/x.md",
		Substatus: "",
	}

	lines := c.renderCard(task, false)
	joined := stripAnsi(strings.Join(lines, "\n"))
	if strings.Contains(joined, "↳") {
		t.Errorf("card should not contain a substatus marker when Substatus is empty, got:\n%s", joined)
	}
}

// TestCardContentLinesCountsSubstatus verifies the layout helper
// (cardContentLines) returns the correct number of content rows when
// Substatus is set. View and hitTest both use this function, so the
// number of rendered lines and the hitTest positions stay in sync.
func TestCardContentLinesCountsSubstatus(t *testing.T) {
	base := kanban.Task{Title: "T", Status: "progress", Path: "/tmp/x.md"}
	without := cardContentLines(base)
	with := cardContentLines(kanban.Task{Title: "T", Status: "progress", Path: "/tmp/x.md", Substatus: "read"})

	if with != without+1 {
		t.Errorf("Substatus should add 1 line, got without=%d with=%d", without, with)
	}
}
