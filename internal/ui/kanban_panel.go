package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/HumanHorizon/automata/internal/kanban"
	"github.com/HumanHorizon/automata/internal/paths"
	apptheme "github.com/HumanHorizon/automata/internal/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/fsnotify/fsnotify"
	warp "github.com/starframe-dev/warp"
)

// kanbanChangedMsg is sent by watchKanbanCmd when fsnotify reports a change
// in the kanban directory. We use a single-message Cmd that re-arms itself
// after every event so the drain loop costs 0 CPU while idle.
type kanbanChangedMsg struct{}

// ChatInfo describes an AI chat session for the picker.
type ChatInfo struct {
	Name      string
	SessionID string
}

// Column statuses in display order.
var kanbanColumns = []struct {
	status string
	label  string
}{
	{"todo", "Todo"},
	{"pending", "Pending"},
	{"progress", "Progress"},
	{"done", "Done"},
}

// Status transitions: current → button label → next status
type transition struct {
	label string
	next  string
}

var statusTransitions = map[string][]transition{
	"todo":     {{"→ Pending", "pending"}},
	"pending":  {{"↩ Todo", "todo"}, {"→ Другой чат", "reassign"}},
	"progress": {{"↩ Todo", "todo"}},
	// Done can be reopened to Pending (but not to Progress — guarded by
	// kanban.UpdateStatus which returns ErrDoneToProgressForbidden).
	"done": {{"→ Pending", "pending"}},
}

// KanbanPanel renders a full kanban board using warp layout.
type KanbanPanel struct {
	profile string
	domain  string
	palette apptheme.Theme
	tasks   []kanban.Task
	tab     *warp.Tab

	btnPanel  *kanbanBtnPanel
	colPanels []*kanbanColPanel

	chats []ChatInfo // available AI chats for the picker

	// sessionNames maps sessionID → chat name for display in columns
	sessionNames map[string]string

	// pendingTask is set when user clicks "→ Pending" — shows chat picker
	pendingTask *kanban.Task

	// pickerHover is the index of the hovered chat in the picker
	pickerHover int

	// lastRefresh tracks the last time tasks were reloaded from disk
	lastRefresh time.Time

	// watcher observes the kanban directory for file changes and triggers
	// immediate reloads so external edits show up in the UI without waiting
	// for the periodic poll.
	watcher *fsnotify.Watcher

	// watchPending is true while a watchKanbanCmd is already in flight — it
	// blocks on watcher.Events and is re-armed after every event.
	watchPending bool

	// onTaskAssigned is called when a task is assigned to a chat
	onTaskAssigned func(sessionID, taskTitle string)

	width  int
	height int

	// activeCol is the index of the column that responds to ↑↓ keyboard
	// navigation. Clicking a column or pressing ←→ changes it.
	activeCol int
}

// NewKanbanPanel creates a new kanban panel.
func NewKanbanPanel(profile string) *KanbanPanel {
	k := &KanbanPanel{
		profile:      profile,
		palette:      apptheme.Default(),
		tab:          warp.NewTab("kanban"),
		btnPanel:     newKanbanBtnPanel(),
		colPanels:    make([]*kanbanColPanel, len(kanbanColumns)),
		pickerHover:  -1,
		sessionNames: make(map[string]string),
	}

	k.btnPanel.parent = k

	for i := range kanbanColumns {
		k.colPanels[i] = newKanbanColPanel(i)
		k.colPanels[i].parent = k
	}

	// Note: we deliberately do NOT use k.tab.FlexColumn / FlexRow here.
	// Those layouts always draw a horizontal ─ border between rows and
	// vertical │ borders between columns, which we don't want for kanban.
	// View() renders manually; Update() still routes through k.tab.Update
	// (which is a no-op since the tree is empty), and handleMouse routes
	// clicks itself based on whether Y is on the button row or a column.

	return k
}

// SetTheme updates the palette used by the Kanban panel.
func (k *KanbanPanel) SetTheme(palette apptheme.Theme) {
	k.palette = palette
}

// SetDomain reloads tasks for the given domain.
func (k *KanbanPanel) SetDomain(domain string) {
	if k.domain == domain {
		return
	}
	k.domain = domain
	k.pendingTask = nil
	k.activeCol = 0
	k.closeWatcher()
	k.reload()
	k.setupWatcher()
	k.watchPending = false // ensure a fresh watcher cmd is scheduled on the next Update
}

