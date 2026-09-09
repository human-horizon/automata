package ui

import (
	"strings"

	apptheme "github.com/HumanHorizon/automata/internal/theme"
	"github.com/charmbracelet/lipgloss"
)

type markdownStyles struct {
	heading    lipgloss.Style
	subheading lipgloss.Style
	text       lipgloss.Style
	code       lipgloss.Style
	inlineCode lipgloss.Style
	quote      lipgloss.Style
	link       lipgloss.Style
	rule       lipgloss.Style
}

func newMarkdownStyles(palette apptheme.Theme) markdownStyles {
	return markdownStyles{
		heading:    lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(palette.TextStrong)),
		subheading: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(palette.TextMuted)),
		text:       lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Text)),
		code:       lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Lime)).Background(lipgloss.Color(palette.Background)),
		inlineCode: lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Lime)),
		quote:      lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextMuted)).Italic(true),
		link:       lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Pink)).Underline(true),
		rule:       lipgloss.NewStyle().Foreground(lipgloss.Color(palette.BorderMuted)),
	}
}

// renderMarkdown renders the supported Markdown constructs into terminal lines.
func renderMarkdown(width int, content string) string {
	return renderMarkdownWithTheme(width, content, apptheme.Default())
}

func renderMarkdownWithTheme(width int, content string, palette apptheme.Theme) string {
	styles := newMarkdownStyles(palette)
	if width <= 0 {
		width = 80
	}

	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	lines := strings.Split(content, "\n")
	var rendered strings.Builder
	inCode := false
	codeLanguage := ""

	for _, line := range lines {
		if inCode {
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				inCode = false
				codeLanguage = ""
				continue
			}
			writeCodeLine(&rendered, width, line, styles)
			continue
		}

		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			codeLanguage = strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			if codeLanguage != "" {
				writeStyledLine(&rendered, width, "  "+codeLanguage, styles.code)
			}
			inCode = true
			continue
		}
		if trimmed == "" {
			rendered.WriteString("\n")
			continue
		}
		if headingLevel, headingText := parseHeading(trimmed); headingLevel > 0 {
			style := styles.subheading
			if headingLevel == 1 {
				style = styles.heading
			}
			writeStyledWrapped(&rendered, width, "  ", headingText, style, styles)
			continue
		}
		if isHorizontalRule(trimmed) {
			writeStyledLine(&rendered, width, strings.Repeat("─", maxInt(1, width-2)), styles.rule)
			continue
		}
		if quoteText, ok := parseQuote(trimmed); ok {
			writeStyledWrapped(&rendered, width, "  │ ", quoteText, styles.quote, styles)
			continue
		}
		if prefix, listText, ok := parseUnorderedList(line); ok {
			writeStyledWrapped(&rendered, width, prefix, listText, styles.text, styles)
			continue
		}
		if prefix, listText, ok := parseOrderedList(line); ok {
			writeStyledWrapped(&rendered, width, prefix, listText, styles.text, styles)
			continue
		}

		writeStyledWrapped(&rendered, width, "  ", line, styles.text, styles)
	}

	return strings.TrimSuffix(rendered.String(), "\n")
}

func renderMarkdownBlock(width int, content, prefix string) string {
	return renderMarkdownBlockWithTheme(width, content, prefix, apptheme.Default())
}

func renderMarkdownBlockWithTheme(width int, content, prefix string, palette apptheme.Theme) string {
	if content == "" {
		return ""
	}
	available := maxInt(1, width-lipgloss.Width(prefix))
	rendered := renderMarkdownWithTheme(available, content, palette)
	if rendered == "" {
		return ""
	}

	var block strings.Builder
	for _, line := range strings.Split(rendered, "\n") {
		block.WriteString(prefix)
		block.WriteString(line)
		block.WriteString("\n")
	}
	return strings.TrimSuffix(block.String(), "\n")
}

func parseHeading(line string) (int, string) {
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || level == len(line) || line[level] != ' ' {
		return 0, ""
	}
	return level, strings.TrimSpace(line[level+1:])
}

func isHorizontalRule(line string) bool {
	if len(line) < 3 {
		return false
	}
	marker := line[0]
	if marker != '-' && marker != '*' && marker != '_' {
		return false
	}
	count := 0
	for _, r := range line {
		if r == rune(marker) {
			count++
			continue
		}
		if r != rune(marker) && r != ' ' && r != '\t' {
			return false
		}
	}
	return count >= 3
}

