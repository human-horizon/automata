package ui

import (
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/HumanHorizon/automata/internal/paths"
	"github.com/Starframe/portalis"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	warp "github.com/starframe-dev/warp"
)

// FamiliarState matches the structure written by the pi familiar extension.
type FamiliarState struct {
	ID      string `json:"id"`
	SessionID  string `json:"sessionId"`
	Created string `json:"created"`
}

// chatSession represents one tab in the chat panel.
type chatSession struct {
	name   string
	panel  warp.Panel
	em     *portalis.Emulator
	familiarID string // empty for main session
}

// ChatPanel manages multiple terminal sessions (main + familiars) with a tab
// bar at the bottom. It polls ~/.ai/automata/sessions/<sessionID>/familiars.json
// to detect new familiars and creates TermPanel tabs (Portalis Emulator) for them.
type ChatPanel struct {
	sessions  []*chatSession
	activeIdx int
	sessionID string
	profile   string

	// Polling state.
	known   map[string]bool // familiar IDs we already have tabs for
	started bool

	// Cached dimensions for tab bar rendering.
	width  int
	height int

	// onClearSession is called when the user clicks the "× Clear" button in the tab bar.
	// The handler is responsible for stopping the emulator, deleting the .jsonl file,
	// and restarting pi. Returning a non-nil cmd lets the panel chain after clear.
	onClearSession func(sessionID, cwd string) tea.Cmd

	// createFamiliarEmulator creates a portalis.Emulator for a new familiar tab.
	// Returns the emulator and optional extra env vars for StartWithEnv.
	// Set by main.go; has access to pi agent directory and other app state.
	createFamiliarEmulator func(sessionID string) (em *portalis.Emulator, env []string)
}

// NewChatPanel creates a ChatPanel with the given main session emulator.
func NewChatPanel(mainEm *portalis.Emulator, sessionID, profile string) *ChatPanel {
	return &ChatPanel{
		sessions: []*chatSession{
			{
				name:  "Main",
				panel: NewTermPanel(mainEm),
				em:    mainEm,
			},
		},
		activeIdx: 0,
		sessionID: sessionID,
		profile:   profile,
		known:     make(map[string]bool),
	}
}

// SetOnClearSession sets the handler invoked when the user clicks the "× Clear"
// button on the chat tab bar. The handler should stop the emulator, delete the
// .jsonl file for the session, and restart pi.
func (cp *ChatPanel) SetOnClearSession(fn func(sessionID, cwd string) tea.Cmd) {
	cp.onClearSession = fn
}

// SetCreateFamiliarEmulator sets the handler that creates a portalis.Emulator
// for a new familiar tab. Returns the emulator and optional extra env vars.
func (cp *ChatPanel) SetCreateFamiliarEmulator(fn func(sessionID string) (em *portalis.Emulator, env []string)) {
	cp.createFamiliarEmulator = fn
}

// ActiveEmulator returns the emulator for the currently active session.
// All sessions (Main + familiars) have emulators.
func (cp *ChatPanel) ActiveEmulator() *portalis.Emulator {
	if cp.activeIdx >= 0 && cp.activeIdx < len(cp.sessions) {
		return cp.sessions[cp.activeIdx].em
	}
	return nil
}

// Sessions returns the underlying chat session list (Main + familiars).
// Exposed for callers that need to inspect or mutate per-tab state
// outside of the panel (e.g. clearSessionCmd in main.go).
func (cp *ChatPanel) Sessions() []*chatSession {
	return cp.sessions
}

// Em returns the portalis.Emulator for this chat session.
// Exposed so external callers (clearSessionCmd) can stop/restart the
// underlying PTY without poking at unexported fields.
func (s *chatSession) Em() *portalis.Emulator {
	return s.em
}

// FamiliarID returns the familiar session id for this chat session,
// or an empty string for the Main tab.
func (s *chatSession) FamiliarID() string {
	return s.familiarID
}

// FamiliarSessionIDs returns the familiar session IDs of all non-Main tabs.
// Order matches cp.sessions. Used by clearSessionCmd to know which
// familiar emulators to stop and which JSONL files to delete.
func (cp *ChatPanel) FamiliarSessionIDs() []string {
	var ids []string
	for _, s := range cp.sessions {
		if s.familiarID == "" {
			continue
		}
		ids = append(ids, s.familiarID)
	}
	return ids
}

