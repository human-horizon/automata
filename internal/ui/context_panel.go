package ui

import (
	"fmt"
	"os"
	"strings"

	"github.com/HumanHorizon/automata/internal/ai-knowledge/memory"
	"github.com/HumanHorizon/automata/internal/paths"
	apptheme "github.com/HumanHorizon/automata/internal/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/fsnotify/fsnotify"
	warp "github.com/starframe-dev/warp"
)

type noteHit struct {
	key string
	row int
}

// ContextPanel renders folder context with Content and Kanban tabs. It is a
// native Warp panel so hover and mouse selection work consistently.
type ContextPanel struct {
	profile      string
	domain       string
	palette      apptheme.Theme
	width        int
	height       int
	activeTab    int // 0 = Content, 1 = Kanban
	scrollOffset int
	data         *memory.Data
	notesReader  *memory.CachedReader
	kanbanPanel  *KanbanPanel

	expandedNotes map[string]bool
	activeNoteKey string
	noteHits      []noteHit

	// notesWatcher observes the active domain directory so changes to
	// notes.json reach the panel without polling. Created in SetDomain,
	// closed when the panel switches domain or shuts down. Re-armed
	// through notesWatchPending like KnowledgePanel.jobsWatcher.
	notesWatcher      *fsnotify.Watcher
	notesWatchPending bool
}

// NewContextPanel creates an empty context panel for the given profile.
func NewContextPanel(profile string) *ContextPanel {
	return &ContextPanel{
		profile:       profile,
		palette:       apptheme.Default(),
		notesReader:   memory.NewCachedReader(),
		kanbanPanel:   NewKanbanPanel(profile),
		expandedNotes: make(map[string]bool),
	}
}

// SetTheme updates the palette used by the context and Kanban panels.
func (c *ContextPanel) SetTheme(palette apptheme.Theme) {
	c.palette = palette
	c.kanbanPanel.SetTheme(palette)
}

// SetProfile updates the profile used to resolve domain data paths.
func (c *ContextPanel) SetProfile(profile string) {
	c.profile = profile
}

// SetChats forwards the chat list to the kanban panel for the picker.
func (c *ContextPanel) SetChats(chats []ChatInfo) {
	c.kanbanPanel.SetChats(chats)
}

// SetOnTaskAssigned sets a callback for when a task is assigned to a chat.
func (c *ContextPanel) SetOnTaskAssigned(fn func(sessionID, taskTitle string)) {
	c.kanbanPanel.SetOnTaskAssigned(fn)
}

// SetDomain switches the panel to the given domain and refreshes data.
func (c *ContextPanel) SetDomain(domain string) {
	if c.domain == domain {
		return
	}
	// Close the watcher on the previous domain before swapping so we never
	// leak an fsnotify descriptor when the user jumps between folders.
	c.closeNotesWatcher()
	c.domain = domain
	c.scrollOffset = 0
	c.expandedNotes = make(map[string]bool)
	c.activeNoteKey = ""
	c.noteHits = nil
	c.refresh()
	c.setupNotesWatcher()
	c.kanbanPanel.SetDomain(domain)
}

// Refresh re-reads domain notes and kanban tasks from disk. Safe to call from a tick.
func (c *ContextPanel) Refresh() {
	c.refresh()
}

func (c *ContextPanel) refresh() {
	if c.domain == "" {
		c.data = nil
		return
	}
	if d, err := c.notesReader.Read(c.profile, c.domain); err == nil {
		c.data = d
	} else {
		c.data = nil
	}
}

// setupNotesWatcher attaches an fsnotify.Watcher to the active domain
// directory so CREATE/WRITE/REMOVE/RENAME on notes.json (or any future
// file we add to the domain dir) are reflected in the panel without a
// tick. Best-effort: a missing domain dir is created on the fly; any
// other error is logged and ignored — the panel just keeps its previous
// data until the next manual refresh.
func (c *ContextPanel) setupNotesWatcher() {
	if c.domain == "" {
		return
	}
	dir := c.domainDirForActive()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "context_panel: cannot create %s: %v\n", dir, err)
		return
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		fmt.Fprintf(os.Stderr, "context_panel: cannot create notes watcher: %v\n", err)
		return
	}
	if err := w.Add(dir); err != nil {
		fmt.Fprintf(os.Stderr, "context_panel: cannot watch %s: %v\n", dir, err)
		w.Close()
		return
	}
	c.notesWatcher = w
}

