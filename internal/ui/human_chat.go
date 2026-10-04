package ui

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/HumanHorizon/automata/internal/ai-knowledge/human"
	apptheme "github.com/HumanHorizon/automata/internal/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
)

type humanRenderedText struct {
	text  string
	theme string
	width int
	lines []string
}

// humanChat renders only classifications supplied by the Pi extension.
// Keyboard and paste messages remain owned by the native terminal panel.
type humanChat struct {
	humanProjectionWatch
	data          *human.Data
	diagnostic    string
	fallback      string
	selected      string
	listOffset    int
	detailOffset  int
	followList    bool
	followDetail  bool
	rendered      bool
	width         int
	listStart     int
	listHeight    int
	detailHeight  int
	listTotal     int
	detailTotal   int
	rowActivities map[int]string
	textCache     map[string]humanRenderedText
	retiredEpochs map[string]struct{}
}

func newHumanChat() *humanChat {
	return &humanChat{followList: true, textCache: make(map[string]humanRenderedText)}
}

func (h *humanChat) render(width, height int, palette apptheme.Theme) (string, bool) {
	h.rendered = false
	h.fallback = ""
	h.rowActivities = make(map[int]string)
	if h.data == nil || h.diagnostic != "" || h.watchError != "" {
		return "", false
	}
	input := h.data.Input
	if input.Native {
		return "", false
	}
	if input.Width != width {
		h.fallback = "ожидание кадра редактора нового размера"
		return "", false
	}
	contentHeight := height - len(input.Lines) - 1
	if width < 4 || contentHeight < 1 {
		h.fallback = "недостаточно места для Human и редактора"
		return "", false
	}
	if palette.ID == "" {
		palette = apptheme.Default()
	}
	h.width = width
	h.detailHeight, h.listStart, h.listHeight = 0, 0, contentHeight
	selected := h.selectedActivity()
	if selected != nil {
		h.detailHeight = (contentHeight - 1) / 2
		h.listStart = h.detailHeight + 1
		h.listHeight = contentHeight - h.listStart
		if h.detailHeight < 2 || h.listHeight < 1 {
			h.fallback = "недостаточно места для цепочки и редактора"
			return "", false
		}
	}
	list, activities := h.historyLines(width, palette)
	h.listTotal = len(list)
	h.listOffset = humanViewportOffset(h.listOffset, len(list), h.listHeight, h.followList)
	lines := make([]string, 0, height)
	if selected != nil {
		header := fmt.Sprintf(" Цепочка · %s", humanStatus(selected.Status))
		header = ansi.Truncate(header, max(0, width-3), "")
		header += strings.Repeat(" ", max(0, width-3-ansi.StringWidth(header))) + " × "
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextStrong)).Render(header))
		detail := h.activityLines(selected, width, palette)
		h.detailTotal = len(detail)
		h.detailOffset = humanViewportOffset(h.detailOffset, len(detail), h.detailHeight-1, h.followDetail)
		lines = append(lines, humanViewport(detail, h.detailOffset, h.detailHeight-1)...)
		lines = append(lines, humanRule(width, palette))
	}
	for row, id := range activities {
		visibleRow := row - h.listOffset
		if visibleRow >= 0 && visibleRow < h.listHeight {
			h.rowActivities[h.listStart+visibleRow] = id
		}
	}
	lines = append(lines, humanViewport(list, h.listOffset, h.listHeight)...)
	lines = append(lines, humanRule(width, palette))
	for row, line := range input.Lines {
		text := ansi.Truncate(humanText(line), width, "")
		text += strings.Repeat(" ", max(0, width-ansi.StringWidth(text)))
		if input.Cursor != nil && input.Cursor.Row == row && input.Cursor.Col < width {
			text = humanCaret(text, input.Cursor.Col, width)
		}
		lines = append(lines, text)
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], width, "")
		lines[i] += strings.Repeat(" ", max(0, width-ansi.StringWidth(lines[i])))
	}
	h.rendered = true
	return strings.Join(lines, "\n"), true
}

func (h *humanChat) selectedActivity() *human.Entry {
	if h.data == nil {
		return nil
	}
	for i := range h.data.Entries {
		entry := &h.data.Entries[i]
		if entry.Kind == human.EntryKindActivity && entry.ID == h.selected {
			return entry
		}
	}
	h.selected = ""
	return nil
}

func (h *humanChat) historyLines(width int, palette apptheme.Theme) ([]string, map[int]string) {
	var lines []string
	activities := make(map[int]string)
	for _, entry := range h.data.Entries {
		if entry.Kind == human.EntryKindActivity {
			activities[len(lines)] = entry.ID
			marker := "▸"
			if entry.ID == h.selected {
				marker = "▾"
			}
			calls := 0
			for _, item := range entry.Items {
				if item.Kind == human.ItemKindToolCall {
					calls++
				}
			}
			label := fmt.Sprintf(" %s Размышления и инструменты · %s · вызовов: %d", marker, humanStatus(entry.Status), calls)
			lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextMuted)).Render(ansi.Truncate(label, width, "")))
			continue
		}
		label := "Ты"
		if entry.Kind == human.EntryKindAnswer {
			label = "Ответ"
			if entry.Truncated {
				label += " · сокращён лимитом модели"
			}
		}
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextStrong)).Bold(true).Render(" "+label))
		lines = append(lines, h.markdownLines(entry.ID, entry.Text, width, palette)...)
		lines = append(lines, "")
	}
	return lines, activities
}