// setupWatcher attaches an fsnotify.Watcher to the domain's kanban
// directory so any external edit to a task .md file is reflected in the UI
// without waiting for the next periodic poll. The actual drain happens in
// Update via kanbanChangedMsg.
func (k *KanbanPanel) setupWatcher() {
	if k.domain == "" {
		return
	}
	dir := kanban.KanbanDir(k.domain, k.profile)
	if _, err := os.Stat(dir); err != nil {
		return // directory may not exist yet; periodic tick will create it
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}
	if err := w.Add(dir); err != nil {
		w.Close()
		return
	}
	k.watcher = w
}

// closeWatcher stops and releases the file watcher if one is attached.
func (k *KanbanPanel) closeWatcher() {
	if k.watcher != nil {
		k.watcher.Close()
		k.watcher = nil
	}
}

// watchKanbanCmd blocks on the fsnotify event channel and returns a single
// kanbanChangedMsg when an event arrives. Update re-arms it after every
// event so the watcher stays alive without a busy heartbeat.
func (k *KanbanPanel) watchKanbanCmd() tea.Cmd {
	if k.watcher == nil {
		return nil
	}
	w := k.watcher
	return func() tea.Msg {
		// Block until *any* fsnotify event lands. This is the only goroutine
		// inside Bubble Tea we deliberately use, and it sleeps cheaply while
		// idle — no CPU.
		ev, ok := <-w.Events
		if !ok {
			return nil // watcher closed
		}
		if !strings.HasSuffix(ev.Name, ".md") {
			// Ignore non-md noise (e.g. .swp files) — re-arm to wait for the
			// next event.
			return kanbanChangedMsg{}
		}
		if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
			return kanbanChangedMsg{}
		}
		// Permission events, etc. — still a change worth picking up.
		return kanbanChangedMsg{}
	}
}

// SetChats updates the list of available AI chats for the picker.
func (k *KanbanPanel) SetChats(chats []ChatInfo) {
	k.chats = chats
	k.sessionNames = make(map[string]string, len(chats))
	for _, c := range chats {
		k.sessionNames[c.SessionID] = c.Name
	}
}

// SetOnTaskAssigned sets a callback that fires when a task is assigned to a chat.
func (k *KanbanPanel) SetOnTaskAssigned(fn func(sessionID, taskTitle string)) {
	k.onTaskAssigned = fn
}

func (k *KanbanPanel) reload() {
	k.tasks = nil
	if k.domain == "" {
		return
	}
	tasks, err := kanban.ReadAll(k.domain, k.profile)
	if err != nil {
		return
	}
	k.tasks = tasks
	k.distribute()
	// Reset scroll offsets on reload so the view doesn't jump to a stale
	// position after tasks change.
	for _, cp := range k.colPanels {
		cp.scrollOffset = 0
	}
}

func (k *KanbanPanel) distribute() {
	groups := make(map[string][]kanban.Task)
	for _, col := range kanbanColumns {
		groups[col.status] = nil
	}
	for _, t := range k.tasks {
		s := t.Status
		if _, ok := groups[s]; !ok {
			s = "todo"
		}
		groups[s] = append(groups[s], t)
	}
	for i, col := range kanbanColumns {
		k.colPanels[i].setTasks(groups[col.status])
	}
}

// Update implements warp.Panel.
func (k *KanbanPanel) Update(msg tea.Msg) tea.Cmd {
	var baseCmd tea.Cmd
	switch msg := msg.(type) {
	case tea.MouseMsg:
		baseCmd = k.handleMouse(msg)
	case warp.ResizeMsg:
		k.width = msg.Width
		k.height = msg.Height
		baseCmd = k.tab.Update(msg)
	case tea.WindowSizeMsg:
		k.width = msg.Width
		k.height = msg.Height
		baseCmd = k.tab.Update(msg)
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			k.pendingTask = nil
			return nil
		case "up":
			if k.activeCol >= 0 && k.activeCol < len(k.colPanels) {
				cp := k.colPanels[k.activeCol]
				if cp.scrollOffset > 0 {
					cp.scrollOffset--
				}
			}
		case "down":
			if k.activeCol >= 0 && k.activeCol < len(k.colPanels) {
				cp := k.colPanels[k.activeCol]
				maxOff := cp.maxScrollOffset()
				if cp.scrollOffset < maxOff {
					cp.scrollOffset++
				}
			}
		case "left":
			if k.activeCol > 0 {
				k.activeCol--
			}
		case "right":
			if k.activeCol < len(kanbanColumns)-1 {
				k.activeCol++
			}
		}
		baseCmd = k.tab.Update(msg)
	case kanbanChangedMsg:
		// fsnotify reported a change — reload and re-arm the watcher.
		k.watchPending = false
		k.reload()
		k.lastRefresh = time.Now()
		baseCmd = nil
	default:
		baseCmd = k.tab.Update(msg)
	}

	// Re-arm the blocking fsnotify Cmd so we keep receiving changes.
	// This is the only goroutine we use inside Bubble Tea — it sleeps on
	// the watcher's Events channel and costs 0 CPU while idle.
	if k.watcher != nil && !k.watchPending {
		k.watchPending = true
		baseCmd = tea.Batch(baseCmd, k.watchKanbanCmd())
	}
	return baseCmd
}

