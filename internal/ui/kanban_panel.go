package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/HumanHorizon/automata/internal/atomicfile"
	"github.com/HumanHorizon/automata/internal/childproc"
	"github.com/HumanHorizon/automata/internal/kanban"
	"github.com/HumanHorizon/automata/internal/paths"
	apptheme "github.com/HumanHorizon/automata/internal/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/fsnotify/fsnotify"
	warp "github.com/starframe-dev/warp"
)

// kanbanChangedMsg is sent by the active watcher after a board change.
type kanbanChangedMsg struct {
	generation uint64
	watcher    *fsnotify.Watcher
}

type kanbanWatcherErrorMsg struct {
	generation uint64
	watcher    *fsnotify.Watcher
	err        error
}

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

var (
	assignKanbanTaskStatus      = kanban.AssignTaskAndStatus
	updateKanbanTaskStatus      = kanban.UpdateStatus
	writeAssignedChatTaskStatus = writeTaskToChatStatus
	writeRemovedChatTaskStatus  = writeTaskRemovedFromChat
)

// KanbanPanel renders a full kanban board using warp layout.
type KanbanPanel struct {
	profile      string
	domain       string
	palette      apptheme.Theme
	tasks        []kanban.Task
	readWarning  string
	watchWarning string
	tab          *warp.Tab

	btnPanel  *kanbanBtnPanel
	colPanels []*kanbanColPanel

	chats []ChatInfo // available AI chats for the picker

	// sessionNames maps sessionID → chat name for display in columns
	sessionNames map[string]string

	// pendingTask is set when user clicks "→ Pending" — shows chat picker
	pendingTask *kanban.Task

	// pickerHover is the index of the hovered chat in the picker
	pickerHover int

	// watcher observes the kanban directory (or its parent until the
	// directory is created) for file changes without polling.
	watcher     *fsnotify.Watcher
	watcherPath string
	watchErrors <-chan error

	// active gates watcher ownership while the Kanban tab is hidden.
	active bool

	// generation binds each blocking command to the watcher that created it.
	generation uint64

	// watchPending guarantees at most one blocking reader per watcher.
	watchPending bool

	// onTaskAssigned is called when a task is assigned to a chat. It may return
	// a Bubble Tea command and an error when the assignment must be rolled back.
	onTaskAssigned func(sessionID, taskTitle string) (tea.Cmd, error)
	assignmentErr  string
	actionWarning  string

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

// SetProfile updates the profile used to resolve Kanban data.
func (k *KanbanPanel) SetProfile(profile string) {
	if k.profile == profile {
		return
	}
	k.closeWatcher()
	k.profile = profile
	k.reload()
	if k.active {
		k.setupWatcher()
	}
}

// SetDomain reloads tasks for the given domain.
func (k *KanbanPanel) SetDomain(domain string) {
	if k.domain == domain {
		if k.active {
			k.setupWatcher()
		}
		return
	}
	k.closeWatcher()
	k.domain = domain
	k.pendingTask = nil
	k.activeCol = 0
	k.reload()
	if k.active {
		k.setupWatcher()
	}
}

// setupWatcher attaches an fsnotify.Watcher to the domain's kanban
// directory. If the directory does not exist yet, it watches the domain
// directory and switches to kanban/ as soon as that directory is created.
func (k *KanbanPanel) setupWatcher() {
	if !k.active || k.domain == "" {
		return
	}
	dir := kanban.KanbanDir(k.domain, k.profile)
	watchPath := dir
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		watchPath = filepath.Dir(dir)
		if err := paths.EnsurePrivateDir(watchPath); err != nil {
			k.watchWarning = fmt.Sprintf("create live-update path: %v", err)
			return
		}
	}
	if k.watcher != nil && k.watcherPath == watchPath {
		return
	}
	k.closeWatcher()
	w, err := fsnotify.NewWatcher()
	if err != nil {
		k.watchWarning = fmt.Sprintf("create live-update watcher: %v", err)
		return
	}
	if err := w.Add(watchPath); err != nil {
		_ = w.Close()
		k.watchWarning = fmt.Sprintf("watch Kanban: %v", err)
		return
	}
	k.watcher = w
	k.watcherPath = watchPath
	k.watchErrors = w.Errors
	k.watchWarning = ""
}