// startPolling begins polling for familiars. Called on first Update.
func (cp *ChatPanel) startPolling() tea.Cmd {
	cp.started = true
	return cp.pollTick()
}

// pollTick checks for new/removed familiars and schedules the next poll.
func (cp *ChatPanel) pollTick() tea.Cmd {
	cmds := cp.checkFamiliars()
	// Schedule next poll in 3 seconds.
	tickCmd := tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
		return pollFamiliarsMsg(t)
	})
	if len(cmds) == 0 {
		return tickCmd
	}
	return tea.Batch(append(cmds, tickCmd)...)
}

// pollFamiliarsMsg is sent by the ticker to trigger a familiar check.
type pollFamiliarsMsg time.Time

// familiarDetectedMsg is sent when a new familiar is found.
type familiarDetectedMsg struct {
	id     string
	familiarID string
}

// familiarRemovedMsg is sent when a familiar is removed.
type familiarRemovedMsg struct {
	id string
}

// familiarStatePath returns the path to the familiars.json file for a session.
// Uses profile-aware path when ChatPanel.profile is set.
func (cp *ChatPanel) familiarStatePath() string {
	return paths.FamiliarsJSONLPath(cp.profile, cp.sessionID)
}

// loadFamiliars reads the familiars.json file for the ChatPanel's session.
func (cp *ChatPanel) loadFamiliars() []FamiliarState {
	path := cp.familiarStatePath()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var familiars []FamiliarState
	if err := json.Unmarshal(data, &familiars); err != nil {
		return nil
	}
	return familiars
}

// checkFamiliars compares the current familiars.json with known familiars
// and returns detected/removed messages. Only shows familiars from the
// current session — each chat sees only its own familiars.
func (cp *ChatPanel) checkFamiliars() []tea.Cmd {
	familiars := cp.loadFamiliars()
	if familiars == nil {
		familiars = []FamiliarState{}
	}

	// Build set of currently-alive familiar session IDs in this chat.
	liveSessionIDs := make(map[string]bool)
	for _, s := range cp.sessions {
		if s.familiarID != "" {
			liveSessionIDs[s.familiarID] = true
		}
	}

	var cmds []tea.Cmd
	seen := make(map[string]bool)
	for _, f := range familiars {
		seen[f.ID] = true
		// Skip if we're already tracking a session for this familiar, or
		// cp.known already has it (avoids duplicate spawns from racing
		// poll + PtyExitMsg + poll cycles during startup).
		if cp.known[f.ID] || liveSessionIDs[f.SessionID] {
			continue
		}
		cp.known[f.ID] = true
		cmds = append(cmds, func() tea.Msg {
			return familiarDetectedMsg{id: f.ID, familiarID: f.SessionID}
		})
	}
	for id := range cp.known {
		if !seen[id] {
			delete(cp.known, id)
			cmds = append(cmds, func() tea.Msg {
				return familiarRemovedMsg{id: id}
			})
		}
	}
	return cmds
}

// addFamiliar creates a new tab for a detected familiar.
// Uses a TermPanel (Portalis Emulator) like the Main tab — no tmux needed.
func (cp *ChatPanel) addFamiliar(id, familiarID string) tea.Cmd {
	// Idempotency: skip if we already track a session with the same
	// familiarID OR same name (id). The duplicate-by-name check guards
	// against races where cp.known was cleared but the prior session
	// is still in cp.sessions.
	for _, s := range cp.sessions {
		if s.familiarID == familiarID {
			return nil
		}
		if s.name == id && s.familiarID != "" {
			return nil
		}
	}

	// Create a portalis.Emulator for this familiar via the callback.
	// The callback is set by main.go and has access to pi agent directory.
	if cp.createFamiliarEmulator == nil {
		return nil
	}
	em, env := cp.createFamiliarEmulator(familiarID)
	if em == nil {
		return nil
	}

	// Set PI_OWNER_SESSION so the familiar's pi agent knows who its owner is.
	// This is used by ask_owner and other familiar tools.
	env = append(env, "PI_OWNER_SESSION="+cp.sessionID)

	// Create a TermPanel wrapping the emulator (same as Main tab).
	panel := NewTermPanel(em)
	cp.sessions = append(cp.sessions, &chatSession{
		name:   id,
		panel:  panel,
		em:     em,
		familiarID: familiarID,
	})
	// Start the emulator with the extra env (e.g. PI_CODING_AGENT_DIR, PI_OWNER_SESSION).
	return em.StartWithEnv(env)
}