// drainWatcher is no longer used — the blocking watchKanbanCmd handles all
// change detection. Kept as a stub so external callers that referenced it
// still compile.
func (k *KanbanPanel) drainWatcher() {}

func (k *KanbanPanel) handleMouse(msg tea.MouseMsg) tea.Cmd {
	// If picker is open, handle picker clicks
	if k.pendingTask != nil {
		switch msg.Button {
		case tea.MouseButtonLeft:
			if msg.Action == tea.MouseActionPress {
				// Check if click is on a chat item
				idx := k.pickerHitTest(msg.Y)
				if idx >= 0 && idx < len(k.chats) {
					// Assign task to selected chat — сразу в PROGRESS
					chat := k.chats[idx]
					kanban.AssignTask(k.pendingTask.Path, chat.SessionID)

					// Check if chat already has a task in progress
					hasProgress := false
					for _, t := range k.tasks {
						if t.AssignedTo == chat.SessionID && t.Status == "progress" {
							hasProgress = true
							break
						}
					}

					status := "progress"
					if hasProgress {
						status = "pending" // queue if already busy
					}
					kanban.UpdateStatus(k.pendingTask.Path, status)
					// Write to chat's status.json so the agent picks it up
					writeTaskToChatStatus(chat.SessionID, k.pendingTask.Title, k.pendingTask.Path)
					// Notify the chat directly via callback
					if k.onTaskAssigned != nil {
						k.onTaskAssigned(chat.SessionID, k.pendingTask.Title)
					}
					k.pendingTask = nil
					k.reload()
				} else if msg.Y >= k.pickerOffset() {
					// Click outside chat list — close picker
					k.pendingTask = nil
				}
			}
		case tea.MouseButtonNone:
			idx := k.pickerHitTest(msg.Y)
			k.pickerHover = idx
		}
		return nil
	}

	// Manual routing — we don't use warp.FlexColumn / FlexRow because they
	// always draw borders we don't want.
	//
	// Layout:
	//   y=0  : blank line (top padding)
	//   y=1  : button row
	//   y=2  : blank line (board top padding)
	//   y=3+ : column area
	//
	// X determines the column: each column is exactly colW wide.
	colW := k.colWidth()

	// Reset ALL hovers at the start — will be set below if mouse is on
	// a specific element. This prevents hover from sticking when the
	// mouse moves to a blank line or leaves the kanban area.
	k.btnPanel.hover = false
	for _, cp := range k.colPanels {
		cp.hoverRow = -1
		cp.hoverBtn = ""
	}

	if msg.Y == 1 {
		// Button row
		if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
			k.createTask()
		}
		k.btnPanel.hover = msg.Button == tea.MouseButtonNone
		return nil
	}

	// Blank lines (y=0, y=2) — hovers already reset above, nothing to do.
	if msg.Y == 0 || msg.Y == 2 {
		return nil
	}

	colIdx := msg.X / colW
	if colIdx < 0 || colIdx >= len(k.colPanels) {
		return nil
	}

	// Click on a column makes it active.
	if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
		k.activeCol = colIdx
	}

	// Wheel events scroll the column under the cursor.
	if msg.Button == tea.MouseButtonWheelUp {
		cp := k.colPanels[colIdx]
		if cp.scrollOffset > 0 {
			cp.scrollOffset--
		}
		return nil
	}
	if msg.Button == tea.MouseButtonWheelDown {
		cp := k.colPanels[colIdx]
		maxOff := cp.maxScrollOffset()
		if cp.scrollOffset < maxOff {
			cp.scrollOffset++
		}
		return nil
	}

	cp := k.colPanels[colIdx]
	colX := msg.X - colIdx*colW
	// colY relative to column: columns start at y=3 in the kanban layout
	// (y=0 blank, y=1 button, y=2 blank, y=3+ columns).
	colY := msg.Y - 3

	// Synthesize a mouse message for the target column so the existing
	// colPanel.handleMouse / hitTest path runs unchanged.
	relMsg := tea.MouseMsg{
		X:      colX,
		Y:      colY,
		Action: msg.Action,
		Button: msg.Button,
	}
	// Clear hover on the other columns so only one column shows hover state.
	for i, other := range k.colPanels {
		if i != colIdx {
			other.hoverRow = -1
			other.hoverBtn = ""
		}
	}
	return cp.Update(relMsg)
}