// closeWatcher stops and releases the file watcher if one is attached.
func (k *KanbanPanel) closeWatcher() {
	k.generation++
	k.watchPending = false
	if k.watcher != nil {
		_ = k.watcher.Close()
		k.watcher = nil
	}
	k.watcherPath = ""
	k.watchErrors = nil
}

// Activate reloads the board while preserving its scroll position and starts its watcher.
func (k *KanbanPanel) Activate() tea.Cmd {
	if !k.active {
		k.active = true
		k.generation++
		offsets := make([]int, len(k.colPanels))
		for index, panel := range k.colPanels {
			offsets[index] = panel.scrollOffset
		}
		k.reload()
		for index, panel := range k.colPanels {
			panel.scrollOffset = min(offsets[index], panel.maxScrollOffset())
		}
		k.setupWatcher()
	}
	return k.armWatcher()
}

// Deactivate releases the watcher without clearing Kanban UI state.
func (k *KanbanPanel) Deactivate() {
	if !k.active && k.watcher == nil {
		return
	}
	k.active = false
	k.closeWatcher()
}

// Close releases the Kanban filesystem watcher.
func (k *KanbanPanel) Close() {
	k.Deactivate()
}

// watchKanbanCmd blocks on fsnotify event/error channels and returns one
// message. Update re-arms it after every result without a busy heartbeat.
func (k *KanbanPanel) watchKanbanCmd() tea.Cmd {
	if k.watcher == nil {
		return nil
	}
	w := k.watcher
	generation := k.generation
	watchErrors := k.watchErrors
	if watchErrors == nil {
		watchErrors = w.Errors
	}
	return func() tea.Msg {
		// Block until the watcher reports a filesystem event or an error.
		select {
		case _, ok := <-w.Events:
			if !ok {
				return kanbanWatcherErrorMsg{generation: generation, watcher: w, err: fmt.Errorf("fsnotify events channel closed")}
			}
			return kanbanChangedMsg{generation: generation, watcher: w}
		case err, ok := <-watchErrors:
			if !ok {
				err = fmt.Errorf("fsnotify errors channel closed")
			} else if err == nil {
				err = fmt.Errorf("fsnotify returned an empty watcher error")
			}
			return kanbanWatcherErrorMsg{generation: generation, watcher: w, err: err}
		}
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
func (k *KanbanPanel) SetOnTaskAssigned(fn func(sessionID, taskTitle string) (tea.Cmd, error)) {
	k.onTaskAssigned = fn
}

func (k *KanbanPanel) reload() {
	k.tasks = nil
	k.readWarning = ""
	if k.domain == "" {
		return
	}
	tasks, err := kanban.ReadAll(k.domain, k.profile)
	k.tasks = tasks
	if err != nil {
		k.readWarning = strings.ReplaceAll(err.Error(), "\n", "; ")
		log.Printf("automata: read Kanban domain %q: %v", k.domain, err)
	}
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
		if k.isCurrentWatcher(msg.generation, msg.watcher) {
			// A missing kanban directory may have just been created, so switch
			// the parent watcher before re-arming the command.
			k.watchPending = false
			k.setupWatcher()
			k.reload()
		}
		baseCmd = nil
	case kanbanWatcherErrorMsg:
		if k.isCurrentWatcher(msg.generation, msg.watcher) {
			k.watchPending = false
			log.Printf("automata: kanban watcher failed: %v", msg.err)
			k.closeWatcher()
			k.setupWatcher()
			k.reload()
		}
		baseCmd = nil
	default:
		baseCmd = k.tab.Update(msg)
	}

	return tea.Batch(baseCmd, k.armWatcher())
}

func (k *KanbanPanel) armWatcher() tea.Cmd {
	if !k.active || k.watcher == nil || k.watchPending {
		return nil
	}
	k.watchPending = true
	return k.watchKanbanCmd()
}

func (k *KanbanPanel) isCurrentWatcher(generation uint64, watcher *fsnotify.Watcher) bool {
	return k.active && watcher != nil && watcher == k.watcher && generation == k.generation
}

func (k *KanbanPanel) handleMouse(msg tea.MouseMsg) tea.Cmd {
	// If picker is open, handle picker clicks
	if k.pendingTask != nil {
		switch msg.Button {
		case tea.MouseButtonLeft:
			if msg.Action == tea.MouseActionPress {
				// Check if click is on a chat item
				idx := k.pickerHitTest(msg.Y)
				if idx >= 0 && idx < len(k.chats) {
					// Assign immediately when the chat is free; otherwise queue the
					// task behind its existing in-progress work.
					chat := k.chats[idx]
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

					assignedTask := *k.pendingTask
					cmd, err := k.assignTaskToChat(assignedTask, chat, status)
					if err != nil {
						k.assignmentErr = err.Error()
						return nil
					}
					k.assignmentErr = ""
					k.pendingTask = nil
					k.reload()
					return cmd
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
			if err := k.createTask(); err != nil {
				k.assignmentErr = err.Error()
			} else {
				k.assignmentErr = ""
			}
		}
		k.btnPanel.hover = msg.Button == tea.MouseButtonNone
		return nil
	}

	// Blank lines (y=0, y=2) — hovers already reset above, nothing to do.
	if msg.Y == 0 || msg.Y == 2 {
		return nil
	}

	return k.routeColumnMouse(msg)
}

func (k *KanbanPanel) routeColumnMouse(msg tea.MouseMsg) tea.Cmd {
	columnWidth := k.colWidth()
	columnIndex := msg.X / columnWidth
	if columnIndex < 0 || columnIndex >= len(k.colPanels) {
		return nil
	}
	if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
		k.activeCol = columnIndex
	}
	column := k.colPanels[columnIndex]
	if msg.Button == tea.MouseButtonWheelUp {
		if column.scrollOffset > 0 {
			column.scrollOffset--
		}
		return nil
	}
	if msg.Button == tea.MouseButtonWheelDown {
		if column.scrollOffset < column.maxScrollOffset() {
			column.scrollOffset++
		}
		return nil
	}

	localMessage := tea.MouseMsg{
		X:      msg.X - columnIndex*columnWidth,
		Y:      msg.Y - 3,
		Action: msg.Action,
		Button: msg.Button,
	}
	for index, other := range k.colPanels {
		if index != columnIndex {
			other.hoverRow = -1
			other.hoverBtn = ""
		}
	}
	return column.Update(localMessage)
}

// colWidth returns the per-column width based on the current panel width.
func (k *KanbanPanel) colWidth() int {
	return kanbanColumnWidth(k.width)
}

func kanbanColumnWidth(panelWidth int) int {
	if panelWidth <= 0 {
		return 20
	}
	return max(4, panelWidth/len(kanbanColumns))
}

// pickerOffset returns the Y offset where the picker starts.
func (k *KanbanPanel) pickerOffset() int {
	return 3 // header + some padding
}

// pickerHitTest returns the chat index at the given Y position, or -1.
func (k *KanbanPanel) pickerHitTest(y int) int {
	return pickerChatIndex(y, k.pickerOffset(), len(k.chats))
}

func pickerChatIndex(y, offset, chatCount int) int {
	index := y - offset
	if index < 0 || index >= chatCount {
		return -1
	}
	return index
}

// View implements warp.Panel. Layout:
//
//	y=0   : blank top padding
//	y=1   : button row
//	y=2   : blank board padding
//	y=3+  : column area
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
	warnings := make([]string, 0, 4)
	if k.actionWarning != "" {
		warnings = append(warnings, "⚠ "+k.actionWarning)
	}
	if k.readWarning != "" {
		warnings = append(warnings, "⚠ Не все Kanban-задачи загружены: "+k.readWarning)
	}
	if k.watchWarning != "" {
		warnings = append(warnings, "⚠ Kanban live update unavailable: "+k.watchWarning)
	}
	if k.assignmentErr != "" {
		warnings = append(warnings, "Ошибка Kanban-действия: "+k.assignmentErr)
	}
	if len(warnings) > 0 {
		warning := ansi.Truncate(strings.Join(warnings, "; "), width, "…")
		boardTopPad = lipgloss.NewStyle().Foreground(lipgloss.Color(k.palette.Error)).Render(warning)
	}

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
	if k.assignmentErr != "" {
		errLine := " Ошибка: " + k.assignmentErr
		if lipgloss.Width(errLine) > width {
			errLine = ansi.Truncate(errLine, width, "")
		}
		b.WriteString(errLine)
		b.WriteString("\n")
	}

	// Chat list
	for i, chat := range k.chats {
		style := styles.item
		if i == k.pickerHover {
			style = styles.cardHover
		}
		line := fmt.Sprintf("  💬 %s", chat.Name)
		if lipgloss.Width(line) > width {
			line = ansi.Truncate(line, width, "")
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

func (k *KanbanPanel) createTask() error {
	return k.createTaskAt(time.Now())
}

func (k *KanbanPanel) createTaskAt(createdAt time.Time) error {
	if k.domain == "" {
		return errors.New("kanban domain is not selected")
	}
	dir := kanban.KanbanDir(k.domain, k.profile)
	if err := paths.EnsurePrivateDir(dir); err != nil {
		return fmt.Errorf("create Kanban directory: %w", err)
	}
	baseName := "task-" + createdAt.Format("2006-01-02T15-04-05")
	content := "---\ntitle: Новая задача\nstatus: todo\n---\n"
	for suffix := uint64(0); ; suffix++ {
		name := baseName + ".md"
		if suffix > 0 {
			name = fmt.Sprintf("%s-%d.md", baseName, suffix+1)
		}
		path := filepath.Join(dir, name)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, paths.PrivateFileMode)
		if err != nil {
			if os.IsExist(err) {
				continue
			}
			return fmt.Errorf("create Kanban task %s: %w", path, err)
		}

		_, writeErr := file.WriteString(content)
		if writeErr == nil {
			writeErr = file.Sync()
		}
		closeErr := file.Close()
		if writeErr == nil {
			writeErr = closeErr
		} else if closeErr != nil {
			writeErr = errors.Join(writeErr, closeErr)
		}
		if writeErr != nil {
			removeErr := os.Remove(path)
			if removeErr != nil && !os.IsNotExist(removeErr) {
				writeErr = errors.Join(writeErr, fmt.Errorf("remove incomplete task: %w", removeErr))
			}
			return fmt.Errorf("write Kanban task %s: %w", path, writeErr)
		}
		k.reload()
		return nil
	}
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
				if err := b.parent.createTask(); err != nil {
					b.parent.assignmentErr = err.Error()
				} else {
					b.parent.assignmentErr = ""
				}
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
// lines including top/bottom borders.
func (c *kanbanColPanel) renderCard(task kanban.Task, isHover bool) []string {
	return newKanbanCardRenderer(c, task, isHover).render()
}

type kanbanCardRenderer struct {
	column     *kanbanColPanel
	task       kanban.Task
	isHover    bool
	innerWidth int
	styles     kanbanStyles
	border     lipgloss.Style
	background lipgloss.Style
}

func newKanbanCardRenderer(column *kanbanColPanel, task kanban.Task, isHover bool) kanbanCardRenderer {
	palette := apptheme.Default()
	styles := newKanbanStyles(palette)
	if column.parent != nil {
		palette = column.parent.palette
		styles = column.parent.styles()
	}
	width := max(4, column.width)
	border := styles.border
	background := styles.background
	if isHover {
		border = styles.border.Foreground(lipgloss.Color(palette.Border))
		background = styles.backgroundHover
	}
	return kanbanCardRenderer{
		column:     column,
		task:       task,
		isHover:    isHover,
		innerWidth: width - 2,
		styles:     styles,
		border:     border,
		background: background,
	}
}

func (r kanbanCardRenderer) render() []string {
	lines := []string{r.horizontalBorder("┌", "┐"), r.renderTitle()}
	if r.task.AssignedTo != "" {
		lines = append(lines, r.renderAssigned())
	}
	if r.task.Substatus != "" {
		lines = append(lines, r.renderSubstatus())
	}
	for _, item := range statusTransitions[r.task.Status] {
		lines = append(lines, r.renderTransition(item))
	}
	lines = append(lines, r.renderPath(), r.horizontalBorder("└", "┘"))
	return lines
}

func (r kanbanCardRenderer) horizontalBorder(left, right string) string {
	line := left + strings.Repeat("─", r.innerWidth) + right
	if r.isHover {
		return r.border.Render(line)
	}
	return line
}

func (r kanbanCardRenderer) wrapLine(content string) string {
	if lipgloss.Width(content) > r.innerWidth {
		content = ansi.Truncate(content, r.innerWidth, "…")
	}
	contentWidth := lipgloss.Width(content)
	pad := strings.Repeat(" ", max(0, r.innerWidth-contentWidth))
	styled := r.background.Render(content + pad)
	left := r.border.Render("│")
	right := r.border.Render("│")
	return left + styled + right
}

func (r kanbanCardRenderer) renderTitle() string {
	titleMax := max(1, r.innerWidth-2)
	title := r.task.Title
	if lipgloss.Width(title) > titleMax {
		title = ansi.Truncate(title, titleMax, "")
	}
	titlePad := strings.Repeat(" ", titleMax-lipgloss.Width(title))
	deleteControl := " ×"
	if r.isHover && r.column.hoverBtn == "delete" {
		deleteControl = r.styles.delete.Render(" ×")
	}
	return r.wrapLine(title + titlePad + deleteControl)
}

func (r kanbanCardRenderer) renderAssigned() string {
	name := r.task.AssignedTo
	if r.column.parent != nil {
		if displayName, ok := r.column.parent.sessionNames[r.task.AssignedTo]; ok {
			name = displayName
		}
	}
	assigned := fmt.Sprintf("  👤 %s", name)
	assignedWidth := max(0, r.innerWidth-r.styles.assigned.GetHorizontalPadding())
	if lipgloss.Width(assigned) > assignedWidth {
		assigned = ansi.Truncate(assigned, assignedWidth, "")
	}
	styled := r.styles.assigned.Render(assigned)
	if width := lipgloss.Width(styled); width < r.innerWidth {
		styled += strings.Repeat(" ", r.innerWidth-width)
	}
	return r.border.Render("│") + r.background.Render(styled) + r.border.Render("│")
}

func (r kanbanCardRenderer) renderSubstatus() string {
	substatus := fmt.Sprintf("  ↳ %s", r.task.Substatus)
	substatusWidth := max(0, r.innerWidth-r.styles.substatus.GetHorizontalPadding())
	if lipgloss.Width(substatus) > substatusWidth {
		substatus = ansi.Truncate(substatus, substatusWidth, "")
	}
	styled := r.styles.substatus.Render(substatus)
	pad := strings.Repeat(" ", max(0, r.innerWidth-lipgloss.Width(styled)))
	return r.border.Render("│") + styled + r.background.Render(pad) + r.border.Render("│")
}

func (r kanbanCardRenderer) renderTransition(item transition) string {
	buttonStyle := r.styles.transit
	if r.isHover && r.column.hoverBtn == item.next {
		buttonStyle = r.styles.transitHover
	}
	button := buttonStyle.Render(" " + item.label + " ")
	pad := strings.Repeat(" ", max(0, r.innerWidth-lipgloss.Width(button)))
	return r.border.Render("│") + button + r.background.Render(pad) + r.border.Render("│")
}

func (r kanbanCardRenderer) renderPath() string {
	label := " Файл задачи"
	href := "file://" + r.task.Path
	hyperlinked := fmt.Sprintf("\x1b]8;;%s\x1b\\%s\x1b]8;;\x1b\\", href, label)
	link := r.styles.cardPath.Render(hyperlinked)
	if lipgloss.Width(link) > r.innerWidth {
		link = ansi.Truncate(link, r.innerWidth, "")
	}
	pad := strings.Repeat(" ", max(0, r.innerWidth-lipgloss.Width(link)))
	return r.border.Render("│") + link + r.background.Render(pad) + r.border.Render("│")
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
			row, button := c.hitTest(msg.Y, msg.X)
			if row >= 0 && row < len(c.tasks) {
				c.handleTaskClick(row, button)
			}
		}
	case tea.MouseButtonNone:
		c.hoverRow, c.hoverBtn = c.hitTest(msg.Y, msg.X)
	}
	return nil
}

func (c *kanbanColPanel) handleTaskClick(row int, button string) {
	task := c.tasks[row]
	switch button {
	case "delete":
		c.deleteTask(task)
	case "reassign", "pending":
		c.openTaskPicker(task)
	case "todo", "progress", "done":
		c.transitionTaskStatus(task, button)
	default:
		c.openTaskFile(task)
	}
}

func (c *kanbanColPanel) deleteTask(task kanban.Task) {
	var err error
	if c.parent == nil {
		err = os.Remove(task.Path)
	} else {
		err = c.parent.deleteTask(task)
	}
	if err != nil {
		if c.parent != nil {
			c.parent.assignmentErr = err.Error()
		}
		return
	}
	if c.parent != nil {
		c.parent.assignmentErr = ""
		c.parent.reload()
	}
}

func (c *kanbanColPanel) openTaskPicker(task kanban.Task) {
	if c.parent == nil {
		return
	}
	c.parent.pendingTask = &task
	c.parent.pickerHover = -1
}

func (c *kanbanColPanel) transitionTaskStatus(task kanban.Task, nextStatus string) {
	updatedTask, taskSnapshot, err := readTaskFileSnapshot(task.Path)
	if err != nil {
		if c.parent != nil {
			c.parent.assignmentErr = fmt.Sprintf("read task: %v", err)
		}
		return
	}

	var statusPath string
	var statusBefore statusSnapshot
	statusChanged := false
	committedWarnings := make([]string, 0, 2)
	if c.parent != nil && nextStatus == "todo" && updatedTask.AssignedTo != "" {
		statusPath = filepath.Join(paths.SessionDir(c.parent.profile, updatedTask.AssignedTo), "status.json")
		statusBefore, err = readStatusSnapshot(statusPath)
		if err == nil {
			err = writeRemovedChatTaskStatus(c.parent.profile, updatedTask.AssignedTo, updatedTask.Title)
		}
		if err != nil && !atomicfile.IsCommitted(err) {
			c.parent.assignmentErr = fmt.Sprintf("notify task removal: %v", err)
			return
		}
		if err != nil {
			committedWarnings = append(committedWarnings, err.Error())
		}
		statusChanged = true
	}

	if nextStatus == "todo" {
		_, _, err = assignKanbanTaskStatus(updatedTask.Path, "", nextStatus)
	} else {
		_, err = updateKanbanTaskStatus(updatedTask.Path, nextStatus)
	}
	if err != nil && !atomicfile.IsCommitted(err) {
		rollbackErrors := []error{fmt.Errorf("update task: %w", err)}
		if restoreErr := taskSnapshot.restore(); restoreErr != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("restore Kanban task: %w", restoreErr))
		}
		if statusChanged {
			if restoreErr := restoreStatusSnapshot(statusPath, statusBefore); restoreErr != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("restore chat status: %w", restoreErr))
			}
		}
		if c.parent != nil {
			c.parent.assignmentErr = errors.Join(rollbackErrors...).Error()
		}
		return
	}
	if err != nil {
		committedWarnings = append(committedWarnings, err.Error())
	}
	if c.parent != nil {
		c.parent.assignmentErr = ""
		c.parent.actionWarning = strings.Join(committedWarnings, "; ")
		c.parent.reload()
	}
}

