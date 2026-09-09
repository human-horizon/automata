package ui

import (
	"time"

	akcontext "github.com/HumanHorizon/automata/internal/ai-knowledge/context"
	akjobs "github.com/HumanHorizon/automata/internal/ai-knowledge/jobs"
	"github.com/HumanHorizon/automata/internal/ai-knowledge/memory"
	apptheme "github.com/HumanHorizon/automata/internal/theme"
	"github.com/HumanHorizon/automata/internal/tree"
	tea "github.com/charmbracelet/bubbletea"
	warp "github.com/starframe-dev/warp"
)

// ContainerMode indicates what the right panel is currently displaying.
type ContainerMode int

const (
	// ChatMode shows a chat terminal on the left and the ai-knowledge panel
	// (status/plans/jobs/notes) on the right.
	ChatMode ContainerMode = iota
	// FolderMode shows the ai-knowledge domain panel for a folder.
	FolderMode
)

// Container is the right-side panel that switches between chat + knowledge
// and folder domain layouts. It uses warp primitives for layout and border
// rendering.
type Container struct {
	innerTab *warp.Tab
	mode     ContainerMode
	palette  apptheme.Theme

	// Panels.
	chatTerminal   warp.Panel
	knowledgePanel *KnowledgePanel
	contextPanel   *ContextPanel

	// State.
	profile   string
	planWidth int // fixed knowledge panel width in characters

	// Cached dimensions for border calculations.
	width  int
	height int

	// Callbacks.
	onPlanWidthChange func(int) // called when user drags the knowledge border
}

// NewContainer creates a new container with the given initial panel.
func NewContainer(initial warp.Panel) *Container {
	c := &Container{
		mode:      ChatMode,
		palette:   apptheme.Default(),
		planWidth: 40,
	}
	if initial != nil {
		c.chatTerminal = initial
		c.knowledgePanel = NewKnowledgePanel()
	}
	c.innerTab = warp.NewTab("container")
	c.rebuildLayout()
	return c
}

// rebuildLayout sets up the innerTab layout for the current mode.
func (c *Container) rebuildLayout() {
	switch c.mode {
	case ChatMode:
		if c.chatTerminal == nil || c.knowledgePanel == nil {
			return
		}
		c.innerTab.SetRootPanel(c.chatTerminal)
		frac := c.planFraction(c.width)
		c.innerTab.SplitVertical(c.chatTerminal, frac, c.knowledgePanel)
	case FolderMode:
		if c.contextPanel == nil {
			return
		}
		c.innerTab.SetRootPanel(c.contextPanel)
	}
}

// planFraction returns the split fraction for the terminal (left) panel.
func (c *Container) planFraction(width int) float64 {
	if width <= 0 {
		return 0.7
	}
	planW := c.planWidth
	if planW < 15 {
		planW = 15
	}
	if planW > width-21 {
		planW = width - 21
	}
	terminalW := width - 1 - planW
	if terminalW < 20 {
		terminalW = 20
		planW = width - 1 - terminalW
	}
	return float64(terminalW) / float64(width)
}

// SetChat switches the container to chat mode with the knowledge side panel.
func (c *Container) SetChat(terminal warp.Panel, sessionID string) {
	c.mode = ChatMode
	c.chatTerminal = terminal
	if chat, ok := terminal.(*ChatPanel); ok {
		chat.SetTheme(c.palette)
	}
	if c.knowledgePanel == nil {
		c.knowledgePanel = NewKnowledgePanel()
	}
	c.knowledgePanel.SetSession(sessionID)
	c.rebuildLayout()
	c.innerTab.SetFocus(terminal)
}

// RenameSessionIDs updates the currently displayed chat and knowledge
// panels after a tree rename. The caller has already stopped the emulators.
func (c *Container) RenameSessionIDs(mapping map[string]string) {
	if cp, ok := c.chatTerminal.(*ChatPanel); ok {
		cp.RenameSessionIDs(mapping)
	}
	if c.knowledgePanel != nil {
		if newID, ok := mapping[c.knowledgePanel.sessionID]; ok {
			c.knowledgePanel.SetSession(newID)
		}
	}
}