// colWidth returns the per-column width based on the current panel width.
func (k *KanbanPanel) colWidth() int {
	if k.width <= 0 {
		return 20
	}
	w := k.width / len(kanbanColumns)
	if w < 4 {
		w = 4
	}
	return w
}

// pickerOffset returns the Y offset where the picker starts.
func (k *KanbanPanel) pickerOffset() int {
	return 3 // header + some padding
}

// pickerHitTest returns the chat index at the given Y position, or -1.
func (k *KanbanPanel) pickerHitTest(y int) int {
	offset := k.pickerOffset()
	idx := y - offset
	if idx < 0 || idx >= len(k.chats) {
		return -1
	}
	return idx
}

// View implements warp.Panel. Layout:
//
//	y=0             : button row (1 line, with horizontal padding)
//	y=1..height-1   : column area (no borders between columns or above them)
//
// We render the columns manually instead of using warp.FlexColumn / FlexRow
// because those layouts always draw horizontal ─ borders between rows and
// vertical │ borders between columns, which we don't want here.
func (k *KanbanPanel) View(width, height int) string {
	k.width = width
	k.height = height

	// If picker is open, show it instead of the board
	if k.pendingTask != nil {
		return k.renderPicker(width, height)
	}

	if height < 1 {
		return ""
	}

	// Reserve the top three rows for the surrounding chrome:
	//   y=0  blank line  (top padding for the button)
	//   y=1  button row
	//   y=2  blank line  (top padding for the board)
	const reserved = 3
	if height < reserved {
		// Panel too short — fall back to whatever fits without crashing.
		return compactFallback(width, height)
	}

	topPad := strings.Repeat(" ", width)
	btnLine := k.btnPanel.View(width, 1)
	boardTopPad := strings.Repeat(" ", width)

	colHeight := height - reserved
	colW := k.colWidth()

	// Render each column and gather its lines into colLines[y].
	colLines := make([]string, colHeight)
	for i, cp := range k.colPanels {
		cp.width = colW
		cp.height = colHeight
		cp.isActive = i == k.activeCol
		col := cp.View(colW, colHeight)
		// Pad every rendered line to colW so the rightmost column isn't
		// truncated on narrow screens.
		for y, ln := range strings.Split(col, "\n") {
			if y >= colHeight {
				break
			}
			if w := lipgloss.Width(ln); w < colW {
				ln += strings.Repeat(" ", colW-w)
			}
			colLines[y] += ln
		}
	}

	// Fill any missing rows with blank lines of full width.
	for y, ln := range colLines {
		if w := lipgloss.Width(ln); w < width {
			colLines[y] = ln + strings.Repeat(" ", width-w)
		}
	}

	return topPad + "\n" + btnLine + "\n" + boardTopPad + "\n" + strings.Join(colLines, "\n")
}

// compactFallback produces `height` blank rows when the panel is shorter than
// the reserved chrome. Without this guard a tiny resize (height=2) would
// skip drawing the button and leave the user with an empty board.
func compactFallback(width, height int) string {
	if height <= 0 {
		return ""
	}
	if width < 0 {
		width = 0
	}
	row := strings.Repeat(" ", width)
	return strings.Repeat(row+"\n", height-1) + row
}