func (c *kanbanColPanel) openTaskFile(task kanban.Task) {
	if err := childproc.StartAndReap(exec.Command("zed", task.Path)); err != nil {
		if c.parent != nil {
			c.parent.assignmentErr = fmt.Sprintf("open task file: %v", err)
		}
	} else if c.parent != nil {
		c.parent.assignmentErr = ""
	}
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
	return kanbanMaxScrollOffset(c.tasks, c.height)
}

func kanbanMaxScrollOffset(tasks []kanban.Task, height int) int {
	total := 1 // header
	for _, task := range tasks {
		total += cardTotalLines(task) + 1 // +1 for gap after card
	}
	if total <= height {
		return 0
	}
	return total - height
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
	return hitTestKanbanColumn(c.tasks, c.width, c.scrollOffset, y, x)
}

func hitTestKanbanColumn(tasks []kanban.Task, width, scrollOffset, y, x int) (int, string) {
	if y < 1 {
		return -1, ""
	}
	// Convert visible Y to actual Y by adding scrollOffset.
	// Header (y=0) is always visible and not affected by scroll.
	actualY := y + scrollOffset

	line := 1 // first content line — corresponds to the top border of card 0
	for i, task := range tasks {
		// Top border
		if actualY == line {
			return i, "" // borders are visual only; clicks fall through to body
		}
		line++

		// Title line — detect × click on the right edge
		if actualY == line {
			if x >= width-3 && x < width-1 {
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
		if i < len(tasks)-1 {
			if actualY == line {
				return -1, ""
			}
			line++
		}
	}
	return -1, ""
}

// --- Helpers ---

type statusSnapshot struct {
	data   []byte
	exists bool
}

type taskFileSnapshot struct {
	path string
	data []byte
	mode os.FileMode
}

func readTaskFileSnapshot(path string) (kanban.Task, taskFileSnapshot, error) {
	info, err := os.Stat(path)
	if err != nil {
		return kanban.Task{}, taskFileSnapshot{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return kanban.Task{}, taskFileSnapshot{}, err
	}
	task, err := kanban.ReadTask(path)
	if err != nil {
		return kanban.Task{}, taskFileSnapshot{}, err
	}
	return task, taskFileSnapshot{path: path, data: data, mode: info.Mode().Perm()}, nil
}

func (snapshot taskFileSnapshot) restore() error {
	return writeAtomicFile(snapshot.path, snapshot.data, snapshot.mode)
}

func (k *KanbanPanel) deleteTask(task kanban.Task) error {
	if err := k.deleteTaskWith(task, os.Remove); err != nil {
		return err
	}
	return nil
}

func (k *KanbanPanel) deleteTaskWith(task kanban.Task, removeFile func(string) error) error {
	current, taskSnapshot, err := readTaskFileSnapshot(task.Path)
	if err != nil {
		return fmt.Errorf("read task before deletion: %w", err)
	}

	var statusPath string
	var statusBefore statusSnapshot
	statusChanged := false
	statusWarning := ""
	if current.AssignedTo != "" {
		statusPath = filepath.Join(paths.SessionDir(k.profile, current.AssignedTo), "status.json")
		statusBefore, err = readStatusSnapshot(statusPath)
		if err != nil {
			return fmt.Errorf("read assigned chat status before deletion: %w", err)
		}
		if err := writeRemovedChatTaskStatus(k.profile, current.AssignedTo, current.Title); err != nil {
			if !atomicfile.IsCommitted(err) {
				return fmt.Errorf("notify assigned chat before deletion: %w", err)
			}
			statusWarning = err.Error()
		}
		statusChanged = true
	}

	if err := removeFile(task.Path); err != nil {
		rollbackErrors := []error{fmt.Errorf("delete Kanban task: %w", err)}
		if restoreErr := taskSnapshot.restore(); restoreErr != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("restore Kanban task snapshot: %w", restoreErr))
		}
		if statusChanged {
			if restoreErr := restoreStatusSnapshot(statusPath, statusBefore); restoreErr != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("restore assigned chat status snapshot: %w", restoreErr))
			}
		}
		return errors.Join(rollbackErrors...)
	}
	if statusWarning != "" {
		k.actionWarning = statusWarning
	}
	return nil
}