func (h *humanChat) activityLines(entry *human.Entry, width int, palette apptheme.Theme) []string {
	var lines []string
	for _, item := range entry.Items {
		label := ""
		switch item.Kind {
		case human.ItemKindThinking:
			label = "Мысли"
		case human.ItemKindCommentary:
			label = "Промежуточный текст"
		case human.ItemKindToolCall:
			label = "Вызов · " + humanText(item.Name)
		case human.ItemKindToolResult:
			label = "Результат · " + humanText(item.Name)
		}
		color := palette.TextMuted
		if item.IsError {
			label += " · ошибка"
			color = palette.Error
		}
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Bold(true).Render(" "+label))
		if item.Kind == human.ItemKindToolCall || item.Kind == human.ItemKindToolResult {
			for _, line := range strings.Split(ansi.Hardwrap(humanText(item.Text), max(1, width-2), true), "\n") {
				lines = append(lines, "  "+line)
			}
		} else {
			lines = append(lines, h.markdownLines(entry.ID+":"+item.ID, item.Text, width, palette)...)
		}
		lines = append(lines, "")
	}
	return lines
}

func (h *humanChat) markdownLines(id, text string, width int, palette apptheme.Theme) []string {
	theme := strings.Join([]string{palette.ID, palette.Text, palette.TextStrong, palette.TextMuted, palette.Lime, palette.Background, palette.Pink, palette.BorderMuted}, "\x00")
	if cached, ok := h.textCache[id]; ok && cached.text == text && cached.width == width && cached.theme == theme {
		return cached.lines
	}
	cleaned := humanText(text)
	var source []string
	inCode := false
	for _, line := range strings.Split(cleaned, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inCode = !inCode
			source = append(source, line)
		} else if inCode {
			source = append(source, strings.Split(ansi.Hardwrap(line, max(1, width-4), true), "\n")...)
		} else {
			source = append(source, line)
		}
	}
	lines := strings.Split(renderMarkdownWithTheme(width, strings.Join(source, "\n"), palette), "\n")
	h.textCache[id] = humanRenderedText{text: text, theme: theme, width: width, lines: lines}
	return lines
}

func (h *humanChat) handleMouse(msg tea.MouseMsg) bool {
	if !h.rendered {
		return false
	}
	if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
		delta := -3
		if msg.Button == tea.MouseButtonWheelDown {
			delta = 3
		}
		if h.detailHeight > 0 && msg.Y > 0 && msg.Y < h.detailHeight {
			limit := max(0, h.detailTotal-(h.detailHeight-1))
			h.detailOffset = min(limit, max(0, h.detailOffset+delta))
			h.followDetail = h.detailOffset == limit
			return true
		}
		if msg.Y >= h.listStart && msg.Y < h.listStart+h.listHeight {
			limit := max(0, h.listTotal-h.listHeight)
			h.listOffset = min(limit, max(0, h.listOffset+delta))
			h.followList = h.listOffset == limit
			return true
		}
		return false
	}
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return false
	}
	if h.selected != "" && msg.Y == 0 && msg.X >= h.width-3 {
		h.selected = ""
		return true
	}
	if id, ok := h.rowActivities[msg.Y]; ok {
		if id == h.selected {
			h.selected = ""
		} else {
			h.selected, h.detailOffset, h.followDetail = id, 0, false
		}
		return true
	}
	return false
}

func humanCaret(text string, col, width int) string {
	clusters := uniseg.NewGraphemes(ansi.Cut(text, col, width))
	caret := ""
	for clusters.Next() {
		caret += clusters.Str()
		if ansi.StringWidth(caret) > 0 {
			break
		}
	}
	if caret == "" {
		return text
	}
	end := col + ansi.StringWidth(caret)
	return ansi.Cut(text, 0, col) + lipgloss.NewStyle().Reverse(true).Render(caret) + ansi.Cut(text, end, width)
}

func humanText(value string) string {
	value = strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, ansi.Strip(value))
}

func humanStatus(status human.ActivityStatus) string {
	switch status {
	case human.ActivityStatusWorking:
		return "в процессе"
	case human.ActivityStatusDone:
		return "готово"
	case human.ActivityStatusAborted:
		return "прервано"
	case human.ActivityStatusError:
		return "ошибка"
	default:
		return string(status)
	}
}

func humanRule(width int, palette apptheme.Theme) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(palette.BorderMuted)).Render(strings.Repeat("─", max(0, width)))
}

func humanViewportOffset(offset, total, height int, follow bool) int {
	limit := max(0, total-height)
	if follow {
		return limit
	}
	return min(limit, max(0, offset))
}

func humanViewport(lines []string, offset, height int) []string {
	visible := make([]string, max(0, height))
	for i := range visible {
		if offset+i < len(lines) {
			visible[i] = lines[offset+i]
		}
	}
	return visible
}
