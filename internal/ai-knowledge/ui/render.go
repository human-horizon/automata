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
)

var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#edb449"))
	sectionStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	itemStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#cdccc3"))
	doneStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#bef264"))
	emptyStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Italic(true)
	pathStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#83a598"))
)

// actionIcon maps a status action to a single emoji + label. Unknown actions
// fall back to a generic indicator.
func actionIcon(action, description string) string {
	switch action {
	case "thinking":
		return "~ thinking"
	case "read":
		return "R " + description
	case "write":
		return "W " + description
	case "grep":
		return "G " + description
	case "run":
		return "> " + description
	case "idle":
		return ""
	case "stop":
		return "X " + description
	case "status":
		return "i " + description
	case "active", "analyze":
		return "A " + description
	default:
		if action == "" {
			return ""
		}
		return action + " " + description
	}
}

// View renders the knowledge data into a fixed-width string. It is safe to
// call from any goroutine; it does not touch any external state.
func View(width, height int, ctx *context.Data, js []jobs.Job, currentTask string) string {
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}

	var b strings.Builder
	writeSection(&b, "Status")
	if ctx != nil && ctx.Status != nil {
		s := ctx.Status
		main := s.DisplayText()
		icon := actionIcon(s.Action, s.Description)
		if s.Action != "" {
			main = icon
		}
		for _, line := range wrapString("  "+main, width) {
			b.WriteString(itemStyle.Render(line))
			b.WriteString("\n")
		}
		if s.Description != "" && s.Action != "" && !strings.HasSuffix(main, s.Description) {
			for _, line := range wrapString("  "+s.Description, width) {
				b.WriteString(pathStyle.Render(line))
				b.WriteString("\n")
			}
		}
	} else {
		b.WriteString(emptyStyle.Render("  (no status)"))
		b.WriteString("\n")
	}

	// Show current task ABOVE Plans (not inside Plans)
	if currentTask != "" {
		writeSection(&b, "Task")
		b.WriteString(titleStyle.Render("  ▶ " + currentTask))
		b.WriteString("\n")
	}

	writeSection(&b, "Plans")

	if ctx != nil && len(ctx.Plans) > 0 {
		names := make([]string, 0, len(ctx.Plans))
		for n := range ctx.Plans {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, name := range names {
			steps := ctx.Plans[name]
			b.WriteString(titleStyle.Render("  " + name))
			b.WriteString("\n")
			for _, s := range steps {
				mark := "[ ]"
				text := s.Text
				style := itemStyle
				if s.Done {
					mark = "[x]"
					style = doneStyle
				}
				line := fmt.Sprintf("    %s %s", mark, text)
				for _, wl := range wrapString(line, width) {
					b.WriteString(style.Render(wl))
					b.WriteString("\n")
				}
			}
		}
	} else {
		b.WriteString(emptyStyle.Render("  (no plans)"))
		b.WriteString("\n")
	}

	writeSection(&b, "Jobs")
	if len(js) > 0 {
		for _, j := range js {
			mark := "•"
			if j.Running {
				mark = "●"
			}
			line := fmt.Sprintf("  %s %s", mark, firstLine(j.Command))
			for _, wl := range wrapString(line, width) {
				b.WriteString(itemStyle.Render(wl))
				b.WriteString("\n")
			}
		}
	} else {
		b.WriteString(emptyStyle.Render("  (no running jobs)"))
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

func writeSection(b *strings.Builder, title string) {
	b.WriteString(sectionStyle.Render("── " + title + " "))
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
