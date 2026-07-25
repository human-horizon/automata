package ui

import (
	"fmt"
	"os"
	"strings"

	"github.com/HumanHorizon/automata/internal/ai-knowledge/memory"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	warp "github.com/starframe-dev/warp"
)

// ContextPanel renders folder context with Content and Kanban tabs. It is a
// native Warp panel so hover and mouse selection work consistently.
type ContextPanel struct {
	profile      string
	domain       string
	width        int
	height       int
	activeTab    int // 0 = Content, 1 = Kanban
	scrollOffset int
	data         *memory.Data
	notesReader  *memory.CachedReader
	kanbanPanel  *KanbanPanel
}

// NewContextPanel creates an empty context panel for the given profile.
func NewContextPanel(profile string) *ContextPanel {
	return &ContextPanel{
		profile:     profile,
		notesReader: memory.NewCachedReader(),
		kanbanPanel: NewKanbanPanel(profile),
	}
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
	c.domain = domain
	c.scrollOffset = 0
	c.refresh()
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

// Update handles resize, tab switching, and scrolling.
func (c *ContextPanel) Update(msg tea.Msg) tea.Cmd {
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
		return c.handleMouse(msg)
	case tea.KeyMsg:
		c.handleKey(msg)
	}
	return nil
}

func (c *ContextPanel) handleMouse(msg tea.MouseMsg) tea.Cmd {
	f, _ := os.OpenFile("/tmp/context-mouse.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if f != nil {
		fmt.Fprintf(f, "ctx: action=%d btn=%d x=%d y=%d tab=%d\n",
			msg.Action, msg.Button, msg.X, msg.Y, c.activeTab)
		f.Close()
	}
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
	// Forward mouse to kanban panel when on kanban tab
	if c.activeTab == 1 && msg.Y > 0 {
		relMsg := tea.MouseMsg{
			X:      msg.X,
			Y:      msg.Y - 1,
			Action: msg.Action,
			Button: msg.Button,
		}
		return c.kanbanPanel.Update(relMsg)
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
	switch msg.String() {
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
	contentStyle := tabInactiveStyle
	kanbanStyle := tabInactiveStyle
	if c.activeTab == 0 {
		contentStyle = tabActiveStyle
	} else {
		kanbanStyle = tabActiveStyle
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
		return c.renderContent(width, height)
	}
	return c.kanbanPanel.View(width, height)
}

func (c *ContextPanel) renderContent(width, height int) string {
	if c.domain == "" {
		return emptyStyle.Render(" No domain context ")
	}
	if c.data == nil || len(c.data.Notes) == 0 {
		return emptyStyle.Render(" No notes for this domain ")
	}

	var b strings.Builder
	for _, note := range c.data.Notes {
		b.WriteString(titleStyle.Render("  " + note.Title))
		b.WriteString("\n")
		for _, n := range note.Notes {
			line := "    • " + n
			// Wrap long lines
			wrapped := wrapString(line, width)
			for _, wl := range wrapped {
				b.WriteString(itemStyle.Render(wl))
				b.WriteString("\n")
			}
		}
	}
	return b.String()
}

var (
	tabActiveStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#282828")).Background(lipgloss.Color("#d79921"))
	tabInactiveStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#a89984")).Background(lipgloss.Color("#3c3836"))
	titleStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#edb449"))
	emptyStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Italic(true)
	itemStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("#cdccc3"))
	doneStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("#bef264"))
)
