package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/HumanHorizon/automata/internal/paths"
	apptheme "github.com/HumanHorizon/automata/internal/theme"
	"github.com/Starframe/portalis"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	warp "github.com/starframe-dev/warp"
)

// FamiliarState matches the structure written by the pi familiar extension.
type FamiliarState struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Created   string `json:"created"`
}

// chatSession represents one tab in the chat panel.
type chatSession struct {
	name       string
	panel      warp.Panel
	em         *portalis.Emulator
	familiarID string // empty for main session
}

// ChatPanel manages multiple terminal sessions (main + familiars) with a tab
// bar at the bottom. It polls ~/.ai/automata/sessions/<sessionID>/familiars.json
// to detect new familiars and creates TermPanel tabs (Portalis Emulator) for them.
type ChatPanel struct {
	sessions  []*chatSession
	activeIdx int
	sessionID string
	palette   apptheme.Theme
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

	// onCloseFamiliar is called when the user confirms closing a familiar via the
	// × button on a familiar tab. The handler stops the emulator, deletes the
	// familiar's JSONL, and removes its entry from familiars.json.
	//
	// Synchronous (no tea.Cmd return) on purpose: Modal.Action callbacks and
	// the Y-key path both fire the close, and Modal.Action is a plain
	// `func()` — there's no way to surface a returned cmd from inside it.
	// Doing the cleanup synchronously here means both mouse and keyboard
	// confirm paths work identically. See Anya's report 2026-08-25.
	onCloseFamiliar func(familiarID string, em *portalis.Emulator)

	// pendingCloseFamiliar holds the familiarID awaiting y/n confirmation.
	// When non-empty, View draws a confirm overlay on top of the terminal area.
	pendingCloseFamiliar string

	// closeFamiliarModal is the Warp modal shown for pendingCloseFamiliar.
	// Built lazily when pendingCloseFamiliar is set, dropped after confirm/cancel.
	closeFamiliarModal *warp.Modal

	// createFamiliarEmulator creates a portalis.Emulator for a new familiar tab.
	// Returns the emulator and optional extra env vars for StartWithEnv.
	// Set by main.go; has access to pi agent directory and other app state.
	createFamiliarEmulator func(sessionID string) (em *portalis.Emulator, env []string)
}