// stopper is implemented by panels that need cleanup when removed.
type stopper interface {
	Stop()
}

// removeDeadFamiliar removes a familiar tab whose PTY has exited.
// Matches by SessionID (the familiar's own session id, e.g.
// humanhorizon__human-horizon.automata.ai-2__test6) and clears cp.known
// so the next checkFamiliars poll re-creates the tab from familiars.json.
func (cp *ChatPanel) removeDeadFamiliar(sessionID string) {
	for i, s := range cp.sessions {
		// Match if SessionID matches OR if the panel is a dead familiar
		// (we no longer have an emulator reference for it).
		if s.em != nil && s.em.SessionID != sessionID {
			continue
		}
		if st, ok := s.panel.(stopper); ok {
			st.Stop()
		}
		delete(cp.known, s.name)
		cp.sessions = append(cp.sessions[:i], cp.sessions[i+1:]...)
		if cp.activeIdx >= len(cp.sessions) {
			cp.activeIdx = len(cp.sessions) - 1
		}
		if cp.activeIdx < 0 {
			cp.activeIdx = 0
		}
		return
	}
}

// removeFamiliar removes the tab for a familiar that has been removed.
func (cp *ChatPanel) removeFamiliar(id string) {
	for i, s := range cp.sessions {
		if s.name == id {
			// Stop the familiar panel if it supports cleanup.
			if st, ok := s.panel.(stopper); ok {
				st.Stop()
			}
			cp.sessions = append(cp.sessions[:i], cp.sessions[i+1:]...)
			if cp.activeIdx >= len(cp.sessions) {
				cp.activeIdx = len(cp.sessions) - 1
			}
			if cp.activeIdx < 0 {
				cp.activeIdx = 0
			}
			return
		}
	}
}

// View renders the chat panel.
func (cp *ChatPanel) View(width, height int) string {
	cp.width = width
	cp.height = height

	if len(cp.sessions) == 0 {
		return ""
	}

	// Reserve 1 row for the tab bar.
	if height < 1 {
		return ""
	}
	termHeight := height - 1
	if termHeight < 1 {
		termHeight = 1
	}

	active := cp.sessions[cp.activeIdx]
	termView := active.panel.View(width, termHeight)
	return cp.joinWithBar(termView, termHeight, width)
}

// joinWithBar appends the tab bar to the rendered terminal area, padding
// the terminal block to termHeight rows so the tab bar always lands flush
// at the bottom of the panel.
func (cp *ChatPanel) joinWithBar(termView string, termHeight, width int) string {
	lines := strings.Split(termView, "\n")
	for len(lines) < termHeight {
		lines = append(lines, "")
	}
	if len(lines) > termHeight {
		lines = lines[:termHeight]
	}
	tabBar := cp.renderTabBar(width)
	return strings.Join(lines, "\n") + "\n" + tabBar
}

