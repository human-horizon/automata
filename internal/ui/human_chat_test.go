package ui

import (
	"strings"
	"testing"

	"github.com/HumanHorizon/automata/internal/ai-knowledge/human"
	apptheme "github.com/HumanHorizon/automata/internal/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func humanTestData(width int) *human.Data {
	return &human.Data{
		Epoch: "epoch-1",
		Input: human.Input{Width: width, Lines: []string{"native editor", "draft"}, Cursor: &human.Cursor{Row: 1, Col: 5}},
		Entries: []human.Entry{
			{Kind: human.EntryKindUser, ID: "user-1", Text: "user-visible"},
			{Kind: human.EntryKindActivity, ID: "chain-1", Status: human.ActivityStatusDone, Items: []human.Item{
				{Kind: human.ItemKindThinking, ID: "thought-1", Text: "secret-thinking"},
				{Kind: human.ItemKindCommentary, ID: "comment-1", Text: "secret-commentary"},
				{Kind: human.ItemKindToolCall, ID: "call-1", ToolCallID: "tool-1", Name: "read", Text: "secret-arguments"},
				{Kind: human.ItemKindToolResult, ID: "result-1", ToolCallID: "tool-1", Name: "read", Text: "secret-output"},
				{Kind: human.ItemKindToolCall, ID: "call-2", ToolCallID: "tool-2", Name: "grep", Text: "second-arguments"},
				{Kind: human.ItemKindToolResult, ID: "result-2", ToolCallID: "tool-2", Name: "grep", Text: "second-output"},
			}},
			{Kind: human.EntryKindAnswer, ID: "answer-1", Text: "final-visible"},
		},
	}
}

func TestHumanWholeChainHasOneRowAndNoIntermediateText(t *testing.T) {
	h := newHumanChat()
	h.data = humanTestData(60)
	view, ok := h.render(60, 30, apptheme.Default())
	if !ok || strings.Count(view, "Размышления и инструменты") != 1 || !strings.Contains(view, "вызовов: 2") || !strings.Contains(view, "user-visible") || !strings.Contains(view, "final-visible") {
		t.Fatalf("collapsed Human = %q, ok=%v", view, ok)
	}
	for _, hidden := range []string{"secret-thinking", "secret-commentary", "secret-arguments", "secret-output", "second-arguments", "second-output"} {
		if strings.Contains(view, hidden) {
			t.Fatalf("intermediate text leaked: %q", hidden)
		}
	}
	if !strings.Contains(view, "native editor") || !strings.Contains(view, "draft") {
		t.Fatal("native editor frame disappeared")
	}
	for _, line := range strings.Split(view, "\n") {
		if ansi.StringWidth(line) != 60 {
			t.Fatalf("rendered line has width %d", ansi.StringWidth(line))
		}
	}
}

func TestHumanClickOpensEntireChainInUpperHalfAndRepeatedClickCloses(t *testing.T) {
	h := newHumanChat()
	h.data = humanTestData(60)
	h.render(60, 45, apptheme.Default())
	row := -1
	for y, id := range h.rowActivities {
		if id == "chain-1" {
			row = y
		}
	}
	if row < 0 || !h.handleMouse(tea.MouseMsg(tea.MouseEvent{X: 2, Y: row, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})) {
		t.Fatal("chain row not clickable")
	}
	view, ok := h.render(60, 45, apptheme.Default())
	if !ok || h.detailHeight == 0 {
		t.Fatalf("split did not open: %q", view)
	}
	for _, hidden := range []string{"secret-thinking", "secret-commentary", "secret-arguments", "secret-output"} {
		if !strings.Contains(view, hidden) {
			t.Fatalf("chain detail omitted %q: %s", hidden, view)
		}
	}
	if strings.Index(view, "secret-thinking") >= strings.Index(view, "user-visible") {
		t.Fatal("chain detail is not above conversation")
	}
	if len(strings.Split(view, "\n")) != 45 {
		t.Fatal("split has wrong height")
	}
	for y, id := range h.rowActivities {
		if id == "chain-1" {
			h.handleMouse(tea.MouseMsg(tea.MouseEvent{X: 2, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}))
		}
	}
	if h.selected != "" {
		t.Fatal("second click did not close split")
	}
}