func (k *KanbanPanel) renderPicker(width, height int) string {
	styles := k.styles()
	var b strings.Builder

	// Header
	header := fmt.Sprintf(" Выберите чат для задачи \"%s\"", k.pendingTask.Title)
	b.WriteString(styles.title.Render(header))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", width))
	b.WriteString("\n")

	// Chat list
	for i, chat := range k.chats {
		style := styles.item
		if i == k.pickerHover {
			style = styles.cardHover
		}
		line := fmt.Sprintf("  💬 %s", chat.Name)
		if lipgloss.Width(line) > width {
			line = line[:width]
		}
		b.WriteString(style.Render(line))
		b.WriteString("\n")
	}

	// Pad
	lines := strings.Split(b.String(), "\n")
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

func (k *KanbanPanel) createTask() {
	if k.domain == "" {
		return
	}
	dir := filepath.Join(paths.DomainDir(k.profile, k.domain), "kanban")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	ts := time.Now().Format("2006-01-02T15-04-05")
	name := fmt.Sprintf("task-%s.md", ts)
	content := `---
title: Новая задача
status: todo
---
`
	os.WriteFile(filepath.Join(dir, name), []byte(content), 0644)
	k.reload()
}

// --- Button panel ---

type kanbanBtnPanel struct {
	hover  bool
	parent *KanbanPanel
}

func newKanbanBtnPanel() *kanbanBtnPanel {
	return &kanbanBtnPanel{}
}

func (b *kanbanBtnPanel) View(width, height int) string {
	text := " + Новая задача "
	styles := newKanbanStyles(apptheme.Default())
	if b.parent != nil {
		styles = b.parent.styles()
	}
	style := styles.button
	if b.hover {
		style = styles.buttonHover
	}
	// Wrap in an outer style that gives the button a small horizontal margin
	// so it doesn't sit flush against the left edge of the panel.
	rendered := style.Render(text)
	wrapped := lipgloss.NewStyle().
		Padding(0, 1).
		Render(rendered)
	w := lipgloss.Width(wrapped)
	if w < width {
		wrapped += strings.Repeat(" ", width-w)
	}
	return wrapped
}

func (b *kanbanBtnPanel) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.MouseMsg:
		if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
			if b.parent != nil {
				b.parent.createTask()
			}
		}
		b.hover = msg.Button == tea.MouseButtonNone
	}
	return nil
}

// --- Column panel ---

type kanbanColPanel struct {
	colIndex int
	tasks    []kanban.Task
	width    int
	height   int
	hoverRow int
	hoverBtn string // which button is hovered: "transit", "delete", or ""
	parent   *KanbanPanel

	// scrollOffset is the number of lines scrolled past from the top of the
	// column. Used when tasks don't fit vertically.
	scrollOffset int

	// isActive is true when this column is the active column for keyboard
	// navigation. Set by the parent KanbanPanel before rendering.
	isActive bool
}

func newKanbanColPanel(colIndex int) *kanbanColPanel {
	return &kanbanColPanel{colIndex: colIndex, hoverRow: -1}
}

func (c *kanbanColPanel) setTasks(tasks []kanban.Task) {
	c.tasks = tasks
}

func (c *kanbanColPanel) View(width, height int) string {
	c.width = width
	c.height = height
	col := kanbanColumns[c.colIndex]
	styles := newKanbanStyles(apptheme.Default())
	if c.parent != nil {
		styles = c.parent.styles()
	}

	var b strings.Builder

	// Header — always visible, not affected by scrollOffset.
	indicator := ""
	if c.isActive {
		indicator = "▎"
	}
	header := fmt.Sprintf("%s %s (%d) ", indicator, col.label, len(c.tasks))
	header = padOrTruncate(header, width)
	b.WriteString(styles.columns[c.colIndex].Render(header))
	b.WriteString("\n")

	// scrollOffset is the number of content lines (after header) to skip.
	// Walk through cards to find the first visible one and how many lines
	// within it to skip.
	type cardInfo struct {
		index      int
		totalLines int // cardTotalLines(task), no gap
	}
	var cards []cardInfo
	for i, task := range c.tasks {
		cards = append(cards, cardInfo{index: i, totalLines: cardTotalLines(task)})
	}

	firstVisible := 0
	skipWithinCard := 0 // lines to skip within the first visible card
	linesPassed := 0
	for _, ci := range cards {
		if linesPassed+ci.totalLines > c.scrollOffset {
			// This card is partially or fully visible.
			skipWithinCard = c.scrollOffset - linesPassed
			firstVisible = ci.index
			break
		}
		linesPassed += ci.totalLines + 1 // +1 for gap after card
		firstVisible = ci.index + 1
	}
	if firstVisible > len(c.tasks) {
		firstVisible = len(c.tasks)
	}

	// Render visible cards.
	linesUsed := 1 // header
	for i := firstVisible; i < len(c.tasks); i++ {
		task := c.tasks[i]
		isHover := i == c.hoverRow
		cardLines := c.renderCard(task, isHover)

		// If this is the first visible card and we need to skip some lines
		// within it, slice the card lines.
		startLine := 0
		if i == firstVisible && skipWithinCard > 0 {
			startLine = skipWithinCard
		}
		visibleLines := cardLines[startLine:]

		if linesUsed+len(visibleLines) > height {
			// Card doesn't fit — show what we can and stop.
			spaceLeft := height - linesUsed
			if spaceLeft > 0 {
				for _, ln := range visibleLines[:spaceLeft] {
					b.WriteString(ln)
					b.WriteString("\n")
				}
				linesUsed += spaceLeft
			}
			break
		}
		for _, ln := range visibleLines {
			b.WriteString(ln)
			b.WriteString("\n")
		}
		linesUsed += len(visibleLines)

		// Gap between cards (skip after the last one).
		if linesUsed < height && i < len(c.tasks)-1 {
			b.WriteString(strings.Repeat(" ", width))
			b.WriteString("\n")
			linesUsed++
		}
	}

	// Fill remaining lines with blanks.
	for i := linesUsed; i < height; i++ {
		b.WriteString("\n")
	}
	return b.String()
}

