package tree

import (
	"os"
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	treeHeaderHeight = 1
	treeFooterHeight = 1
)

// noColor reports whether the tree should skip all ANSI styling.
func (t *Tree) noColor() bool {
	if t.NoColor {
		return true
	}
	if t.Theme == "mono" {
		return true
	}
	// NO_COLOR env var: https://no-color.org/
	_, present := os.LookupEnv("NO_COLOR")
	return present
}

// View renders the tree into a string of the given dimensions.
func (t *Tree) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}

	t.height = height
	t.width = width
	styles := t.styles()

	if t.Collapsed {
		return t.renderCollapsed(width, height)
	}

	contentHeight := height - treeHeaderHeight - treeFooterHeight
	if contentHeight < 1 {
		contentHeight = 1
	}
	contentEnd := treeHeaderHeight + contentHeight
	if contentEnd > height {
		contentEnd = height
	}

	// Content width (leave 1 char for scrollbar).
	contentWidth := width - 1
	if contentWidth < 1 {
		contentWidth = width
	}

	// Header line.
	header := t.renderHeader(width)

	// Build full lines array including header.
	var lines []string
	lines = append(lines, header)

	// Build tree content lines and a screen-row to flat-index map.
	t.clampScroll(contentHeight)

	archiveLines := t.archiveLineRows(width)

	// rowToFlat: screen row 0 = header, rows 1+ = content.
	t.rowToFlat = make([]int, 0, height)
	t.rowToFlat = append(t.rowToFlat, -1)

	if len(t.flat) > 0 && contentHeight > 0 {
		start := t.scroll
		end := start + contentHeight
		if end > len(t.flat) {
			end = len(t.flat)
		}

		needsScrollbar := len(t.flat) > contentHeight
		thumbSize, thumbPos := computeThumb(t.scroll, contentHeight, len(t.flat))

		// Precompute branch info for all visible items.
		branchInfo := computeBranchInfo(t.flat)

		for i := start; i < end; i++ {
			// Render archive separator line before the item it belongs to.
			if sep, ok := archiveLines[i]; ok {
				lines = append(lines, sep)
				t.rowToFlat = append(t.rowToFlat, -1)
			}

			item := t.flat[i]
			isSelected := i == t.selected
			isHovered := i == t.hoverIdx

			styled := t.renderItemLine(item, contentWidth, isSelected, isHovered, branchInfo[i], 0)

			// Scrollbar column.
			if needsScrollbar {
				row := i - start
				if row >= thumbPos && row < thumbPos+thumbSize {
					styled += styles.scrollbarStyle.Render("▐")
				} else {
					styled += " "
				}
			}

			lines = append(lines, styled)
			t.rowToFlat = append(t.rowToFlat, i)
		}

		// Pad remaining content lines.
		for len(lines) < contentEnd {
			pad := strings.Repeat(" ", contentWidth)
			if needsScrollbar {
				pad += " "
			}
			lines = append(lines, pad)
			t.rowToFlat = append(t.rowToFlat, -1)
		}
	} else {
		// Empty state.
		msg := styles.emptyStyle.Width(contentWidth).Render("No chats yet — create one!")
		lines = append(lines, msg)
		t.rowToFlat = append(t.rowToFlat, -1)
		for len(lines) < contentEnd {
			pad := strings.Repeat(" ", contentWidth)
			if width > contentWidth {
				pad += " "
			}
			lines = append(lines, pad)
			t.rowToFlat = append(t.rowToFlat, -1)
		}
	}

	if len(lines) > contentEnd {
		lines = lines[:contentEnd]
		t.rowToFlat = t.rowToFlat[:contentEnd]
	}
	if len(lines) < height {
		lines = append(lines, t.renderFooter(width))
		t.rowToFlat = append(t.rowToFlat, -1)
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
		t.rowToFlat = append(t.rowToFlat, -1)
	}

	// Context menu overlay.
	if t.popover != nil {
		lines = t.popover.Overlay(lines, width, height)
	}

	// Modal overlay (input or confirmation).
	if t.modalActive && t.modal != nil {
		lines = t.modal.Overlay(lines, width, height)
	}

	return strings.Join(lines, "\n")
}

