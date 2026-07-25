package ui

import (
	"strings"
	"testing"

	"github.com/Starframe/portalis"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// ---- styleSeq tests ----

func TestStyleSeqEmpty(t *testing.T) {
	got := styleSeq("", "", 0)
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestStyleSeqBold(t *testing.T) {
	got := styleSeq("", "", 1) // bold
	if got != "\x1b[0;1m" {
		t.Fatalf("expected bold, got %q", got)
	}
}

func TestStyleSeqTrueColorFG(t *testing.T) {
	got := styleSeq("#ff8800", "", 0)
	if got != "\x1b[0;38;2;255;136;0m" {
		t.Fatalf("expected true color FG, got %q", got)
	}
}

func TestStyleSeqTrueColorBG(t *testing.T) {
	got := styleSeq("", "#1a1a2e", 0)
	if got != "\x1b[0;48;2;26;26;46m" {
		t.Fatalf("expected true color BG, got %q", got)
	}
}

func TestStyleSeq256ColorFG(t *testing.T) {
	got := styleSeq("196", "", 0) // bright red
	if got != "\x1b[0;38;5;196m" {
		t.Fatalf("expected 256-color FG, got %q", got)
	}
}

func TestStyleSeqBoldUnderlineTrueColor(t *testing.T) {
	got := styleSeq("#00ff00", "#000000", 9) // bold(1) + underline(8) = 9
	if got != "\x1b[0;1;4;38;2;0;255;0;48;2;0;0;0m" {
		t.Fatalf("expected bold+underline+truecolor, got %q", got)
	}
}

func TestStyleSeqAllFlags(t *testing.T) {
	// bold(1) + dim(2) + italic(4) + underline(8) + blink(16) + strikethrough(32) + reverse(64) + hidden(128) = 255
	// Portalis renderStyle order: 1(bold), 2(dim), 3(italic), 4(underline), 5(blink), 7(reverse), 8(hidden), 9(strikethrough)
	got := styleSeq("", "", 255)
	expected := "\x1b[0;1;2;3;4;5;7;8;9m"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

// ---- sgrColorStr tests ----

func TestSgrColorStrTrueColor(t *testing.T) {
	got := sgrColorStr("#ff8800", false)
	if got != "38;2;255;136;0" {
		t.Fatalf("expected 38;2;255;136;0, got %q", got)
	}
}

func TestSgrColorStrTrueColorBG(t *testing.T) {
	got := sgrColorStr("#1a1a2e", true)
	if got != "48;2;26;26;46" {
		t.Fatalf("expected 48;2;26;26;46, got %q", got)
	}
}

func TestSgrColorStr256Color(t *testing.T) {
	got := sgrColorStr("196", false)
	if got != "38;5;196" {
		t.Fatalf("expected 38;5;196, got %q", got)
	}
}

func TestSgrColorStrInvalid(t *testing.T) {
	got := sgrColorStr("not-a-color", false)
	if got != "" {
		t.Fatalf("expected empty for invalid color, got %q", got)
	}
}

// ---- padOrTruncANSI tests ----

func TestPadOrTruncANSIShorter(t *testing.T) {
	got := padOrTruncANSI("hello", 10)
	if got != "hello     " {
		t.Fatalf("expected 'hello     ', got %q", got)
	}
}

func TestPadOrTruncANSILonger(t *testing.T) {
	got := padOrTruncANSI("hello world", 5)
	if got != "hello" {
		t.Fatalf("expected 'hello', got %q", got)
	}
}

func TestPadOrTruncANSIExact(t *testing.T) {
	got := padOrTruncANSI("hello", 5)
	if got != "hello" {
		t.Fatalf("expected 'hello', got %q", got)
	}
}

func TestPadOrTruncANSIWithANSI(t *testing.T) {
	// Red text "hello" — truncation should preserve ANSI.
	input := "\x1b[31mhello\x1b[0m"
	got := padOrTruncANSI(input, 3)
	// Should be "\x1b[31mhel\x1b[0m" or similar valid ANSI.
	if ansi.StringWidth(got) != 3 {
		t.Fatalf("expected visible width 3, got %d: %q", ansi.StringWidth(got), got)
	}
	assertCompleteSGRSequences(t, got)
}

func TestPadOrTruncANSIWithANSIPad(t *testing.T) {
	input := "\x1b[31mhi\x1b[0m"
	got := padOrTruncANSI(input, 5)
	if ansi.StringWidth(got) != 5 {
		t.Fatalf("expected visible width 5, got %d: %q", ansi.StringWidth(got), got)
	}
	if !strings.HasSuffix(got, "   ") {
		t.Fatalf("expected trailing spaces, got %q", got)
	}
}

func TestPadOrTruncANSIZeroTarget(t *testing.T) {
	got := padOrTruncANSI("hello", 0)
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

// ---- tmuxKeyName tests ----

func TestTmuxKeyNameCtrlLetter(t *testing.T) {
	got := tmuxKeyName(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if got != "C-c" {
		t.Fatalf("expected 'C-c', got %q", got)
	}
}

func TestTmuxKeyNameEmptyRunes(t *testing.T) {
	got := tmuxKeyName(tea.KeyMsg{Type: tea.KeyEnter})
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

// ---- sendKey tests ----

func TestSendKeySkipsWhenNoPane(t *testing.T) {
	// No pane ID — should not panic, no error.
	p := &FamiliarPanel{tmuxID: "nonexistent"}
	p.sendKey("", tea.KeyMsg{Type: tea.KeyEnter})
	// Just verify no panic.
}

// ---- screenLinePlain tests ----

func TestScreenLinePlainSimple(t *testing.T) {
	s := portalis.NewScreen(1, 5)
	s.Cells[0][0] = portalis.Cell{Rune: 'H'}
	s.Cells[0][1] = portalis.Cell{Rune: 'e'}
	s.Cells[0][2] = portalis.Cell{Rune: 'l'}
	s.Cells[0][3] = portalis.Cell{Rune: 'l'}
	s.Cells[0][4] = portalis.Cell{Rune: 'o'}

	got := screenLinePlain(s, 0, 5)
	if got != "Hello" {
		t.Fatalf("expected 'Hello', got %q", got)
	}
}

func TestScreenLinePlainWithColor(t *testing.T) {
	s := portalis.NewScreen(1, 5)
	s.Cells[0][0] = portalis.Cell{Rune: 'H', FG: "#ff0000"}
	s.Cells[0][1] = portalis.Cell{Rune: 'e', FG: "#ff0000"}
	s.Cells[0][2] = portalis.Cell{Rune: 'l', FG: "#ff0000"}
	s.Cells[0][3] = portalis.Cell{Rune: 'l', FG: "#ff0000"}
	s.Cells[0][4] = portalis.Cell{Rune: 'o', FG: "#ff0000"}

	got := screenLinePlain(s, 0, 5)
	// Should have ANSI sequences.
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("expected ANSI sequences, got plain: %q", got)
	}
	if ansi.StringWidth(got) != 5 {
		t.Fatalf("expected visible width 5, got %d: %q", ansi.StringWidth(got), got)
	}
	assertCompleteSGRSequences(t, got)
}

func TestScreenLinePlainSkipsContinuation(t *testing.T) {
	s := portalis.NewScreen(1, 4)
	// Wide char: '✅' at col 0, continuation at col 1.
	s.Cells[0][0] = portalis.Cell{Rune: '✅'}
	s.Cells[0][1] = portalis.Cell{Continuation: true}
	s.Cells[0][2] = portalis.Cell{Rune: 'x'}

	got := screenLinePlain(s, 0, 4)
	// Should be "✅x  " (wide char + x + 2 spaces).
	if ansi.StringWidth(got) != 4 {
		t.Fatalf("expected visible width 4, got %d: %q", ansi.StringWidth(got), got)
	}
}

func TestScreenLinePlainWithCombining(t *testing.T) {
	s := portalis.NewScreen(1, 3)
	// 'e' with combining acute accent.
	s.Cells[0][0] = portalis.Cell{Rune: 'e', Combining: "\u0301"}
	s.Cells[0][1] = portalis.Cell{Rune: 'a'}

	got := screenLinePlain(s, 0, 3)
	// Should be "éa " (e + combining acute + a + space).
	if !strings.Contains(got, "\u0301") {
		t.Fatalf("expected combining character, got %q", got)
	}
}

func TestScreenLinePlainOutOfRange(t *testing.T) {
	s := portalis.NewScreen(1, 3)
	got := screenLinePlain(s, 5, 3)
	if got != "" {
		t.Fatalf("expected empty for out-of-range row, got %q", got)
	}
}

// ---- renderFamiliarScreen tests ----

func TestRenderFamiliarScreenBasic(t *testing.T) {
	s := portalis.NewScreen(2, 4)
	s.Cells[0][0] = portalis.Cell{Rune: 'H'}
	s.Cells[0][1] = portalis.Cell{Rune: 'i'}
	s.Cells[1][0] = portalis.Cell{Rune: '!'}

	got := renderFamiliarScreen(s, 4, 2)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if ansi.StringWidth(lines[0]) != 4 {
		t.Fatalf("expected line 0 width 4, got %d: %q", ansi.StringWidth(lines[0]), lines[0])
	}
}

func TestRenderFamiliarScreenLargerThanScreen(t *testing.T) {
	s := portalis.NewScreen(1, 3)
	s.Cells[0][0] = portalis.Cell{Rune: 'A'}
	s.Cells[0][1] = portalis.Cell{Rune: 'B'}
	s.Cells[0][2] = portalis.Cell{Rune: 'C'}

	got := renderFamiliarScreen(s, 5, 3)
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	// Line 0: "ABC  " (padded to 5)
	if ansi.StringWidth(lines[0]) != 5 {
		t.Fatalf("expected line 0 width 5, got %d: %q", ansi.StringWidth(lines[0]), lines[0])
	}
	// Lines 1-2: all spaces
	for i := 1; i < 3; i++ {
		if ansi.StringWidth(lines[i]) != 5 {
			t.Fatalf("expected line %d width 5, got %d: %q", i, ansi.StringWidth(lines[i]), lines[i])
		}
	}
}

func TestRenderFamiliarScreenSmallerThanScreen(t *testing.T) {
	s := portalis.NewScreen(3, 5)
	s.Cells[0][0] = portalis.Cell{Rune: 'H', FG: "#00ff00"}
	s.Cells[0][1] = portalis.Cell{Rune: 'e'}
	s.Cells[0][2] = portalis.Cell{Rune: 'l'}
	s.Cells[0][3] = portalis.Cell{Rune: 'l'}
	s.Cells[0][4] = portalis.Cell{Rune: 'o'}

	got := renderFamiliarScreen(s, 3, 1)
	lines := strings.Split(got, "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if ansi.StringWidth(lines[0]) != 3 {
		t.Fatalf("expected width 3, got %d: %q", ansi.StringWidth(lines[0]), lines[0])
	}
	assertCompleteSGRSequences(t, lines[0])
}

func TestRenderFamiliarScreenNil(t *testing.T) {
	got := renderFamiliarScreen(nil, 5, 2)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	for i, line := range lines {
		if ansi.StringWidth(line) != 5 {
			t.Fatalf("expected line %d width 5, got %d: %q", i, ansi.StringWidth(line), line)
		}
	}
}

func TestRenderFamiliarScreenZeroSize(t *testing.T) {
	s := portalis.NewScreen(1, 3)
	got := renderFamiliarScreen(s, 0, 0)
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}