// renderCard returns the card for the given task as a slice of pre-styled
// lines including top/bottom borders. The card has 2 internal padding columns
// (one on each side of the content), so the inner width is width-2.
func (c *kanbanColPanel) renderCard(task kanban.Task, isHover bool) []string {
	palette := apptheme.Default()
	styles := newKanbanStyles(palette)
	if c.parent != nil {
		palette = c.parent.palette
		styles = c.parent.styles()
	}
	width := c.width
	if width < 4 {
		width = 4
	}
	inner := width - 2

	borderStyle := styles.border
	bgStyle := styles.background
	if isHover {
		borderStyle = styles.border.Copy().Foreground(lipgloss.Color(palette.Border))
		bgStyle = styles.backgroundHover
	}

	// Top border
	top := "┌" + strings.Repeat("─", inner) + "┐"
	if isHover {
		top = borderStyle.Render("┌" + strings.Repeat("─", inner) + "┐")
	}

	// Bottom border
	bot := "└" + strings.Repeat("─", inner) + "┘"
	if isHover {
		bot = borderStyle.Render("└" + strings.Repeat("─", inner) + "┘")
	}

	wrapLine := func(content string) string {
		// Pad/truncate the visible content to fit inside the card.
		if lipgloss.Width(content) > inner {
			content = content[:inner]
		}
		pad := strings.Repeat(" ", inner-lipgloss.Width(content))
		styled := bgStyle.Render(content + pad)
		left := borderStyle.Render("│")
		right := borderStyle.Render("│")
		return left + styled + right
	}

	// Title line — reserves 2 chars at the right edge for the always-visible ×
	titleMax := inner - 2
	if titleMax < 1 {
		titleMax = 1
	}
	title := task.Title
	if lipgloss.Width(title) > titleMax {
		title = title[:titleMax]
	}
	titlePad := strings.Repeat(" ", titleMax-lipgloss.Width(title))
	del := " ×"
	if isHover && c.hoverBtn == "delete" {
		del = styles.delete.Render(" ×")
	}
	titleContent := title + titlePad + del
	titleLine := wrapLine(titleContent)

	lines := []string{top, titleLine}

	// Assigned to (only if assigned)
	if task.AssignedTo != "" {
		name := task.AssignedTo
		if c.parent != nil {
			if n, ok := c.parent.sessionNames[task.AssignedTo]; ok {
				name = n
			}
		}
		assigned := fmt.Sprintf("  👤 %s", name)
		if lipgloss.Width(assigned) > inner {
			assigned = assigned[:inner]
		}
		styled := styles.assigned.Render(assigned)
		// Pad to fill the inner width after styling.
		visW := lipgloss.Width(styled)
		if visW < inner {
			styled += strings.Repeat(" ", inner-visW)
		}
		left := borderStyle.Render("│")
		right := borderStyle.Render("│")
		lines = append(lines, left+bgStyle.Render(strings.Repeat(" ", 0)+styled)+right)
	}

	// Substatus — shown only while the task is in progress and a substatus
	// has been recorded. Mirrors the tree's status column.
	if task.Substatus != "" {
		sub := fmt.Sprintf("  ↳ %s", task.Substatus)
		if lipgloss.Width(sub) > inner {
			sub = sub[:inner]
		}
		styled := styles.substatus.Render(sub)
		visW := lipgloss.Width(styled)
		pad := ""
		if visW < inner {
			pad = strings.Repeat(" ", inner-visW)
		}
		left := borderStyle.Render("│")
		right := borderStyle.Render("│")
		lines = append(lines, left+styled+bgStyle.Render(pad)+right)
	}

	// Transition buttons — always visible
	for _, t := range statusTransitions[task.Status] {
		btnText := " " + t.label + " "
		btnStyle := styles.transit
		if isHover && c.hoverBtn == t.next {
			btnStyle = styles.transitHover
		}
		btn := btnStyle.Render(btnText)
		visW := lipgloss.Width(btn)
		pad := ""
		if visW < inner {
			pad = strings.Repeat(" ", inner-visW)
		}
		left := borderStyle.Render("│")
		right := borderStyle.Render("│")
		lines = append(lines, left+btn+bgStyle.Render(pad)+right)
	}

	// File link
	label := " Файл задачи"
	href := "file://" + task.Path
	hyperlinked := fmt.Sprintf("\x1b]8;;%s\x1b\\%s\x1b]8;;\x1b\\", href, label)
	link := styles.cardPath.Render(hyperlinked)
	visW := lipgloss.Width(link)
	pad := ""
	if visW < inner {
		pad = strings.Repeat(" ", inner-visW)
	}
	left := borderStyle.Render("│")
	right := borderStyle.Render("│")
	lines = append(lines, left+link+bgStyle.Render(pad)+right)

	lines = append(lines, bot)
	return lines
}

