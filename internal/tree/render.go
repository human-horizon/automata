package tree

import (
	"os"
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ── Color palette (semantic, no explicit background) ──────────────
// These work on both light and dark terminals. When NO_COLOR is set
// or Theme=="mono" all lipgloss styling is skipped.
const (
	cHeaderFg   = "#a89984" // muted gray for header text
	cTitleFg    = "#ebdbb2" // warm white for title
	cCollapse   = "#d5c4a1" // light gray for collapse button
	cFolder     = "#d79921" // yellow for folder names
	cChat       = "#ebdbb2" // warm white for chat names
	cArchived   = "#a89984" // gray for archived items
	cSelected   = "#458588" // blue for selected background
	cSelectedFg = "#282828" // dark for selected text
	cStatus     = "#a89984" // gray for status text
	cActive     = "#98971a" // green for active status
	cIdle       = "#a89984" // gray for idle status
	cBranch     = "#504945" // dim for tree branch lines
	cEmpty      = "#a89984" // gray for empty state
	cScrollbar  = "#504945" // dim for scrollbar
)

var (
	headerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(cHeaderFg)).
			Bold(true)

	titleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(cTitleFg))

	collapseStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(cCollapse))

	folderStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(cFolder))

	chatStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(cChat))

	archivedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(cArchived))

	selectedStyle = lipgloss.NewStyle().
			Background(lipgloss.Color(cSelected)).
			Foreground(lipgloss.Color(cSelectedFg))

	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(cStatus))

	activeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(cActive))

	idleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(cIdle))

	branchStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(cBranch))

	emptyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(cEmpty))

	scrollbarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(cScrollbar))

	// ── Modal / popover styles (unchanged from original) ──────────
	modalBorderStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("#3c3836")).
				Foreground(lipgloss.Color("#fbf1c7")).
				BorderStyle(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#d79921")).
				Padding(1, 2)

	modalTitleStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#3c3836")).
			Foreground(lipgloss.Color("#d79921")).
			Bold(true)

	modalInputStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#282828")).
			Foreground(lipgloss.Color("#fbf1c7"))

	modalHintStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#3c3836")).
			Foreground(lipgloss.Color("#a89984"))

	modalCloseStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#3c3836")).
			Foreground(lipgloss.Color("#cc241d"))

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#a89984"))

	actionIconStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#a89984"))

	actionIconHoverStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#d79921"))
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

	if t.Collapsed {
		return t.renderCollapsed(width, height)
	}

	// Reserve one line for header (toolbar is now in the header).
	contentHeight := height - 1
	if contentHeight < 1 {
		contentHeight = 1
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
					styled += scrollbarStyle.Render("▐")
				} else {
					styled += " "
				}
			}

			lines = append(lines, styled)
			t.rowToFlat = append(t.rowToFlat, i)
		}

		// Pad remaining content lines.
		for len(lines) < height {
			pad := strings.Repeat(" ", contentWidth)
			if needsScrollbar {
				pad += " "
			}
			lines = append(lines, pad)
			t.rowToFlat = append(t.rowToFlat, -1)
		}
	} else {
		// Empty state.
		msg := emptyStyle.Width(contentWidth).Render("No chats yet — create one!")
		lines = append(lines, msg)
		t.rowToFlat = append(t.rowToFlat, -1)
		for len(lines) < height {
			pad := strings.Repeat(" ", contentWidth)
			if width > contentWidth {
				pad += " "
			}
			lines = append(lines, pad)
			t.rowToFlat = append(t.rowToFlat, -1)
		}
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

	// Label style.
	var labelStyle lipgloss.Style
	if selected {
		labelStyle = selectedStyle
	} else if item.Archived {
		labelStyle = archivedStyle
	} else if item.IsFolder {
		labelStyle = folderStyle
	} else {
		labelStyle = chatStyle
	}

	// Status style.
	var stStyle lipgloss.Style
	if strings.HasPrefix(status, "●") {
		stStyle = activeStyle
	} else {
		stStyle = idleStyle
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
	stopPart := actionIconStyle.Render(stopIcon)
	menuPart := actionIconStyle.Render(menuIcon)

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
		line = selectedStyle.Width(width).Render(stripANSI(line))
	}

	return line
}

func (t *Tree) actionButton(symbol, action string, hovered bool) string {
	if hovered && t.actionIcon == "hover-"+action {
		return actionIconHoverStyle.Render(symbol)
	}
	return actionIconStyle.Render(symbol)
}

// ── Header ───────────────────────────────────────────────────────

func (t *Tree) renderHeader(width int) string {
	// Title (center-left).
	title := "Automata "
	if t.Profile != "" {
		title = "Automata (" + t.Profile + ") "
	}

	// Toolbar button "+" at the right edge (flush to border/collapse symbol).
	plusBtn := " + "

	// Collapse button removed — now on the warp border.

	nc := t.noColor()

	if nc {
		// Plain text header.
		line := title
		// Pad to make room for "+" at the right edge.
		needed := lipgloss.Width(line) + lipgloss.Width(plusBtn)
		if needed < width {
			line += strings.Repeat(" ", width-needed)
		}
		line += plusBtn
		return line
	}

	// Styled header.
	titlePart := titleStyle.Render(title)
	plusPart := headerStyle.Copy().Foreground(lipgloss.Color("#98971a")).Render(plusBtn)

	line := titlePart
	lineW := lipgloss.Width(stripANSI(line)) + lipgloss.Width(stripANSI(plusPart))
	if lineW < width {
		line += strings.Repeat(" ", width-lineW)
	}
	line += plusPart
	return line
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
		lines[idx] = archivedStyle.Render(line)
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
