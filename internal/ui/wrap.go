package ui

import "github.com/charmbracelet/lipgloss"

// wrapString splits s into lines that fit within the given width.
// It breaks at word boundaries (spaces) when possible, and at any character
// when a single word is longer than width.
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

		// Find the longest prefix that fits within width.
		// Try to break at a space first.
		breakAt := lastSpaceBefore(s, width)
		if breakAt <= 0 {
			// No space found — hard break at width.
			breakAt = findBreakAt(s, width)
		}

		lines = append(lines, s[:breakAt])
		s = s[breakAt:]
	}

	return lines
}

// lastSpaceBefore finds the last space position within the first `width` cells of s.
// Returns 0 if no space is found.
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
		return lastSpace + 1 // include the space in the line
	}
	return 0
}

// findBreakAt finds the byte position where the string exceeds the given width.
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