// closeNotesWatcher releases the domain notes watcher if one is attached.
func (c *ContextPanel) closeNotesWatcher() {
	if c.notesWatcher != nil {
		c.notesWatcher.Close()
		c.notesWatcher = nil
	}
	c.notesWatchPending = false
}

// watchNotesCmd blocks on the domain notes watcher and returns a single
// notesChangedMsg when any event lands. Update re-arms it after every
// event so the watcher stays alive without a busy heartbeat.
func (c *ContextPanel) watchNotesCmd() tea.Cmd {
	if c.notesWatcher == nil {
		return nil
	}
	w := c.notesWatcher
	return func() tea.Msg {
		_, ok := <-w.Events
		if !ok {
			return nil // watcher closed
		}
		return notesChangedMsg{}
	}
}

// domainDirForActive returns the on-disk path of the active domain
// directory under the resolved profile. Explicit profiles win; an empty
// profile falls back to AI_PROFILE, then paths.DomainDir applies the default.
func (c *ContextPanel) domainDirForActive() string {
	profile := c.profile
	if profile == "" {
		profile = os.Getenv("AI_PROFILE")
	}
	return paths.DomainDir(profile, c.domain)
}

// notesChangedMsg is sent by watchNotesCmd when fsnotify reports a
// change to the active domain directory (typically notes.json).
type notesChangedMsg struct{}

// Update handles resize, tab switching, and scrolling.
func (c *ContextPanel) Update(msg tea.Msg) tea.Cmd {
	var baseCmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		c.width = msg.Width
		c.height = msg.Height
		c.kanbanPanel.Update(warp.ResizeMsg{Width: msg.Width, Height: msg.Height - 1})
	case warp.ResizeMsg:
		c.width = msg.Width
		c.height = msg.Height
		c.kanbanPanel.Update(warp.ResizeMsg{Width: msg.Width, Height: msg.Height - 1})
	case tea.MouseMsg:
		baseCmd = c.handleMouse(msg)
	case tea.KeyMsg:
		c.handleKey(msg)
	case notesChangedMsg:
		c.notesWatchPending = false
		c.refresh()
	}

	if c.notesWatcher != nil && !c.notesWatchPending {
		c.notesWatchPending = true
		baseCmd = tea.Batch(baseCmd, c.watchNotesCmd())
	}
	return baseCmd
}

func (c *ContextPanel) handleMouse(msg tea.MouseMsg) tea.Cmd {
	// Tab bar is at the top, one row high.
	if msg.Y == 0 && msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		contentLabel := " Content "
		kanbanLabel := " Kanban "
		contentW := lipgloss.Width(contentLabel)
		kanbanW := lipgloss.Width(kanbanLabel)
		if msg.X < contentW {
			c.activeTab = 0
		} else if msg.X >= contentW+1 && msg.X < contentW+1+kanbanW {
			c.activeTab = 1
		}
		c.scrollOffset = 0
		return nil
	}
	// Forward mouse to kanban panel when on Kanban tab.
	if c.activeTab == 1 && msg.Y > 0 {
		relMsg := tea.MouseMsg{
			X:      msg.X,
			Y:      msg.Y - 1,
			Action: msg.Action,
			Button: msg.Button,
		}
		return c.kanbanPanel.Update(relMsg)
	}
	if c.activeTab == 0 && msg.Y > 0 && msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		if hit, ok := c.noteAt(msg.Y - 1 + c.scrollOffset); ok {
			c.activeNoteKey = hit.key
			c.toggleNote(hit.key)
			return nil
		}
	}
	// Scroll wheel on the content area.
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		c.scrollOffset -= 3
		if c.scrollOffset < 0 {
			c.scrollOffset = 0
		}
	case tea.MouseButtonWheelDown:
		c.scrollOffset += 3
	}
	return nil
}

func (c *ContextPanel) handleKey(msg tea.KeyMsg) {
	key := msg.String()
	if c.activeTab == 0 && c.hasNotes() {
		switch key {
		case "up":
			c.moveActiveNote(-1)
			c.ensureActiveNoteVisible()
			return
		case "down":
			c.moveActiveNote(1)
			c.ensureActiveNoteVisible()
			return
		case "enter", " ", "space":
			c.toggleActiveNote()
			return
		case "left":
			c.collapseActiveNote()
			return
		case "right":
			c.expandActiveNote()
			return
		}
	}

	switch key {
	case "up":
		c.scrollOffset--
		if c.scrollOffset < 0 {
			c.scrollOffset = 0
		}
	case "down":
		c.scrollOffset++
	case "pgup":
		c.scrollOffset -= 10
		if c.scrollOffset < 0 {
			c.scrollOffset = 0
		}
	case "pgdown":
		c.scrollOffset += 10
	case "left":
		c.activeTab = 0
		c.scrollOffset = 0
	case "right":
		c.activeTab = 1
		c.scrollOffset = 0
	}
}