func readStatusSnapshot(path string) (statusSnapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return statusSnapshot{}, nil
		}
		return statusSnapshot{}, err
	}
	var status map[string]interface{}
	if err := json.Unmarshal(data, &status); err != nil {
		return statusSnapshot{}, err
	}
	return statusSnapshot{data: data, exists: true}, nil
}

func restoreStatusSnapshot(path string, snapshot statusSnapshot) error {
	if !snapshot.exists {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return writeAtomicFile(path, snapshot.data, paths.PrivateFileMode)
}

func writeAtomicFile(path string, data []byte, mode os.FileMode) error {
	if err := paths.EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return err
	}
	return atomicfile.Write(path, data, mode)
}

func (k *KanbanPanel) assignTaskToChat(task kanban.Task, chat ChatInfo, status string) (tea.Cmd, error) {
	previous, taskSnapshot, err := readTaskFileSnapshot(task.Path)
	if err != nil {
		return nil, fmt.Errorf("read Kanban task before assignment: %w", err)
	}

	type sessionStatusSnapshot struct {
		sessionID string
		path      string
		snapshot  statusSnapshot
	}
	statusSnapshots := make([]sessionStatusSnapshot, 0, 2)
	sessionIDs := make([]string, 0, 2)
	if previous.AssignedTo != "" && previous.AssignedTo != chat.SessionID {
		sessionIDs = append(sessionIDs, previous.AssignedTo)
	}
	sessionIDs = append(sessionIDs, chat.SessionID)
	for _, sessionID := range sessionIDs {
		statusPath := filepath.Join(paths.SessionDir(k.profile, sessionID), "status.json")
		snapshot, err := readStatusSnapshot(statusPath)
		if err != nil {
			return nil, fmt.Errorf("read chat status for %s: %w", sessionID, err)
		}
		statusSnapshots = append(statusSnapshots, sessionStatusSnapshot{
			sessionID: sessionID,
			path:      statusPath,
			snapshot:  snapshot,
		})
	}

	rollback := func(cause error) error {
		rollbackErrors := []error{cause}
		if err := taskSnapshot.restore(); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("restore Kanban task snapshot: %w", err))
		}
		for index := len(statusSnapshots) - 1; index >= 0; index-- {
			entry := statusSnapshots[index]
			if err := restoreStatusSnapshot(entry.path, entry.snapshot); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("restore chat status for %s: %w", entry.sessionID, err))
			}
		}
		return errors.Join(rollbackErrors...)
	}

	committedWarnings := make([]string, 0, 2)
	updatedPrevious, _, err := assignKanbanTaskStatus(task.Path, chat.SessionID, status)
	if err != nil {
		if !atomicfile.IsCommitted(err) {
			return nil, fmt.Errorf("assign Kanban task %s: %w", task.Path, err)
		}
		committedWarnings = append(committedWarnings, err.Error())
	} else {
		previous = updatedPrevious
	}
	if previous.AssignedTo != "" && previous.AssignedTo != chat.SessionID {
		if err := writeRemovedChatTaskStatus(k.profile, previous.AssignedTo, previous.Title); err != nil {
			return nil, rollback(fmt.Errorf("notify previous chat about reassignment: %w", err))
		}
	}
	if err := writeAssignedChatTaskStatus(k.profile, chat.SessionID, previous.Title, task.Path); err != nil {
		return nil, rollback(fmt.Errorf("write assigned chat status: %w", err))
	}
	if k.onTaskAssigned == nil {
		k.actionWarning = strings.Join(committedWarnings, "; ")
		return nil, nil
	}
	cmd, startErr := k.onTaskAssigned(chat.SessionID, previous.Title)
	if startErr == nil {
		k.actionWarning = strings.Join(committedWarnings, "; ")
		return cmd, nil
	}
	var committed *CommittedActionError
	if errors.As(startErr, &committed) {
		committedWarnings = append(committedWarnings, startErr.Error())
		k.actionWarning = strings.Join(committedWarnings, "; ")
		return cmd, nil
	}
	return nil, fmt.Errorf("start assigned chat: %w", rollback(startErr))
}