func parseQuote(line string) (string, bool) {
	if !strings.HasPrefix(line, ">") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(line, ">")), true
}

func parseUnorderedList(line string) (string, string, bool) {
	indent := len(line) - len(strings.TrimLeft(line, " \t"))
	trimmed := strings.TrimLeft(line, " \t")
	if len(trimmed) < 3 || (trimmed[0] != '-' && trimmed[0] != '*' && trimmed[0] != '+') || trimmed[1] != ' ' {
		return "", "", false
	}
	prefix := strings.Repeat(" ", indent+2) + "• "
	return prefix, strings.TrimSpace(trimmed[2:]), true
}

func parseOrderedList(line string) (string, string, bool) {
	indent := len(line) - len(strings.TrimLeft(line, " \t"))
	trimmed := strings.TrimLeft(line, " \t")
	separator := strings.IndexAny(trimmed, ".)")
	if separator <= 0 || separator+1 >= len(trimmed) || trimmed[separator+1] != ' ' {
		return "", "", false
	}
	for _, r := range trimmed[:separator] {
		if r < '0' || r > '9' {
			return "", "", false
		}
	}
	prefix := strings.Repeat(" ", indent+2) + trimmed[:separator+1] + " "
	return prefix, strings.TrimSpace(trimmed[separator+2:]), true
}

func writeCodeLine(builder *strings.Builder, width int, line string, styles markdownStyles) {
	prefix := "  │ "
	available := maxInt(1, width-lipgloss.Width(prefix))
	wrapped := wrapPlain(line, available)
	if len(wrapped) == 0 {
		wrapped = []string{""}
	}
	for _, part := range wrapped {
		writeStyledLine(builder, width, prefix+part, styles.code)
	}
}

func writeStyledWrapped(builder *strings.Builder, width int, prefix, text string, style lipgloss.Style, styles markdownStyles) {
	available := maxInt(1, width-lipgloss.Width(prefix))
	wrapped := wrapPlain(text, available)
	if len(wrapped) == 0 {
		wrapped = []string{""}
	}
	for _, part := range wrapped {
		builder.WriteString(style.Render(prefix + renderInlineMarkdown(part, styles)))
		builder.WriteString("\n")
		prefix = strings.Repeat(" ", lipgloss.Width(prefix))
	}
}

func writeStyledLine(builder *strings.Builder, width int, line string, style lipgloss.Style) {
	for _, part := range wrapPlain(line, maxInt(1, width)) {
		builder.WriteString(style.Render(part))
		builder.WriteString("\n")
	}
}

func renderInlineMarkdown(text string, styles markdownStyles) string {
	var rendered strings.Builder
	for i := 0; i < len(text); {
		switch {
		case text[i] == '`':
			if end := strings.IndexByte(text[i+1:], '`'); end >= 0 {
				end += i + 1
				rendered.WriteString(styles.inlineCode.Render(text[i+1 : end]))
				i = end + 1
				continue
			}
		case strings.HasPrefix(text[i:], "**"):
			if end := strings.Index(text[i+2:], "**"); end >= 0 {
				end += i + 2
				rendered.WriteString(lipgloss.NewStyle().Bold(true).Render(text[i+2 : end]))
				i = end + 2
				continue
			}
		case text[i] == '*' || text[i] == '_':
			marker := text[i]
			if end := strings.IndexByte(text[i+1:], marker); end >= 0 {
				end += i + 1
				rendered.WriteString(lipgloss.NewStyle().Italic(true).Render(text[i+1 : end]))
				i = end + 1
				continue
			}
		case text[i] == '[':
			if labelEnd := strings.IndexByte(text[i+1:], ']'); labelEnd >= 0 {
				labelEnd += i + 1
				if labelEnd+1 < len(text) && text[labelEnd+1] == '(' {
					if urlEnd := strings.IndexByte(text[labelEnd+2:], ')'); urlEnd >= 0 {
						rendered.WriteString(styles.link.Render(text[i+1 : labelEnd]))
						i = labelEnd + 3 + urlEnd
						continue
					}
				}
			}
		}
		rendered.WriteByte(text[i])
		i++
	}
	return rendered.String()
}

func wrapPlain(text string, width int) []string {
	if text == "" {
		return []string{""}
	}
	return wrapString(text, width)
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