// View renders the context panel.
func (c *ContextPanel) View(width, height int) string {
	if width > 0 {
		c.width = width
	}
	if height > 0 {
		c.height = height
	}
	if c.width <= 0 {
		c.width = 80
	}
	if c.height <= 0 {
		c.height = 24
	}
	if c.height < 2 {
		return strings.Repeat(" ", c.width)
	}

	tabBar := c.renderTabBar(c.width)
	bodyHeight := c.height - 1
	if bodyHeight < 1 {
		return tabBar
	}
	body := c.renderBody(c.width, bodyHeight)
	lines := strings.Split(body, "\n")
	if len(lines) > bodyHeight {
		maxOffset := len(lines) - bodyHeight
		if c.scrollOffset > maxOffset {
			c.scrollOffset = maxOffset
		}
		if c.scrollOffset < 0 {
			c.scrollOffset = 0
		}
		lines = lines[c.scrollOffset:]
		if len(lines) > bodyHeight {
			lines = lines[:bodyHeight]
		}
	} else {
		c.scrollOffset = 0
	}
	for len(lines) < bodyHeight {
		lines = append(lines, strings.Repeat(" ", c.width))
	}
	return tabBar + "\n" + strings.Join(lines, "\n")
}

func (c *ContextPanel) renderTabBar(width int) string {
	styles := c.styles()
	contentStyle := styles.tabInactive
	kanbanStyle := styles.tabInactive
	if c.activeTab == 0 {
		contentStyle = styles.tabActive
	} else {
		kanbanStyle = styles.tabActive
	}
	content := contentStyle.Render(" Content ")
	kanban := kanbanStyle.Render(" Kanban ")
	bar := content + " " + kanban
	barW := lipgloss.Width(bar)
	if barW < width {
		bar += strings.Repeat(" ", width-barW)
	}
	return bar
}

func (c *ContextPanel) renderBody(width, height int) string {
	if c.activeTab == 0 {
		body, hits := c.renderContentLayout(width)
		c.setNoteHits(hits)
		return body
	}
	c.noteHits = nil
	return c.kanbanPanel.View(width, height)
}

func (c *ContextPanel) renderContent(width, height int) string {
	body, hits := c.renderContentLayout(width)
	c.setNoteHits(hits)
	return body
}

func (c *ContextPanel) renderContentLayout(width int) (string, []noteHit) {
	styles := c.styles()
	if c.domain == "" {
		return styles.empty.Render(" No domain context "), nil
	}
	if c.data == nil || len(c.data.Notes) == 0 {
		return styles.empty.Render(" No notes for this domain "), nil
	}

	var b strings.Builder
	var hits []noteHit
	row := 0
	for noteIndex, note := range c.data.Notes {
		key := noteKey(noteIndex)
		title := note.Title
		if title == "" {
			title = "Notes"
		}
		marker := "▸"
		if c.expandedNotes[key] {
			marker = "▾"
		}
		header := fmt.Sprintf(" %s %s", marker, title)
		if key == c.activeNoteKey {
			header = styles.sectionActive.Render(header)
		} else {
			header = styles.sectionHeader.Render(header)
		}
		hits = append(hits, noteHit{key: key, row: row})
		appendPanelLine(&b, header, &row)

		if c.expandedNotes[key] {
			content := renderNoteContent(width, note, c.palette)
			if content != "" {
				appendPanelText(&b, content, &row)
			}
		}
	}
	return b.String(), hits
}

func renderNoteContent(width int, note memory.NoteSummary, palette apptheme.Theme) string {
	var blocks []string
	for _, section := range displaySections(note) {
		content := section.Content
		if section.Title != "" {
			heading := "### " + section.Title
			if strings.TrimSpace(content) == "" {
				blocks = append(blocks, heading)
				continue
			}
			blocks = append(blocks, heading+"\n\n"+content)
			continue
		}
		if strings.TrimSpace(content) != "" {
			blocks = append(blocks, content)
		}
	}
	return renderMarkdownBlockWithTheme(width, strings.Join(blocks, "\n\n"), " │", palette)
}