// ── Branch info ──────────────────────────────────────────────────

type branchInfo struct {
	hasNextSibling   bool
	continuationMask []bool // for each depth level < item.Depth()
}

// computeBranchInfo precomputes sibling/continuation info for every item in
// the flat list. Runs in O(n * maxDepth).
func computeBranchInfo(flat []*Item) []branchInfo {
	info := make([]branchInfo, len(flat))

	for i, item := range flat {
		depth := item.Depth()
		info[i].continuationMask = make([]bool, depth)

		// For each depth level, find the ancestor at that depth.
		// If the ancestor is an open folder, show a vertical line.
		for d := 0; d < depth; d++ {
			ancestor := findAncestorAtDepth(flat, i, d)
			if ancestor != nil && ancestor.IsFolder && ancestor.Expanded {
				info[i].continuationMask[d] = true
			}
		}

		// Next sibling at the same depth.
		if i+1 < len(flat) && flat[i+1].Depth() == depth {
			info[i].hasNextSibling = true
		}
	}
	return info
}

// findAncestorAtDepth scans backwards from idx to find the first item at
// targetDepth (the ancestor at that depth level).
func findAncestorAtDepth(flat []*Item, idx, targetDepth int) *Item {
	for j := idx - 1; j >= 0; j-- {
		if flat[j].Depth() == targetDepth {
			return flat[j]
		}
	}
	return nil
}

// branchPrefix returns the tree-drawing prefix for an item. For depth 0
// items (roots) it returns ""; for deeper items it returns 2-space indentation
// per depth level, with a vertical line "│ " at levels where the parent
// folder is open and there are more siblings after the current item.
func branchPrefix(item *Item, bi branchInfo) string {
	depth := item.Depth()
	if depth == 0 {
		return ""
	}

	var b strings.Builder
	for d := 0; d < depth; d++ {
		if bi.continuationMask[d] {
			b.WriteString("│ ")
		} else {
			b.WriteString("  ")
		}
	}
	return b.String()
}

// expandMarker returns the one-character expand/collapse indicator for a
// folder, or a space for leaves (so labels stay aligned).
func expandMarker(item *Item) string {
	if item.IsFolder {
		if item.Expanded {
			return "▾"
		}
		return "▸"
	}
	return " "
}

// ── Status column ────────────────────────────────────────────────

// statusString returns the status symbol + text for an item, or "" if the
// item has no status. The result is used in the right-aligned status column.
func (t *Tree) statusString(item *Item) string {
	if item.IsFolder {
		if t.HasActiveDescendant(item) {
			return "●"
		}
		return ""
	}
	// Active session with a known status badge.
	if !item.IsTerminal {
		if b := t.StatusBadge(item); b != "" {
			// b is an emoji like 🧠📖✏️🔍⚙️💤 — map to spec symbols.
			return statusEmojiToSpec(b)
		}
	}
	// No badge — idle.
	return "○ idle"
}

// statusEmojiToSpec maps the old emoji badges to spec-compliant symbols.
//
// The spec requires "active" to never be shown: a session running without a
// recorded substatus must look idle. Unknown emojis therefore map to "" so
// the caller can fall back to the idle indicator.
func statusEmojiToSpec(emoji string) string {
	word := statusEmojiToWord(emoji)
	if word == "" {
		// "active" or unknown — fall back to idle so the column never shows
		// a confusing "● active" badge.
		return "○ idle"
	}
	return "● " + word
}

// statusEmojiToWord returns the canonical substatus word for a given status
// glyph (the keys SetStatusBadges was given). The mapping mirrors
// status.Emoji() in pkg/status.
//
// "active" is intentionally not handled: a session running without a
// recorded substatus must not be labeled "active" in the tree.
func statusEmojiToWord(emoji string) string {
	switch emoji {
	case "~":
		return "thinking"
	case "R":
		return "read"
	case "W":
		// "W" is shared by "write" and "wait" in the emoji map; pick "write"
		// here for the glyph. The action field on status.json is the source
		// of truth — see status.Word() for the full set.
		return "write"
	case "G":
		return "grep"
	case "F":
		return "find"
	case "A":
		return "analyze"
	case "J":
		return "job"
	case ">":
		return "run"
	case "X":
		return "stopped"
	case "":
		return "idle"
	}
	return ""
}