// renderTabBar renders the tab bar at the bottom. Layout, right-aligned:
//     [...tabs...]   <padding>   <clear-btn>
func (cp *ChatPanel) renderTabBar(width int) string {
	if width <= 0 {
		return ""
	}

	const clearBtnText = " × Clear "
	clearW := ansi.StringWidth(clearBtnText)

	var tabs []string
	for i, s := range cp.sessions {
		tab := " " + s.name + " "
		if i == cp.activeIdx {
			tab = lipgloss.NewStyle().
				Background(lipgloss.Color("4")).
				Foreground(lipgloss.Color("0")).
				Render(tab)
		} else {
			tab = lipgloss.NewStyle().
				Background(lipgloss.Color("8")).
				Foreground(lipgloss.Color("7")).
				Render(tab)
		}
		tabs = append(tabs, tab)
	}

	bar := strings.Join(tabs, " ")

	// Build "× Clear" button.
	clearBtn := lipgloss.NewStyle().
		Background(lipgloss.Color("#3c3836")).
		Foreground(lipgloss.Color("#cc241d")).
		Bold(true).
		Render(clearBtnText)

	// Use lipgloss.Width for tabs (matches terminal cell count after styling).
	visibleWidth := lipgloss.Width(bar)

	// If not enough room for tabs + buttons, truncate tabs to fit.
	buttonsW := clearW
	availableForTabs := width - buttonsW
	if availableForTabs < 0 {
		availableForTabs = 0
	}
	if visibleWidth > availableForTabs {
		bar = ansi.Truncate(bar, availableForTabs, "")
		visibleWidth = lipgloss.Width(bar)
	}

	// Pad with spaces so the buttons sit flush against the right edge.
	padCount := width - visibleWidth - buttonsW
	if padCount < 0 {
		padCount = 0
	}
	bar += strings.Repeat(" ", padCount) + clearBtn
	return bar
}

// Update handles messages for the ChatPanel.
func (cp *ChatPanel) Update(msg tea.Msg) tea.Cmd {
	// Start polling on first Update, but still handle the message.
	var pollCmd tea.Cmd
	if !cp.started {
		pollCmd = cp.startPolling()
	}

	var forwardCmd tea.Cmd
	switch msg := msg.(type) {
	case pollFamiliarsMsg:
		forwardCmd = cp.pollTick()

	case familiarDetectedMsg:
		forwardCmd = cp.addFamiliar(msg.id, msg.familiarID)

	case familiarRemovedMsg:
		cp.removeFamiliar(msg.id)

	case tea.MouseMsg:
		forwardCmd = cp.handleMouse(msg)

	case tea.KeyMsg:
		// Forward keystrokes to the active session's panel.
		if cp.activeIdx >= 0 && cp.activeIdx < len(cp.sessions) {
			forwardCmd = cp.sessions[cp.activeIdx].panel.Update(msg)
		}

	case warp.ResizeMsg:
		cp.width = msg.Width
		cp.height = msg.Height
		forwardCmd = cp.resizeActivePanel()

	case tea.WindowSizeMsg:
		cp.width = msg.Width
		cp.height = msg.Height
		forwardCmd = cp.resizeActivePanel()

	case familiarOutputMsg, familiarPaneIDMsg:
		// Familiars now use TermPanel (Portalis Emulator), not FamiliarPanel.
		// These messages are no longer sent — fall through to default routing.
		forwardCmd = cp.routeBySessionID(msg)
		if forwardCmd == nil {
			if cp.activeIdx >= 0 && cp.activeIdx < len(cp.sessions) {
				forwardCmd = cp.sessions[cp.activeIdx].panel.Update(msg)
			}
		}

	case portalis.PtyExitMsg:
		// When a PTY exits (familiar died), remove it from sessions and clear
		// cp.known so the next checkFamiliars poll can re-spawn it from
		// familiars.json. The familiar's pi agent itself is responsible for
		// shutting down cleanly (writes .kill marker on free_familiar).
		cp.removeDeadFamiliar(msg.SessionID)

	default:
		// Route messages with SessionID to the correct session.
		forwardCmd = cp.routeBySessionID(msg)
		if forwardCmd == nil {
			// Fallback: forward to active session only.
			if cp.activeIdx >= 0 && cp.activeIdx < len(cp.sessions) {
				forwardCmd = cp.sessions[cp.activeIdx].panel.Update(msg)
			}
		}
	}

	return tea.Batch(pollCmd, forwardCmd)
}

// routeBySessionID routes messages with a SessionID field to the correct session.
// Returns nil if the message doesn't have a SessionID or no matching session is found.
func (cp *ChatPanel) routeBySessionID(msg tea.Msg) tea.Cmd {
	var sessionID string
	switch m := msg.(type) {
	case portalis.PtyReadyMsg:
		sessionID = m.SessionID
	case portalis.PtyOutputMsg:
		sessionID = m.SessionID
	case portalis.PtyExitMsg:
		sessionID = m.SessionID
	default:
		return nil
	}

	for _, s := range cp.sessions {
		if s.em != nil && s.em.SessionID == sessionID {
			return s.panel.Update(msg)
		}
	}
	return nil
}