// writeTaskToChatStatus writes a task assignment to the chat's status.json
// so the just-pi extension can pick it up on agent_end.
func writeTaskToChatStatus(profile, sessionID, taskTitle, taskPath string) error {
	statusPath := filepath.Join(paths.SessionDir(profile, sessionID), "status.json")

	var status map[string]interface{}
	if data, err := os.ReadFile(statusPath); err == nil {
		if err := json.Unmarshal(data, &status); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if status == nil {
		status = make(map[string]interface{})
	}
	status["action"] = "task_assigned"
	status["task_title"] = taskTitle
	status["task_path"] = taskPath
	status["substatus"] = ""
	status["updatedAt"] = time.Now().Format(time.RFC3339)

	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomicFile(statusPath, data, paths.PrivateFileMode)
}

// writeTaskRemovedFromChat notifies the chat that a task was removed from progress.
func writeTaskRemovedFromChat(profile, sessionID, taskTitle string) error {
	statusPath := filepath.Join(paths.SessionDir(profile, sessionID), "status.json")

	var status map[string]interface{}
	if data, err := os.ReadFile(statusPath); err == nil {
		if err := json.Unmarshal(data, &status); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if status == nil {
		status = make(map[string]interface{})
	}
	status["action"] = "task_removed"
	status["task_title"] = taskTitle
	delete(status, "task_path")
	status["substatus"] = ""
	status["updatedAt"] = time.Now().Format(time.RFC3339)

	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomicFile(statusPath, data, paths.PrivateFileMode)
}

type voidPanel struct{}

func (voidPanel) View(width, height int) string { return "" }
func (voidPanel) Update(msg tea.Msg) tea.Cmd    { return nil }

func padOrTruncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	visibleWidth := lipgloss.Width(s)
	if visibleWidth > w {
		return ansi.Truncate(s, w, "")
	}
	return s + strings.Repeat(" ", w-visibleWidth)
}