// ── Line rendering ────────────────────────────────────────────────

func (t *Tree) renderItemLine(item *Item, width int, selected, hovered bool, bi branchInfo, maxLabelWidth int) string {
	prefix := branchPrefix(item, bi)
	expandM := expandMarker(item)
	label := prefix + expandM + " " + item.Name

	// Status column (right-aligned).
	status := t.statusString(item)

	// Action icons: stop button before status, menu icon after status.
	stopIcon := ""
	if t.IsActiveSession(item) && !item.IsFolder {
		stopIcon = " " + t.actionButton("■", "stop", hovered)
	}
	menuIcon := ""
	if hovered || selected {
		menuIcon = " " + t.actionButton("⋮", "menu", hovered)
	}

	// ── Styling ──
	nc := t.noColor()
	styles := t.styles()

	// Label style.
	var labelStyle lipgloss.Style
	if selected {
		labelStyle = styles.selectedStyle
	} else if item.Archived {
		labelStyle = styles.archivedStyle
	} else if item.IsFolder {
		labelStyle = styles.folderStyle
	} else {
		labelStyle = styles.chatStyle
	}

	// Status style.
	var stStyle lipgloss.Style
	if strings.HasPrefix(status, "●") {
		stStyle = styles.activeStyle
	} else {
		stStyle = styles.idleStyle
	}

	// ── Assemble ──
	if nc {
		// Plain text, no ANSI.
		line := label + stopIcon + menuIcon
		if status != "" {
			line = label + stopIcon + menuIcon + " " + status
		}
		if selected {
			line = "> " + line
		}
		lineW := lipgloss.Width(line)
		if lineW < width {
			line += strings.Repeat(" ", width-lineW)
		}
		return line
	}

	// With ANSI colors.
	labelPart := labelStyle.Render(label)
	stopPart := styles.actionIconStyle.Render(stopIcon)
	menuPart := styles.actionIconStyle.Render(menuIcon)

	var line string
	if status != "" {
		statusPart := stStyle.Render(status)
		line = labelPart + stopPart + menuPart + " " + statusPart
	} else {
		line = labelPart + stopPart + menuPart
	}

	plainLine := stripANSI(label) + stripANSI(stopIcon+menuIcon)
	lineW := lipgloss.Width(plainLine)
	if lineW < width {
		line += strings.Repeat(" ", width-lineW)
	}

	// If selected, re-render entire line with selected background to fill width.
	if selected {
		line = styles.selectedStyle.Width(width).Render(stripANSI(line))
	}

	return line
}

func (t *Tree) actionButton(symbol, action string, hovered bool) string {
	styles := t.styles()
	if hovered && t.actionIcon == "hover-"+action {
		return styles.actionIconHoverStyle.Render(symbol)
	}
	return styles.actionIconStyle.Render(symbol)
}

// ── Header ───────────────────────────────────────────────────────

func (t *Tree) renderHeader(width int) string {
	// Title (center-left).
	title := "Automata "
	if t.Profile != "" {
		title = "Automata (" + t.Profile + ") "
	}

	// Toolbar buttons are flush to the right edge before the collapse border.
	rootMenuBtn := " ⋮ "
	plusBtn := " + "

	// Collapse button is rendered by warp on the panel border.
	nc := t.noColor()
	styles := t.styles()

	if nc {
		line := title
		needed := lipgloss.Width(line) + lipgloss.Width(rootMenuBtn) + lipgloss.Width(plusBtn)
		if needed < width {
			line += strings.Repeat(" ", width-needed)
		}
		line += rootMenuBtn + plusBtn
		return line
	}

	titlePart := styles.titleStyle.Render(title)
	rootMenuPart := styles.actionIconHoverStyle.Copy().Bold(true).Render(rootMenuBtn)
	plusPart := styles.activeStyle.Copy().Bold(true).Render(plusBtn)

	line := titlePart
	lineW := lipgloss.Width(stripANSI(line)) + lipgloss.Width(stripANSI(rootMenuPart)) + lipgloss.Width(stripANSI(plusPart))
	if lineW < width {
		line += strings.Repeat(" ", width-lineW)
	}
	line += rootMenuPart + plusPart
	return line
}

