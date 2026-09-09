package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestRenderMarkdownFormatsCommonBlocks(t *testing.T) {
	content := "# Title\n\n**bold** and *italic* with `code`\n\n- first\n- second\n\n1. ordered\n\n> quote\n\n```go\nfmt.Println(\"ok\")\n```\n\n[link](https://example.com)"
	out := renderMarkdown(60, content)

	for _, want := range []string{"Title", "bold", "italic", "code", "• first", "1. ordered", "quote", "go", "fmt.Println", "link"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered Markdown does not contain %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"# Title", "**bold**", "*italic*", "```", "[link]", "https://example.com"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("rendered Markdown still contains raw %q:\n%s", unwanted, out)
		}
	}
}

func TestRenderMarkdownWrapsRenderedLines(t *testing.T) {
	out := renderMarkdown(24, "A very long paragraph with **formatted** content that must wrap.")
	for _, line := range strings.Split(out, "\n") {
		if width := lipgloss.Width(line); width > 24 {
			t.Errorf("line width = %d, want <= 24: %q", width, line)
		}
	}
}

func TestRenderMarkdownBlockAddsIndentAndGuide(t *testing.T) {
	out := renderMarkdownBlock(32, "**content**\n\n- item", "      │ ")
	if !strings.Contains(out, "      │ ") {
		t.Fatalf("expected guide prefix, got:\n%s", out)
	}
	if !strings.Contains(out, "content") || !strings.Contains(out, "• item") {
		t.Fatalf("expected rendered block content, got:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if width := lipgloss.Width(line); width > 32 {
			t.Errorf("indented line width = %d, want <= 32: %q", width, line)
		}
	}
}

func TestRenderMarkdownPreservesEmptyCodeLines(t *testing.T) {
	out := renderMarkdown(40, "```text\nfirst\n\nlast\n```")
	if !strings.Contains(out, "first") || !strings.Contains(out, "last") {
		t.Fatalf("code content missing:\n%s", out)
	}
	if !strings.Contains(out, "│ ") {
		t.Fatalf("expected code block marker:\n%s", out)
	}
}