func displaySections(note memory.NoteSummary) []memory.NoteSection {
	if len(note.Sections) > 0 {
		return note.Sections
	}
	if len(note.Notes) == 0 {
		return nil
	}

	legacyLines := make([]string, 0, len(note.Notes))
	for _, line := range note.Notes {
		legacyLines = append(legacyLines, "- "+line)
	}
	return []memory.NoteSection{{Content: strings.Join(legacyLines, "\n")}}
}

func noteKey(noteIndex int) string {
	return fmt.Sprintf("note-%d", noteIndex)
}

func appendPanelLine(builder *strings.Builder, line string, row *int) {
	builder.WriteString(line)
	builder.WriteString("\n")
	*row++
}

func appendPanelText(builder *strings.Builder, text string, row *int) {
	for _, line := range strings.Split(text, "\n") {
		appendPanelLine(builder, line, row)
	}
}

func (c *ContextPanel) setNoteHits(hits []noteHit) {
	c.noteHits = hits
	for _, hit := range hits {
		if hit.key == c.activeNoteKey {
			return
		}
	}
	if len(hits) > 0 {
		c.activeNoteKey = hits[0].key
	} else {
		c.activeNoteKey = ""
	}
}

func (c *ContextPanel) currentNoteHits() []noteHit {
	width := c.width
	if width <= 0 {
		width = 80
	}
	_, hits := c.renderContentLayout(width)
	c.setNoteHits(hits)
	return hits
}

func (c *ContextPanel) hasNotes() bool {
	return len(c.currentNoteHits()) > 0
}

func (c *ContextPanel) noteAt(row int) (noteHit, bool) {
	for _, hit := range c.currentNoteHits() {
		if hit.row == row {
			return hit, true
		}
	}
	return noteHit{}, false
}

func (c *ContextPanel) toggleNote(key string) {
	c.expandedNotes[key] = !c.expandedNotes[key]
}

func (c *ContextPanel) toggleActiveNote() {
	if c.activeNoteKey == "" {
		return
	}
	c.toggleNote(c.activeNoteKey)
}

func (c *ContextPanel) moveActiveNote(delta int) {
	hits := c.currentNoteHits()
	if len(hits) == 0 {
		return
	}
	current := 0
	for index, hit := range hits {
		if hit.key == c.activeNoteKey {
			current = index
			break
		}
	}
	current += delta
	if current < 0 {
		current = 0
	}
	if current >= len(hits) {
		current = len(hits) - 1
	}
	c.activeNoteKey = hits[current].key
}

func (c *ContextPanel) collapseActiveNote() {
	if c.activeNoteKey != "" {
		c.expandedNotes[c.activeNoteKey] = false
	}
}

func (c *ContextPanel) expandActiveNote() {
	if c.activeNoteKey != "" {
		c.expandedNotes[c.activeNoteKey] = true
	}
}

func (c *ContextPanel) ensureActiveNoteVisible() {
	bodyHeight := c.height - 1
	if bodyHeight < 1 {
		return
	}
	for _, hit := range c.currentNoteHits() {
		if hit.key != c.activeNoteKey {
			continue
		}
		if hit.row < c.scrollOffset {
			c.scrollOffset = hit.row
		} else if hit.row >= c.scrollOffset+bodyHeight {
			c.scrollOffset = hit.row - bodyHeight + 1
		}
		return
	}
}

type contextStyles struct {
	tabActive     lipgloss.Style
	tabInactive   lipgloss.Style
	title         lipgloss.Style
	sectionHeader lipgloss.Style
	sectionActive lipgloss.Style
	empty         lipgloss.Style
}

func newContextStyles(palette apptheme.Theme) contextStyles {
	return contextStyles{
		tabActive:     lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(palette.TextStrong)).Background(lipgloss.Color(palette.Raised)),
		tabInactive:   lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextMuted)).Background(lipgloss.Color(palette.Surface)),
		title:         lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(palette.TextStrong)),
		sectionHeader: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(palette.TextStrong)),
		sectionActive: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(palette.TextStrong)).Background(lipgloss.Color(palette.Raised)),
		empty:         lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextDim)).Italic(true),
	}
}

func (c *ContextPanel) styles() contextStyles {
	return newContextStyles(c.palette)
}