func (t *Tree) renderFooter(width int) string {
	label := "? Help  Settings"
	styles := t.styles()
	actionErr := t.lastActionError
	if t.stateLoadErr != nil {
		actionErr = t.stateLoadErr
	}
	warning := ""
	if actionErr != nil {
		message := strings.Join(strings.Fields(actionErr.Error()), " ")
		baseWidth := lipgloss.Width(label)
		if width > baseWidth+3 {
			warning = "  ! " + message
			warning = ansi.Truncate(warning, width-baseWidth, "…")
		} else {
			label = ansi.Truncate("! "+message, width, "…")
		}
	}
	if t.noColor() {
		line := ansi.Truncate(label+warning, width, "")
		return line + strings.Repeat(" ", max(0, width-ansi.StringWidth(line)))
	}
	styled := styles.headerStyle.Render(label)
	if warning != "" {
		styled += styles.modalCloseStyle.Render(warning)
	}
	lineWidth := ansi.StringWidth(styled)
	if lineWidth < width {
		styled += strings.Repeat(" ", width-lineWidth)
	}
	return ansi.Truncate(styled, width, "")
}

func (t *Tree) renderCollapsed(width, height int) string {
	mid := height / 2
	var lines []string
	for r := 0; r < height; r++ {
		if r == mid {
			lines = append(lines, ">")
		} else {
			lines = append(lines, " ")
		}
	}
	return strings.Join(lines, "\n")
}

// ── Archive separators ───────────────────────────────────────────

func (t *Tree) archiveLineRows(width int) map[int]string {
	lines := make(map[int]string)
	styles := t.styles()
	contentWidth := width - 1
	if contentWidth < 1 {
		contentWidth = width
	}

	renderSep := func(depth, idx int) {
		indentStr := indent(depth)
		prefix := indentStr + " "
		lineWidth := contentWidth - lipgloss.Width(prefix)
		if lineWidth < 4 {
			lineWidth = 4
		}
		line := prefix + strings.Repeat("─", lineWidth)
		lines[idx] = styles.archivedStyle.Render(line)
	}

	firstArchivedRoot := -1
	for j, root := range t.root {
		if root.IsFolder && root.Archived {
			firstArchivedRoot = j
			break
		}
	}
	if firstArchivedRoot >= 0 {
		for j, root := range t.root {
			if root == t.root[firstArchivedRoot] {
				renderSep(0, j)
				break
			}
		}
	}

	for i, item := range t.flat {
		if !item.IsFolder || !item.Expanded || len(item.Children) == 0 {
			continue
		}
		firstArchived := -1
		for j, child := range item.Children {
			if child.IsFolder && child.Archived {
				firstArchived = j
				break
			}
		}
		if firstArchived < 0 {
			continue
		}
		for j := i + 1; j < len(t.flat); j++ {
			if t.flat[j] == item.Children[firstArchived] {
				renderSep(item.Depth()+1, j)
				break
			}
		}
	}
	return lines
}

// ── Helpers ──────────────────────────────────────────────────────

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (t *Tree) clampScroll(contentHeight int) {
	if contentHeight <= 0 || len(t.flat) == 0 {
		t.scroll = 0
		return
	}

	maxScroll := len(t.flat) - contentHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	if t.scroll < 0 {
		t.scroll = 0
	}
	if t.scroll > maxScroll {
		t.scroll = maxScroll
	}

	if t.selected >= 0 {
		if t.selected < t.scroll {
			t.scroll = t.selected
		}
		if t.selected >= t.scroll+contentHeight {
			t.scroll = t.selected - contentHeight + 1
			if t.scroll > maxScroll {
				t.scroll = maxScroll
			}
		}
	}
}

// computeThumb returns thumb size and position for the scrollbar.
func computeThumb(scroll, height, total int) (size, pos int) {
	if total <= height {
		return 0, 0
	}
	size = max(1, height*height/total)
	maxPos := total - height
	if maxPos <= 0 {
		return size, 0
	}
	pos = scroll * (height - size) / maxPos
	if pos > height-size {
		pos = height - size
	}
	return size, pos
}
