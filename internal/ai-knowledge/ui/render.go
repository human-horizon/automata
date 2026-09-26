// Package ui provides a stateless renderer for ai-knowledge data, suitable
// for embedding inside another Bubble Tea tree (no tea.NewProgram, no
// goroutines, no tickers — caller drives Refresh + Render).
package ui

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"github.com/HumanHorizon/automata/internal/ai-knowledge/context"
	"github.com/HumanHorizon/automata/internal/ai-knowledge/jobs"
	apptheme "github.com/HumanHorizon/automata/internal/theme"
)

type renderStyles struct {
	title   lipgloss.Style
	section lipgloss.Style
	item    lipgloss.Style
	done    lipgloss.Style
	empty   lipgloss.Style
	path    lipgloss.Style
}

func newRenderStyles(palette apptheme.Theme) renderStyles {
	return renderStyles{
		title:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(palette.TextStrong)),
		section: lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextMuted)),
		item:    lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Text)),
		done:    lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Lime)),
		empty:   lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextDim)).Italic(true),
		path:    lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextDim)),
	}
}

// actionIcon maps a status action to a single bullet + canonical word.
// Idle is intentionally empty so the Knowledge panel does not duplicate the
// "○ idle" indicator that the Tree already shows. Description is rendered
// separately below the main line (see View), so we never concatenate it here.
//
// Canonical words match internal/tree/render.go:statusEmojiToWord so the two
// panels read the same status the same way.
func actionIcon(action, _ string) string {
	switch action {
	case "thinking":
		return "● thinking"
	case "read":
		return "● read"
	case "write":
		return "● write"
	case "grep":
		return "● grep"
	case "find":
		return "● find"
	case "analyze":
		return "● analyze"
	case "wait":
		return "● wait"
	case "job":
		return "● job"
	case "run":
		return "● run"
	case "status":
		return "● status"
	case "active":
		return "● active"
	case "stop":
		return "● stopped"
	case "idle", "":
		return ""
	default:
		return "● " + action
	}
}

// View renders the knowledge data into a fixed-width string. It is safe to
// call from any goroutine; it does not touch any external state.
func View(width, height int, ctx *context.Data, js []jobs.Job, currentTask string) string {
	return ViewWithTheme(width, height, ctx, js, currentTask, apptheme.Default())
}

// CollapseState records which knowledge sections are hidden.
type CollapseState struct {
	Plans bool
	Jobs  bool
}

// ViewWithTheme renders the knowledge panel with the supplied palette and a fixed height.
func ViewWithTheme(width, height int, ctx *context.Data, js []jobs.Job, currentTask string, palette apptheme.Theme) string {
	return fitHeight(ContentWithTheme(width, ctx, js, currentTask, palette, CollapseState{}), height)
}