func (c *kanbanColPanel) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.MouseMsg:
		return c.handleMouse(msg)
	case warp.ResizeMsg:
		c.width = msg.Width
		c.height = msg.Height
	}
	return nil
}

func (c *kanbanColPanel) handleMouse(msg tea.MouseMsg) tea.Cmd {
	switch msg.Button {
	case tea.MouseButtonLeft:
		if msg.Action == tea.MouseActionPress {
			row, btn := c.hitTest(msg.Y, msg.X)
			if row >= 0 && row < len(c.tasks) {
				switch btn {
				case "delete":
					os.Remove(c.tasks[row].Path)
					if c.parent != nil {
						c.parent.reload()
					}
				case "reassign":
					// Open chat picker for reassignment
					if c.parent != nil {
						task := c.tasks[row]
						c.parent.pendingTask = &task
						c.parent.pickerHover = -1
					}
				case "pending":
					// Open chat picker
					if c.parent != nil {
						task := c.tasks[row]
						c.parent.pendingTask = &task
						c.parent.pickerHover = -1
					}
				case "todo", "progress", "done":
					task := c.tasks[row]
					// If moving from progress to todo, notify the chat
					if task.Status == "progress" && btn == "todo" && task.AssignedTo != "" {
						writeTaskRemovedFromChat(task.AssignedTo, task.Title)
					}
					kanban.UpdateStatus(task.Path, btn)
					if btn == "todo" {
						kanban.AssignTask(task.Path, "") // clear assignment
					}
					if c.parent != nil {
						c.parent.reload()
					}
				default:
					// Click on card — open file
					exec.Command("zed", c.tasks[row].Path).Start()
				}
			}
		}
	case tea.MouseButtonNone:
		row, btn := c.hitTest(msg.Y, msg.X)
		c.hoverRow = row
		c.hoverBtn = btn
	}
	return nil
}

// cardContentLines returns the number of content lines inside a card (title
// + assigned + substatus + transitions + file link), excluding the top/bottom
// borders. Both View and hitTest use this to keep vertical positions in
// sync.
func cardContentLines(task kanban.Task) int {
	n := 2 // title + file link
	if task.AssignedTo != "" {
		n++
	}
	if task.Substatus != "" {
		n++
	}
	n += len(statusTransitions[task.Status])
	return n
}

// cardTotalLines returns the total number of screen lines occupied by a card,
// including top/bottom borders but not the trailing gap between cards.
func cardTotalLines(task kanban.Task) int {
	return cardContentLines(task) + 2
}

// maxScrollOffset returns the maximum scroll offset for this column.
// It is the number of lines that can be scrolled past before the last
// task is fully visible at the bottom of the column.
func (c *kanbanColPanel) maxScrollOffset() int {
	total := 1 // header
	for _, task := range c.tasks {
		total += cardTotalLines(task) + 1 // +1 for gap after card
	}
	if total <= c.height {
		return 0
	}
	return total - c.height
}

// hitTest returns (taskIndex, button) for the given Y, X position.
// button is: "delete", a status name, or "" for the card body.
//
// Screen layout (line numbers are 0-based):
//
//	y=0: column header
//	y=1: top border of card 0
//	y=2: title of card 0
//	y=3+: assigned / transitions / file / bottom border
//	... then a 1-line gap before the next card
func (c *kanbanColPanel) hitTest(y, x int) (int, string) {
	if y < 1 {
		return -1, ""
	}
	// Convert visible Y to actual Y by adding scrollOffset.
	// Header (y=0) is always visible and not affected by scroll.
	actualY := y + c.scrollOffset

	line := 1 // first content line — corresponds to the top border of card 0
	for i, task := range c.tasks {
		// Top border
		if actualY == line {
			return i, "" // borders are visual only; clicks fall through to body
		}
		line++

		// Title line — detect × click on the right edge
		if actualY == line {
			if x >= c.width-3 && x < c.width-1 {
				return i, "delete"
			}
			return i, ""
		}
		line++

		// Assigned to line
		if task.AssignedTo != "" {
			if actualY == line {
				return i, ""
			}
			line++
		}

		// Substatus line (only if set)
		if task.Substatus != "" {
			if actualY == line {
				return i, ""
			}
			line++
		}

		// Transition buttons
		transitions := statusTransitions[task.Status]
		for _, t := range transitions {
			if actualY == line {
				return i, t.next
			}
			line++
		}

		// File link line
		if actualY == line {
			return i, ""
		}
		line++

		// Bottom border
		if actualY == line {
			return i, "" // same as top border
		}
		line++

		// Gap between cards (1 blank line), except after the last card
		if i < len(c.tasks)-1 {
			if actualY == line {
				return -1, ""
			}
			line++
		}
	}
	return -1, ""
}