// NewChatPanel creates a ChatPanel with the given main session emulator.
func NewChatPanel(mainEm *portalis.Emulator, sessionID, profile string) *ChatPanel {
	return &ChatPanel{
		palette: apptheme.Default(),
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

// SetTheme updates the palette used by the chat tab bar.
func (cp *ChatPanel) SetTheme(palette apptheme.Theme) {
	cp.palette = palette
}

// SetOnClearSession sets the handler invoked when the user clicks the "× Clear"
// button on the chat tab bar. The handler should stop the emulator, delete the
// .jsonl file for the session, and restart pi.
func (cp *ChatPanel) SetOnClearSession(fn func(sessionID, cwd string) tea.Cmd) {
	cp.onClearSession = fn
}

// SetOnCloseFamiliar sets the handler invoked when the user confirms closing
// a familiar via the × button on a familiar tab. The handler stops the
// emulator, deletes the familiar's JSONL, and removes the entry from
// familiars.json.
//
// Synchronous by design — see the comment on ChatPanel.onCloseFamiliar.
func (cp *ChatPanel) SetOnCloseFamiliar(fn func(familiarID string, em *portalis.Emulator)) {
	cp.onCloseFamiliar = fn
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

// SessionID returns the owning session id (the main chat's sessionID).
// Needed by callers outside the panel (e.g. main.go for clearSession
// bookkeeping) that have to address the panel without poking at unexported
// fields.
func (cp *ChatPanel) SessionID() string {
	return cp.sessionID
}

// RenameSessionIDs updates the main and familiar session IDs after an
// external tree rename. Emulators are already stopped by the caller, so
// changing their routing IDs cannot leave an old PTY event in flight.
func (cp *ChatPanel) RenameSessionIDs(mapping map[string]string) {
	if newID, ok := mapping[cp.sessionID]; ok {
		cp.sessionID = newID
	}
	for _, session := range cp.sessions {
		if session.em != nil {
			if newID, ok := mapping[session.em.SessionID]; ok {
				session.em.SessionID = newID
			}
		}
		if newID, ok := mapping[session.familiarID]; ok {
			session.familiarID = newID
		}
	}
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

// Panel returns the warp.Panel wrapping the emulator. Exposed for tests
// that need to inspect the wrapped panel's internal state after Clear
// (e.g. TestClearReplacesPanelEmulator).
func (s *chatSession) Panel() warp.Panel {
	return s.panel
}

// SetEm replaces the emulator reference and updates the wrapped panel.
// Used by clearSessionCmd to point the active chatSession at a freshly
// restarted PTY so the UI no longer renders the stopped emulator.
// Safe when panel is nil (defensive — shouldn't happen in practice).
func (s *chatSession) SetEm(em *portalis.Emulator) {
	s.em = em
	if s.panel != nil {
		if tp, ok := s.panel.(*TermPanel); ok {
			tp.SetEm(em)
		}
	}
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
	id         string
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
		name:       id,
		panel:      panel,
		em:         em,
		familiarID: familiarID,
	})
	// Start the emulator with the extra env (e.g. PI_CODING_AGENT_DIR, PI_OWNER_SESSION).
	return em.StartWithEnv(env)
}

// stopper is implemented by panels that need cleanup when removed.
type stopper interface {
	Stop()
}

// removeSessionAt stops and removes one tab while preserving the active tab
// when a preceding inactive tab is deleted.
func (cp *ChatPanel) removeSessionAt(index int) {
	if index < 0 || index >= len(cp.sessions) {
		return
	}
	if st, ok := cp.sessions[index].panel.(stopper); ok {
		st.Stop()
	}
	cp.sessions = append(cp.sessions[:index], cp.sessions[index+1:]...)
	if index < cp.activeIdx {
		cp.activeIdx--
	}
	if cp.activeIdx >= len(cp.sessions) {
		cp.activeIdx = len(cp.sessions) - 1
	}
	if cp.activeIdx < 0 {
		cp.activeIdx = 0
	}
}

// removeDeadFamiliar removes a familiar tab whose PTY has exited.
// Matches by SessionID (the familiar's own session id, e.g.
// humanhorizon__human-horizon.automata.ai-2__test6) and clears cp.known
// so the next checkFamiliars poll re-creates the tab from familiars.json.
func (cp *ChatPanel) removeDeadFamiliar(sessionID string) bool {
	for i, s := range cp.sessions {
		// Only familiar tabs may be removed here. Main-session PtyExitMsg
		// must not flow through the familiar cleanup path.
		if s == nil || s.familiarID == "" || s.em == nil || s.em.SessionID != sessionID {
			continue
		}
		delete(cp.known, s.name)
		cp.removeSessionAt(i)
		return true
	}
	return false
}

// removeFamiliar removes the tab for a familiar that has been removed.
// openCloseFamiliarModal builds a Warp modal asking the user to confirm
// closing a familiar. The Yes action performs the close; No/Esc/× just
// cancels. Y/N keys are handled in Update alongside the buttons, so both
// keyboard and mouse paths converge on the same outcome.
func (cp *ChatPanel) openCloseFamiliarModal(name string) {
	fid := cp.pendingCloseFamiliar
	cp.closeFamiliarModal = warp.NewModal(
		"Close familiar",
		fmt.Sprintf("Close %q?\nThis kills the PTY and deletes the session JSONL.", name),
		[]warp.ModalButton{
			{Label: "Yes", Action: func() {
				cp.closeFamiliarModal = nil
				cp.pendingCloseFamiliar = ""
				cp.closeFamiliarByID(fid)
			}},
			{Label: "No", Action: func() {
				cp.closeFamiliarModal = nil
				cp.pendingCloseFamiliar = ""
			}},
		},
		func() {
			cp.closeFamiliarModal = nil
			cp.pendingCloseFamiliar = ""
		},
	)
}

// closeFamiliarByID drops the familiar tab from cp.sessions and asks the
// host (main.go via onCloseFamiliar) to clean up the underlying emulator,
// JSONL, and familiars.json entry. The host callback is synchronous (no
// cmd return) — that way it works identically whether the user confirms
// via the Y key (handled in Update) or by clicking the Yes button in the
// Modal overlay (handled in warp.ModalButton.Action, which is a plain
// `func()` and can't surface a cmd).
func (cp *ChatPanel) closeFamiliarByID(familiarID string) {
	var (
		idx = -1
		em  *portalis.Emulator
	)
	for i, s := range cp.sessions {
		if s.familiarID == familiarID {
			idx = i
			em = s.em
			break
		}
	}
	if idx < 0 {
		return
	}
	// Stop the panel synchronously so the terminal stops drawing.
	cp.removeSessionAt(idx)
	if cp.onCloseFamiliar != nil {
		cp.onCloseFamiliar(familiarID, em)
	}
}

func (cp *ChatPanel) removeFamiliar(id string) {
	for i, s := range cp.sessions {
		if s.name == id {
			cp.removeSessionAt(i)
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
	out := cp.joinWithBar(termView, termHeight, width)
	if cp.closeFamiliarModal != nil {
		lines := cp.closeFamiliarModal.Overlay(strings.Split(out, "\n"), width, height)
		out = strings.Join(lines, "\n")
	}
	return out
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

func familiarTabStyle(palette apptheme.Theme, active bool) lipgloss.Style {
	background := palette.Surface
	foreground := palette.TextMuted
	if active {
		background = palette.Raised
		foreground = palette.TextStrong
	}
	return lipgloss.NewStyle().
		Background(lipgloss.Color(background)).
		Foreground(lipgloss.Color(foreground))
}

// renderTabBar renders the tab bar at the bottom. Layout, right-aligned:
//
//	[...tabs...]   <padding>   <clear-btn>
func (cp *ChatPanel) renderTabBar(width int) string {
	if width <= 0 {
		return ""
	}

	const clearBtnText = " × Clear "
	clearW := ansi.StringWidth(clearBtnText)
	palette := cp.palette
	if palette.ID == "" {
		palette = apptheme.Default()
	}
	familiarTabInactiveStyle := familiarTabStyle(palette, false)
	familiarTabActiveStyle := familiarTabStyle(palette, true)
	familiarCloseBtnStyle := lipgloss.NewStyle().Background(lipgloss.Color(palette.Error)).Foreground(lipgloss.Color(palette.TextStrong))

	var tabs []string
	for i, s := range cp.sessions {
		var tab string
		var style lipgloss.Style
		closeBtn := ""
		if s.familiarID != "" {
			// Two-cell close button: " ×" — the label's trailing space
			// provides separation before the close affordance.
			closeBtn = " ×"
			if i == cp.activeIdx {
				style = familiarTabActiveStyle
			} else {
				style = familiarTabInactiveStyle
			}
		} else if i == cp.activeIdx {
			style = lipgloss.NewStyle().
				Background(lipgloss.Color(palette.SelectionBackground)).
				Foreground(lipgloss.Color(palette.SelectionForeground))
		} else {
			style = lipgloss.NewStyle().
				Background(lipgloss.Color(palette.Surface)).
				Foreground(lipgloss.Color(palette.TextMuted))
		}
		tab = style.Render(" " + s.name + " ")
		if closeBtn != "" {
			tab += familiarCloseBtnStyle.Render(closeBtn)
		}
		tabs = append(tabs, tab)
	}

	bar := strings.Join(tabs, " ")

	// Build "× Clear" button.
	clearBtn := lipgloss.NewStyle().
		Background(lipgloss.Color(palette.Surface)).
		Foreground(lipgloss.Color(palette.Error)).
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

	// Familiar-close confirmation modal swallows keys. While pending,
	// Y/N/Esc close/cancel the prompt and nothing else is forwarded to the
	// underlying session. Mouse events are routed to the modal so its
	// buttons and ✕ work too.
	if cp.pendingCloseFamiliar != "" {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.String() {
			case "y", "Y":
				id := cp.pendingCloseFamiliar
				cp.pendingCloseFamiliar = ""
				cp.closeFamiliarModal = nil
				cp.closeFamiliarByID(id) // synchronous cleanup
				return pollCmd
			case "n", "N", "esc":
				cp.pendingCloseFamiliar = ""
				cp.closeFamiliarModal = nil
				return pollCmd
			}
			return pollCmd // swallow other keys while modal is up
		case tea.MouseMsg:
			if cp.closeFamiliarModal != nil && cp.closeFamiliarModal.HandleMouse(msg) {
				return pollCmd
			}
			// An unconsumed click is outside the modal. Dismiss the
			// confirmation without forwarding the event to the tab bar or
			// the active terminal.
			if msg.Action == tea.MouseActionPress {
				cp.pendingCloseFamiliar = ""
				cp.closeFamiliarModal = nil
			}
			return pollCmd
		}
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
		// When a familiar PTY exits, remove it from sessions and clear
		// cp.known so the next checkFamiliars poll can re-spawn it from
		// familiars.json. Main PTY exits stay in Main and are routed to
		// that panel so its emulator can process the lifecycle event.
		if !cp.removeDeadFamiliar(msg.SessionID) {
			forwardCmd = cp.routeBySessionID(msg)
		}

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
		closeW := 0
		if s.familiarID != "" {
			closeW = 2 // " ×"
		}
		fullTabLen := tabLen + closeW
		if int(m.X) >= x && int(m.X) < x+fullTabLen {
			// Familiar × button is the rightmost closeW cells.
			if s.familiarID != "" && int(m.X) >= x+tabLen {
				cp.pendingCloseFamiliar = s.familiarID
				cp.openCloseFamiliarModal(s.name)
				return nil
			}
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
		x += fullTabLen + 1 // +1 for space between tabs
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