// ContentWithTheme renders all content without clipping it to a viewport. The
// embedding panel owns scrolling and can collapse selected sections.
func ContentWithTheme(width int, ctx *context.Data, js []jobs.Job, currentTask string, palette apptheme.Theme, collapsed CollapseState) string {
	styles := newRenderStyles(palette)
	if width <= 0 {
		width = 80
	}

	var b strings.Builder
	writeSection(&b, "Status", styles.section)
	if ctx != nil && ctx.Status != nil {
		s := ctx.Status
		main := s.DisplayText()
		icon := actionIcon(s.Action, s.Description)
		if s.Action != "" {
			main = icon
		}
		for _, line := range wrapPrefixed("  ", main, width) {
			b.WriteString(styles.item.Render(line))
			b.WriteString("\n")
		}
		if s.Description != "" && (s.Action == "read" || s.Action == "write" || s.Action == "run") && !strings.HasSuffix(main, s.Description) {
			for _, line := range wrapPrefixed("  ", s.Description, width) {
				b.WriteString(styles.path.Render(line))
				b.WriteString("\n")
			}
		}
	} else {
		b.WriteString(styles.empty.Render("  (no status)"))
		b.WriteString("\n")
	}

	// Show current task ABOVE Plans (not inside Plans)
	if currentTask != "" {
		writeSection(&b, "Task", styles.section)
		b.WriteString(styles.title.Render("  ▶ " + currentTask))
		b.WriteString("\n")
	}

	writeCollapsibleSection(&b, "Plans", collapsed.Plans, styles.section)

	if !collapsed.Plans && ctx != nil && len(ctx.Plans) > 0 {
		names := make([]string, 0, len(ctx.Plans))
		for n := range ctx.Plans {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, name := range names {
			steps := ctx.Plans[name]
			b.WriteString(styles.title.Render("  " + name))
			b.WriteString("\n")
			for _, s := range steps {
				mark := "[ ]"
				text := s.Text
				style := styles.item
				if s.Done {
					mark = "[x]"
					style = styles.done
				}
				prefix := fmt.Sprintf("    %s ", mark)
				for _, wl := range wrapPrefixed(prefix, text, width) {
					b.WriteString(style.Render(wl))
					b.WriteString("\n")
				}
			}
		}
	} else if !collapsed.Plans {
		b.WriteString(styles.empty.Render("  (no plans)"))
		b.WriteString("\n")
	}

	writeCollapsibleSection(&b, "Jobs", collapsed.Jobs, styles.section)
	if !collapsed.Jobs && len(js) > 0 {
		for _, j := range js {
			mark := "•"
			if j.Running {
				mark = "●"
			}
			prefix := fmt.Sprintf("  %s ", mark)
			for _, wl := range wrapPrefixed(prefix, firstLine(j.Command), width) {
				b.WriteString(styles.item.Render(wl))
				b.WriteString("\n")
			}
		}
	} else if !collapsed.Jobs {
		b.WriteString(styles.empty.Render("  (no running jobs)"))
		b.WriteString("\n")
	}

	return strings.TrimSuffix(b.String(), "\n")
}

func fitHeight(content string, height int) string {
	if height <= 0 {
		height = 24
	}
	lines := strings.Split(content, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func writeSection(b *strings.Builder, title string, style lipgloss.Style) {
	b.WriteString(style.Render("── " + title + " "))
	b.WriteString("\n")
}

func writeCollapsibleSection(b *strings.Builder, title string, collapsed bool, style lipgloss.Style) {
	indicator := "▾"
	if collapsed {
		indicator = "▸"
	}
	b.WriteString(style.Render("── " + indicator + " " + title + " "))
	b.WriteString("\n")
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx]
	}
	return s
}

// wrapString splits s into lines that fit within the given width.
func wrapString(s string, width int) []string {
	if width <= 0 || s == "" {
		return []string{s}
	}
	var lines []string
	for len(s) > 0 {
		if lipgloss.Width(s) <= width {
			lines = append(lines, s)
			break
		}
		breakAt := lastSpaceBefore(s, width)
		if breakAt <= 0 {
			breakAt = findBreakAt(s, width)
		}
		if breakAt <= 0 {
			_, breakAt = utf8.DecodeRuneInString(s)
		}
		lines = append(lines, s[:breakAt])
		s = s[breakAt:]
	}
	return lines
}

func wrapPrefixed(prefix, text string, width int) []string {
	if width <= lipgloss.Width(prefix) {
		return wrapString(prefix+text, width)
	}
	if text == "" {
		return []string{prefix}
	}

	continuation := strings.Repeat(" ", lipgloss.Width(prefix))
	lines := make([]string, 0, 1)
	currentPrefix := prefix
	remaining := text
	for remaining != "" {
		available := width - lipgloss.Width(currentPrefix)
		if lipgloss.Width(remaining) <= available {
			lines = append(lines, currentPrefix+remaining)
			break
		}
		breakAt := lastSpaceBefore(remaining, available)
		if breakAt <= 0 {
			breakAt = findBreakAt(remaining, available)
		}
		if breakAt <= 0 {
			_, breakAt = utf8.DecodeRuneInString(remaining)
		}
		lines = append(lines, currentPrefix+strings.TrimRight(remaining[:breakAt], " "))
		remaining = strings.TrimLeft(remaining[breakAt:], " ")
		currentPrefix = continuation
	}
	return lines
}

func lastSpaceBefore(s string, width int) int {
	cells := 0
	lastSpace := -1
	for i, r := range s {
		cells += lipgloss.Width(string(r))
		if cells > width {
			break
		}
		if r == ' ' {
			lastSpace = i
		}
	}
	if lastSpace > 0 {
		return lastSpace + 1
	}
	return 0
}

func findBreakAt(s string, width int) int {
	cells := 0
	for i, r := range s {
		cells += lipgloss.Width(string(r))
		if cells > width {
			return i
		}
	}
	return len(s)
}
