// Package ui provides a stateless renderer for ai-knowledge data, suitable
// for embedding inside another Bubble Tea tree (no tea.NewProgram, no
// goroutines, no tickers — caller drives Refresh + Render).
package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

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

// ViewWithTheme renders the knowledge panel with the supplied palette.
func ViewWithTheme(width, height int, ctx *context.Data, js []jobs.Job, currentTask string, palette apptheme.Theme) string {
	styles := newRenderStyles(palette)
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
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
		for _, line := range wrapString("  "+main, width) {
			b.WriteString(styles.item.Render(line))
			b.WriteString("\n")
		}
		if s.Description != "" && (s.Action == "read" || s.Action == "write" || s.Action == "run") && !strings.HasSuffix(main, s.Description) {
			for _, line := range wrapString("  "+s.Description, width) {
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

	writeSection(&b, "Plans", styles.section)

	if ctx != nil && len(ctx.Plans) > 0 {
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
				line := fmt.Sprintf("    %s %s", mark, text)
				for _, wl := range wrapString(line, width) {
					b.WriteString(style.Render(wl))
					b.WriteString("\n")
				}
			}
		}
	} else {
		b.WriteString(styles.empty.Render("  (no plans)"))
		b.WriteString("\n")
	}

	writeSection(&b, "Jobs", styles.section)
	if len(js) > 0 {
		for _, j := range js {
			mark := "•"
			if j.Running {
				mark = "●"
			}
			line := fmt.Sprintf("  %s %s", mark, firstLine(j.Command))
			for _, wl := range wrapString(line, width) {
				b.WriteString(styles.item.Render(wl))
				b.WriteString("\n")
			}
		}
	} else {
		b.WriteString(styles.empty.Render("  (no running jobs)"))
		b.WriteString("\n")
	}

	lines := strings.Split(b.String(), "\n")
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
	_ = time.Now()
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
		lines = append(lines, s[:breakAt])
		s = s[breakAt:]
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