// RenameDomains updates the currently displayed folder context after a tree
// rename. The caller has already moved the domain directories.
func (c *Container) RenameDomains(mapping map[string]string) {
	if c.contextPanel == nil {
		return
	}
	if newDomain, ok := mapping[c.contextPanel.domain]; ok {
		c.contextPanel.SetDomain(newDomain)
	}
}

// SetFolder switches the container to folder domain mode.
func (c *Container) SetFolder(folder *tree.Item) {
	c.mode = FolderMode
	if c.contextPanel == nil {
		c.contextPanel = NewContextPanel(c.profile)
	}
	c.contextPanel.SetTheme(c.palette)
	c.contextPanel.SetDomain(folder.Domain(c.profile))
	c.rebuildLayout()
	c.innerTab.SetFocus(c.contextPanel)
}

// SetTheme applies a palette to all native panels currently owned by the container.
func (c *Container) SetTheme(palette apptheme.Theme) {
	c.palette = palette
	if c.knowledgePanel != nil {
		c.knowledgePanel.SetTheme(palette)
	}
	if c.contextPanel != nil {
		c.contextPanel.SetTheme(palette)
	}
	if chat, ok := c.chatTerminal.(*ChatPanel); ok {
		chat.SetTheme(palette)
	}
}

// SetChats forwards the chat list to the context panel for the kanban picker.
func (c *Container) SetChats(chats []ChatInfo) {
	if c.contextPanel != nil {
		c.contextPanel.SetChats(chats)
	}
}

// SetOnTaskAssigned sets a callback for when a task is assigned to a chat.
func (c *Container) SetOnTaskAssigned(fn func(sessionID, taskTitle string)) {
	if c.contextPanel != nil {
		c.contextPanel.SetOnTaskAssigned(fn)
	}
}

// RefreshKnowledge tells the knowledge and context panels to re-read data
// from disk. Call from a tick.
func (c *Container) RefreshKnowledge() {
	if c.knowledgePanel != nil {
		c.knowledgePanel.Refresh()
	}
	if c.contextPanel != nil {
		c.contextPanel.Refresh()
	}
}

// KnowledgeRefreshMsg carries data read asynchronously from disk for the
// knowledge and context panels. The UI applies it in the main Update loop so
// file I/O never blocks the renderer.
type KnowledgeRefreshMsg struct {
	SessionID string
	Domain    string
	Profile   string
	Context   *akcontext.Data
	Jobs      []akjobs.Job
	Memory    *memory.Data
}

// RefreshKnowledgeCmd returns a command that reads the knowledge files for
// the currently displayed session/domain in a background goroutine. The result
// is delivered as a KnowledgeRefreshMsg.
func (c *Container) RefreshKnowledgeCmd() tea.Cmd {
	sessionID := ""
	domain := ""
	profile := ""
	if c.knowledgePanel != nil {
		sessionID = c.knowledgePanel.sessionID
	}
	if c.contextPanel != nil {
		domain = c.contextPanel.domain
		profile = c.contextPanel.profile
	}
	return func() tea.Msg {
		msg := KnowledgeRefreshMsg{
			SessionID: sessionID,
			Domain:    domain,
			Profile:   profile,
		}
		if sessionID != "" {
			if d, err := akcontext.Read(sessionID); err == nil {
				msg.Context = d
			}
			if j, err := akjobs.List(sessionID); err == nil {
				msg.Jobs = j
			}
		}
		if domain != "" {
			if d, err := memory.Read(profile, domain); err == nil {
				msg.Memory = d
			}
		}
		return msg
	}
}

// ApplyKnowledgeRefresh applies the data carried by a KnowledgeRefreshMsg.
func (c *Container) ApplyKnowledgeRefresh(msg KnowledgeRefreshMsg) {
	if c.knowledgePanel != nil && msg.SessionID == c.knowledgePanel.sessionID {
		if msg.Context != nil {
			c.knowledgePanel.data = msg.Context
		}
		if msg.Jobs != nil {
			c.knowledgePanel.jobs = msg.Jobs
		}
		c.knowledgePanel.lastRefresh = time.Now()
		c.knowledgePanel.refreshCurrentTask()
	}
	if c.contextPanel != nil && msg.Domain == c.contextPanel.domain && msg.Profile == c.contextPanel.profile {
		if msg.Memory != nil {
			c.contextPanel.data = msg.Memory
		}
	}
}