func TestHumanScrollKeepsIndependentAnchors(t *testing.T) {
	h := newHumanChat()
	h.data = humanTestData(40)
	h.data.Entries[1].Items[0].Text = strings.Repeat("thought-line\n", 50)
	h.data.Entries[0].Text = strings.Repeat("user-line\n", 40)
	h.selected = "chain-1"
	h.render(40, 25, apptheme.Default())
	oldList := h.listOffset
	h.handleMouse(tea.MouseMsg(tea.MouseEvent{X: 2, Y: 2, Button: tea.MouseButtonWheelDown}))
	if h.detailOffset == 0 || h.listOffset != oldList {
		t.Fatal("detail scroll changed history anchor")
	}
	oldDetail := h.detailOffset
	h.handleMouse(tea.MouseMsg(tea.MouseEvent{X: 2, Y: h.listStart + 1, Button: tea.MouseButtonWheelUp}))
	if h.listOffset >= oldList || h.detailOffset != oldDetail || h.followList {
		t.Fatal("history scroll changed detail anchor")
	}
	anchor := h.listOffset
	h.data.Entries = append(h.data.Entries, human.Entry{Kind: human.EntryKindAnswer, ID: "answer-2", Text: "new-answer"})
	h.render(40, 25, apptheme.Default())
	if h.listOffset != anchor {
		t.Fatal("new data forcibly scrolled history")
	}
}

func TestHumanFallsBackForDialogsMismatchedFramesAndTinySplit(t *testing.T) {
	h := newHumanChat()
	h.data = humanTestData(40)
	h.data.Input.Native = true
	if _, ok := h.render(40, 25, apptheme.Default()); ok {
		t.Fatal("native dialog hidden by Human")
	}
	h.data.Input.Native = false
	if _, ok := h.render(41, 25, apptheme.Default()); ok || h.fallback == "" {
		t.Fatal("stale editor dimensions accepted")
	}
	h.selected = "chain-1"
	if _, ok := h.render(40, 7, apptheme.Default()); ok || h.fallback == "" {
		t.Fatal("tiny split hides editor")
	}
}

func TestHumanNativeCaretPreservesWideAndCombinedGraphemes(t *testing.T) {
	for _, value := range []struct {
		name string
		text string
		col  int
	}{
		{name: "CJK", text: "你x", col: 0},
		{name: "combined", text: "e\u0301X", col: 0},
		{name: "emoji", text: "前👩‍👩‍👦suffix", col: 2},
	} {
		t.Run(value.name, func(t *testing.T) {
			h := newHumanChat()
			h.data = humanTestData(60)
			h.data.Input.Lines[1] = value.text
			h.data.Input.Cursor.Col = value.col
			view, ok := h.render(60, 30, apptheme.Default())
			if !ok || !strings.Contains(ansi.Strip(view), value.text) {
				t.Fatalf("native caret split or removed the glyph: %q", view)
			}
		})
	}
}

func TestHumanSanitizesControlsWithoutLosingLongToolOutput(t *testing.T) {
	if got := humanText("safe\x1b]52;c;ZXhmaWw=\a\x1b[2Jtail\x00"); got != "safetail" {
		t.Fatalf("terminal control sequence survived: %q", got)
	}
	h := newHumanChat()
	h.data = humanTestData(30)
	long := strings.Repeat("0123456789", 50)
	h.data.Entries[1].Items[3].Text = long
	lines := h.activityLines(&h.data.Entries[1], 30, apptheme.Default())
	joined := strings.Join(lines, "")
	if strings.Count(joined, "0123456789") < 30 {
		t.Fatal("long tool output was truncated instead of wrapped")
	}
	for _, line := range lines {
		if ansi.StringWidth(line) > 30 {
			t.Fatalf("long chain exceeds viewport width: %q", line)
		}
	}
}