// routeFamiliarMsg routes familiarOutputMsg and familiarPaneIDMsg to the
// correct FamiliarPanel by matching familiarID. Returns nil if no match.
func (cp *ChatPanel) routeFamiliarMsg(msg tea.Msg) tea.Cmd {
	var familiarID string
	switch m := msg.(type) {
	case familiarOutputMsg:
		familiarID = m.sessionID
	case familiarPaneIDMsg:
		// familiarPaneIDMsg carries pane ID, not familiarID — route to all
		// FamiliarPanels, each checks if it's the right one.
		for _, s := range cp.sessions {
			if fp, ok := s.panel.(*FamiliarPanel); ok {
				if cmd := fp.Update(msg); cmd != nil {
					return cmd
				}
			}
		}
		return nil
	default:
		return nil
	}

	for _, s := range cp.sessions {
		if s.familiarID == familiarID {
			return s.panel.Update(msg)
		}
	}
	return nil
}

// handleMouse handles mouse clicks on the tab bar.
func (cp *ChatPanel) handleMouse(msg tea.Msg) tea.Cmd {
	m, ok := msg.(tea.MouseMsg)
	if !ok {
		return nil
	}

	// Forward wheel/motion/release events to the active session.
	if m.Button == tea.MouseButtonWheelUp || m.Button == tea.MouseButtonWheelDown ||
		m.Action == tea.MouseActionMotion || m.Action == tea.MouseActionRelease {
		if cp.activeIdx >= 0 && cp.activeIdx < len(cp.sessions) {
			return cp.sessions[cp.activeIdx].panel.Update(msg)
		}
		return nil
	}

	// Only handle left-click for tab switching.
	if m.Action != tea.MouseActionPress || m.Button != tea.MouseButtonLeft {
		return nil
	}

	// Check if click is on the tab bar (last row).
	if int(m.Y) != cp.height-1 {
		// Forward to the active session.
		if cp.activeIdx >= 0 && cp.activeIdx < len(cp.sessions) {
			return cp.sessions[cp.activeIdx].panel.Update(msg)
		}
		return nil
	}

	// Right-aligned button: "× Clear".
	// Clear occupies the rightmost 9 cells (1 padding + "× Clear" 8 wide).
	const clearBtnWidth = 9
	if int(m.X) >= cp.width-clearBtnWidth && cp.onClearSession != nil {
		active := cp.sessions[cp.activeIdx]
		var cwd string
		if active != nil && active.em != nil {
			cwd = active.em.CWD()
		}
		return cp.onClearSession(cp.sessionID, cwd)
	}

	// Find which tab was clicked.
	x := 0
	for i, s := range cp.sessions {
		tabLen := len(s.name) + 2 // " name "
		if int(m.X) >= x && int(m.X) < x+tabLen {
			if i != cp.activeIdx {
				cp.activeIdx = i
				// Send ResizeMsg to the new active session so it knows its size.
				termHeight := cp.height - 1
				if termHeight < 1 {
					termHeight = 1
				}
				adjusted := warp.ResizeMsg{Width: cp.width, Height: termHeight}
				return cp.sessions[i].panel.Update(adjusted)
			}
			return nil
		}
		x += tabLen + 1 // +1 for space between tabs
	}

	return nil
}

// resizeActivePanel sends a fresh warp.ResizeMsg to all sessions so they
// know their current size. FamiliarPanel uses this to keep its tmux pane
// in sync even when the user isn't currently viewing it.
func (cp *ChatPanel) resizeActivePanel() tea.Cmd {
	if cp.width <= 0 || cp.height <= 1 {
		return nil
	}
	termHeight := cp.height - 1
	if termHeight < 1 {
		termHeight = 1
	}
	var cmds []tea.Cmd
	for _, s := range cp.sessions {
		if s != nil && s.panel != nil {
			cmds = append(cmds, s.panel.Update(warp.ResizeMsg{Width: cp.width, Height: termHeight}))
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}