// Active returns the currently displayed primary panel.
func (c *Container) Active() warp.Panel {
	if c.mode == ChatMode {
		return c.chatTerminal
	}
	if c.mode == FolderMode {
		return c.contextPanel
	}
	return nil
}

// Knowledge returns the right-side knowledge panel in chat mode.
func (c *Container) Knowledge() warp.Panel {
	if c.mode != ChatMode {
		return nil
	}
	return c.knowledgePanel
}

// Focused returns the panel currently focused inside the container.
func (c *Container) Focused() warp.Panel {
	if c.innerTab == nil {
		return nil
	}
	return c.innerTab.Focus()
}

// SetFocus focuses a panel inside the container.
func (c *Container) SetFocus(panel warp.Panel) {
	if c.innerTab == nil || panel == nil {
		return
	}
	c.innerTab.SetFocus(panel)
}

// SetProfile sets the profile used to compute folder domains.
func (c *Container) SetProfile(profile string) {
	c.profile = profile
}

// PlanWidth returns the fixed knowledge panel width in characters.
func (c *Container) PlanWidth() int {
	if c.planWidth <= 0 {
		return 40
	}
	return c.planWidth
}

// SetPlanWidth sets the fixed knowledge panel width.
func (c *Container) SetPlanWidth(w int) {
	c.planWidth = w
}

// View renders the container content by delegating to the innerTab.
// Does NOT call Update on innerTab — that happens in the normal Update path
// so commands (like Listen for PTY output) are not lost.
func (c *Container) View(width, height int) string {
	c.width = width
	c.height = height

	return c.innerTab.View(width, height)
}

// Update forwards messages to the innerTab and handles mode-specific logic.
func (c *Container) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		c.width = msg.Width
		c.height = msg.Height
		// Update split fraction to match current planWidth.
		c.updateSplitFraction()
		return c.innerTab.Update(msg)

	case warp.ResizeMsg:
		c.width = msg.Width
		c.height = msg.Height
		// Update split fraction to match current planWidth.
		c.updateSplitFraction()
		return c.innerTab.Update(msg)

	case tea.MouseMsg:
		return c.handleMouse(msg)
	}

	return c.innerTab.Update(msg)
}

// updateSplitFraction sets the innerTab split fraction to match planWidth.
func (c *Container) updateSplitFraction() {
	if c.mode != ChatMode || c.chatTerminal == nil || c.width <= 0 {
		return
	}
	frac := c.planFraction(c.width)
	c.innerTab.SetSplitFraction(c.chatTerminal, frac)
}

func (c *Container) handleMouse(msg tea.MouseMsg) tea.Cmd {
	if c.mode != ChatMode {
		return c.innerTab.HandleMouse(msg)
	}

	if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
		borderX := c.findBorderX()
		if borderX >= 0 && int(msg.X) == borderX && msg.Y == 0 {
			return nil
		}
	}

	// Forward to innerTab's HandleMouse for border dragging.
	cmd := c.innerTab.HandleMouse(msg)
	c.syncPlanWidth()
	return cmd
}

// findBorderX returns the X position of the vertical split border, or -1.
func (c *Container) findBorderX() int {
	if c.innerTab == nil || c.chatTerminal == nil || c.width <= 0 {
		return -1
	}
	frac, ok := c.innerTab.GetSplitFraction(c.chatTerminal)
	if !ok {
		return -1
	}
	return int(float64(c.width) * frac)
}

// syncPlanWidth reads the current split fraction and updates planWidth.
func (c *Container) syncPlanWidth() {
	if c.mode != ChatMode || c.innerTab == nil || c.width <= 0 {
		return
	}
	frac, ok := c.innerTab.GetSplitFraction(c.chatTerminal)
	if !ok {
		return
	}
	terminalW := int(float64(c.width) * frac)
	newPlanW := c.width - 1 - terminalW
	if newPlanW < 15 {
		newPlanW = 15
	}
	if newPlanW != c.planWidth {
		c.planWidth = newPlanW
		if c.onPlanWidthChange != nil {
			c.onPlanWidthChange(newPlanW)
		}
	}
}

// SetOnPlanWidthChange sets a callback invoked when the user drags the knowledge border.
func (c *Container) SetOnPlanWidthChange(fn func(int)) {
	c.onPlanWidthChange = fn
}

// Ensure Container implements warp.Panel
var _ warp.Panel = (*Container)(nil)
