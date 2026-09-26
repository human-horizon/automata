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

const dragResizeInterval = 33 * time.Millisecond

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
type chatResizeDueMsg struct {
	generation uint64
}

type Container struct {
	innerTab *warp.Tab
	mode     ContainerMode
	palette  apptheme.Theme

	// Panels.
	chatTerminal   warp.Panel
	knowledgePanel *KnowledgePanel
	contextPanel   *ContextPanel

	// State.
	profile        string
	planWidth      int // fixed knowledge panel width in characters
	chats          []ChatInfo
	onTaskAssigned func(sessionID, taskTitle string) (tea.Cmd, error)

	// Cached dimensions for border calculations.
	width  int
	height int

	// Callbacks.
	onPlanWidthChange func(int) // called when user drags the knowledge border

	planDragActive       bool
	dragResizePending    bool
	dragResizeGeneration uint64
	lastDragResizeAt     time.Time
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
	// There is not enough room to honor the normal 20/15 cell minimums on
	// tiny terminals. Keep both panes bounded instead of returning a negative
	// width or a fraction greater than one.
	if width < 36 {
		return 0.5
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
	fraction := float64(terminalW) / float64(width)
	if fraction < 0 {
		return 0
	}
	if fraction > 1 {
		return 1
	}
	return fraction
}

// SetChat switches the container to chat mode with the knowledge side panel.
func (c *Container) SetChat(terminal warp.Panel, sessionID string) {
	c.cancelPlanDrag()
	c.Deactivate()
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

// SetChatDomain sets the canonical tree-derived domain used by the
// knowledge panel's Kanban lookup.
func (c *Container) SetChatDomain(domain string) {
	if c.knowledgePanel != nil {
		c.knowledgePanel.SetDomain(domain)
	}
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
	if c.contextPanel != nil {
		if newDomain, ok := mapping[c.contextPanel.domain]; ok {
			c.contextPanel.SetDomain(newDomain)
		}
	}
	if c.knowledgePanel != nil {
		if newDomain, ok := mapping[c.knowledgePanel.domain]; ok {
			c.knowledgePanel.SetDomain(newDomain)
		}
	}
}

// RenameSessionDomains applies per-session canonical domains after a rename
// or cross-folder move. The map is keyed by the post-migration session ID.
func (c *Container) RenameSessionDomains(domains map[string]string) {
	if c.knowledgePanel == nil {
		return
	}
	if domain, ok := domains[c.knowledgePanel.sessionID]; ok {
		c.knowledgePanel.SetDomain(domain)
	}
}

// SetFolder switches the container to folder domain mode.
func (c *Container) SetFolder(folder *tree.Item) {
	c.cancelPlanDrag()
	c.Deactivate()
	c.mode = FolderMode
	if c.contextPanel == nil {
		c.contextPanel = NewContextPanel(c.profile)
		c.contextPanel.SetChats(c.chats)
		c.contextPanel.SetOnTaskAssigned(c.onTaskAssigned)
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
	c.chats = append([]ChatInfo(nil), chats...)
	if c.contextPanel != nil {
		c.contextPanel.SetChats(c.chats)
	}
}

// Chats returns a copy of the chat options currently supplied to the Kanban picker.
func (c *Container) Chats() []ChatInfo {
	return append([]ChatInfo(nil), c.chats...)
}

// SetOnTaskAssigned sets a callback for when a task is assigned to a chat.
func (c *Container) SetOnTaskAssigned(fn func(sessionID, taskTitle string) (tea.Cmd, error)) {
	c.onTaskAssigned = fn
	if c.contextPanel != nil {
		c.contextPanel.SetOnTaskAssigned(fn)
	}
}

// Activate refreshes and starts only the panel tree visible in the current mode.
func (c *Container) Activate() tea.Cmd {
	switch c.mode {
	case ChatMode:
		var cmds []tea.Cmd
		if chat, ok := c.chatTerminal.(*ChatPanel); ok {
			cmds = append(cmds, chat.Activate())
		}
		if c.knowledgePanel != nil {
			cmds = append(cmds, c.knowledgePanel.Activate())
		}
		return tea.Batch(cmds...)
	case FolderMode:
		if c.contextPanel != nil {
			return c.contextPanel.Activate()
		}
	}
	return nil
}

// Deactivate releases hidden-panel watchers without clearing panel state.
func (c *Container) Deactivate() {
	c.cancelPlanDrag()
	if chat, ok := c.chatTerminal.(*ChatPanel); ok {
		chat.Deactivate()
	}
	if c.knowledgePanel != nil {
		c.knowledgePanel.Deactivate()
	}
	if c.contextPanel != nil {
		c.contextPanel.Deactivate()
	}
}

// Close releases filesystem watchers owned by the container's panels.
func (c *Container) Close() {
	c.Deactivate()
	if chat, ok := c.chatTerminal.(*ChatPanel); ok {
		chat.Close()
	}
	if c.knowledgePanel != nil {
		c.knowledgePanel.Close()
	}
	if c.contextPanel != nil {
		c.contextPanel.Close()
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
	SessionID    string
	Domain       string
	Profile      string
	Context      *akcontext.Data
	ContextError string
	Jobs         []akjobs.Job
	JobsError    string
	Memory       *memory.Data
	MemoryError  string
}

// RefreshKnowledgeCmd returns a command that reads the knowledge files for
// the currently displayed session/domain in a background goroutine. The result
// is delivered as a KnowledgeRefreshMsg.
func (c *Container) RefreshKnowledgeCmd() tea.Cmd {
	sessionID := ""
	domain := ""
	profile := c.profile
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
			contextData, contextErr := akcontext.ReadForProfile(profile, sessionID)
			msg.Context = contextData
			msg.ContextError = knowledgeReadError(contextErr)
			jobs, jobsErr := akjobs.ListForProfile(profile, sessionID)
			msg.Jobs = jobs
			msg.JobsError = knowledgeReadError(jobsErr)
		}
		if domain != "" {
			if d, err := memory.Read(profile, domain); err == nil {
				msg.Memory = d
			} else {
				msg.MemoryError = err.Error()
			}
		}
		return msg
	}
}

// ApplyKnowledgeRefresh applies the data carried by a KnowledgeRefreshMsg.
func (c *Container) ApplyKnowledgeRefresh(msg KnowledgeRefreshMsg) {
	if c.knowledgePanel != nil && msg.SessionID == c.knowledgePanel.sessionID {
		if msg.SessionID != "" {
			c.knowledgePanel.data = msg.Context
			c.knowledgePanel.jobs = msg.Jobs
		}
		c.knowledgePanel.contextError = msg.ContextError
		c.knowledgePanel.jobsError = msg.JobsError
		c.knowledgePanel.lastRefresh = time.Now()
		c.knowledgePanel.refreshCurrentTask()
	}
	if c.contextPanel != nil && msg.Domain == c.contextPanel.domain && msg.Profile == c.contextPanel.profile {
		if msg.Memory != nil {
			c.contextPanel.data = msg.Memory
		} else if msg.MemoryError != "" {
			c.contextPanel.data = nil
			c.contextPanel.notesStatus = "✗ Notes read error: " + msg.MemoryError
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
	if c.knowledgePanel != nil {
		c.knowledgePanel.SetProfile(profile)
	}
	if c.contextPanel != nil {
		c.contextPanel.SetProfile(profile)
	}
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
	widthChanged := width > 0 && width != c.width
	c.width = width
	c.height = height
	if widthChanged {
		c.updateSplitFraction()
	}

	return c.innerTab.View(width, height)
}

// Update forwards messages to the innerTab and handles mode-specific logic.
func (c *Container) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case chatResizeDueMsg:
		if !c.planDragActive || !c.dragResizePending || msg.generation != c.dragResizeGeneration {
			return nil
		}
		c.dragResizePending = false
		c.lastDragResizeAt = time.Now()
		return c.applyChatResize()

	case tea.WindowSizeMsg:
		c.cancelPlanDrag()
		c.width = msg.Width
		c.height = msg.Height
		// Update split fraction to match current planWidth.
		c.updateSplitFraction()
		return c.innerTab.Update(msg)

	case warp.ResizeMsg:
		c.cancelPlanDrag()
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

	if c.planDragActive {
		switch msg.Action {
		case tea.MouseActionMotion:
			c.updatePlanDrag(msg.X)
			return c.queueDragResize()
		case tea.MouseActionRelease:
			c.updatePlanDrag(msg.X)
			c.planDragActive = false
			c.cancelDragResizeTimer()
			c.lastDragResizeAt = time.Now()
			return c.applyChatResize()
		default:
			return nil
		}
	}

	if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
		borderX := c.findBorderX()
		if borderX >= 0 && int(msg.X) == borderX {
			if msg.Y == 0 {
				return nil
			}
			c.planDragActive = true
			return nil
		}
	}

	return c.innerTab.HandleMouse(msg)
}

// findBorderX returns the X position of the vertical split border, or -1.
func (c *Container) findBorderX() int {
	if c.innerTab == nil || c.chatTerminal == nil || c.width <= 0 {
		return -1
	}
	return c.terminalPanelWidth()
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

func (c *Container) updatePlanDrag(x int) {
	if c.innerTab == nil || c.chatTerminal == nil || c.width <= 0 {
		return
	}
	c.innerTab.SetSplitFraction(c.chatTerminal, float64(x)/float64(c.width))
	c.syncPlanWidth()
}

func (c *Container) queueDragResize() tea.Cmd {
	if c.dragResizePending {
		return nil
	}
	now := time.Now()
	delay := time.Duration(0)
	if !c.lastDragResizeAt.IsZero() {
		delay = time.Until(c.lastDragResizeAt.Add(dragResizeInterval))
	}
	if delay <= 0 {
		c.lastDragResizeAt = now
		return c.applyChatResize()
	}

	c.dragResizePending = true
	c.dragResizeGeneration++
	generation := c.dragResizeGeneration
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return chatResizeDueMsg{generation: generation}
	})
}

func (c *Container) cancelDragResizeTimer() {
	c.dragResizeGeneration++
	c.dragResizePending = false
}

func (c *Container) cancelPlanDrag() {
	c.planDragActive = false
	c.cancelDragResizeTimer()
}

func (c *Container) applyChatResize() tea.Cmd {
	if c.mode != ChatMode || c.height <= 0 {
		return nil
	}
	chat, ok := c.chatTerminal.(*ChatPanel)
	if !ok || chat == nil {
		return nil
	}
	return chat.Update(warp.ResizeMsg{Width: c.terminalPanelWidth(), Height: c.height})
}

func (c *Container) terminalPanelWidth() int {
	if c.innerTab == nil || c.chatTerminal == nil || c.width <= 0 {
		return 0
	}
	fraction, ok := c.innerTab.GetSplitFraction(c.chatTerminal)
	if !ok {
		return 0
	}
	available := c.width - 1
	first := int(float64(available) * fraction)
	if first < warp.MinPanelSize {
		first = warp.MinPanelSize
	}
	if available-first < warp.MinPanelSize {
		first = available - warp.MinPanelSize
	}
	if first < 0 {
		return 0
	}
	return first
}

// SetOnPlanWidthChange sets a callback invoked when the user drags the knowledge border.
func (c *Container) SetOnPlanWidthChange(fn func(int)) {
	c.onPlanWidthChange = fn
}

// Ensure Container implements warp.Panel
var _ warp.Panel = (*Container)(nil)