// --- Helpers ---

// writeTaskToChatStatus writes a task assignment to the chat's status.json
// so the just-pi extension can pick it up on agent_end.
func writeTaskToChatStatus(sessionID, taskTitle, taskPath string) {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "/Users/a"
	}
	base := os.Getenv("AI_DATA_HOME")
	if base == "" {
		base = filepath.Join(home, ".ai", "automata")
	}
	// Extract profile from sessionID (before __)
	profile := "default"
	if idx := strings.Index(sessionID, "__"); idx > 0 {
		p := strings.ToLower(strings.TrimSpace(sessionID[:idx]))
		var b strings.Builder
		for _, r := range p {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				b.WriteRune(r)
			} else if b.Len() > 0 && b.String()[b.Len()-1] != '-' {
				b.WriteRune('-')
			}
		}
		profile = strings.Trim(b.String(), "-")
	}
	statusPath := filepath.Join(base, "profiles", profile, "sessions", sessionID, "status.json")

	// Read existing status
	var status map[string]interface{}
	if data, err := os.ReadFile(statusPath); err == nil {
		json.Unmarshal(data, &status)
	}
	if status == nil {
		status = make(map[string]interface{})
	}
	status["action"] = "task_assigned"
	status["task_title"] = taskTitle
	status["task_path"] = taskPath
	status["substatus"] = ""
	status["updatedAt"] = time.Now().Format(time.RFC3339)

	os.MkdirAll(filepath.Dir(statusPath), 0755)
	data, _ := json.MarshalIndent(status, "", "  ")
	os.WriteFile(statusPath, data, 0644)
}

// writeTaskRemovedFromChat notifies the chat that a task was removed from progress.
func writeTaskRemovedFromChat(sessionID, taskTitle string) {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "/Users/a"
	}
	base := os.Getenv("AI_DATA_HOME")
	if base == "" {
		base = filepath.Join(home, ".ai", "automata")
	}
	profile := "default"
	if idx := strings.Index(sessionID, "__"); idx > 0 {
		p := strings.ToLower(strings.TrimSpace(sessionID[:idx]))
		var b strings.Builder
		for _, r := range p {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				b.WriteRune(r)
			} else if b.Len() > 0 && b.String()[b.Len()-1] != '-' {
				b.WriteRune('-')
			}
		}
		profile = strings.Trim(b.String(), "-")
	}
	statusPath := filepath.Join(base, "profiles", profile, "sessions", sessionID, "status.json")

	// Debug: log the path
	logFile := filepath.Join(home, ".automata-debug.log")
	logLine := fmt.Sprintf("[%s] writeTaskToChatStatus sessionID=%q profile=%q path=%q\n", time.Now().Format(time.RFC3339), sessionID, profile, statusPath)
	os.MkdirAll(filepath.Dir(logFile), 0755)
	os.WriteFile(logFile, []byte(logLine), 0644)

	var status map[string]interface{}
	if data, err := os.ReadFile(statusPath); err == nil {
		json.Unmarshal(data, &status)
	}
	if status == nil {
		status = make(map[string]interface{})
	}
	status["action"] = "task_removed"
	status["task_title"] = taskTitle
	status["substatus"] = ""
	status["updatedAt"] = time.Now().Format(time.RFC3339)

	os.MkdirAll(filepath.Dir(statusPath), 0755)
	data, _ := json.MarshalIndent(status, "", "  ")
	os.WriteFile(statusPath, data, 0644)
}

type voidPanel struct{}

func (voidPanel) View(width, height int) string { return "" }
func (voidPanel) Update(msg tea.Msg) tea.Cmd    { return nil }

func padOrTruncate(s string, w int) string {
	if lipgloss.Width(s) >= w {
		return s[:w]
	}
	return s + strings.Repeat(" ", w-lipgloss.Width(s))
}
