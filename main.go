package main

import (
	"flag"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/pprof"
	"syscall"
	"time"

	"github.com/HumanHorizon/automata/internal/paths"
	"github.com/HumanHorizon/automata/internal/slug"
	"github.com/HumanHorizon/automata/internal/status"
	"github.com/HumanHorizon/automata/internal/tree"
	"github.com/HumanHorizon/automata/internal/ui"
	"github.com/Starframe/portalis"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/starframe-dev/warp"
)

// App is the top-level Bubbletea model for Automata.
type App struct {
	warp      *warp.Warp
	tree      *tree.Tree
	container *ui.Container
	sm        *stateManager
	blink     bool
	blinkT    time.Time

	// activeSessions tracks running session IDs for the stop button.
	activeSessions   map[string]struct{}
	currentSessionID string

	// emulatorCache reuses emulators by session ID so clicking an existing chat
	// resumes the same terminal instead of creating a new one.
	emulatorCache map[string]*portalis.Emulator

	// startEmulatorSync is injectable so Clear can be tested without
	// starting a real pi process.
	startEmulatorSyncFn func(*portalis.Emulator, []string) error

	// mouseEnabled toggles mouse capture. When disabled, mouse events are
	// not captured, allowing text selection in the terminal. Toggle with F8.
	mouseEnabled bool

	// lastKnowledgeRefresh is the time of the last disk read for status/plans/
	// jobs/notes. Refreshes every 2 seconds while the panel is visible.
	lastKnowledgeRefresh time.Time

	// profile is the active profile name; stored on App so background workers
	// (e.g. status polling) know where to look for session data.
	profile string

	// piAgentDir is the path to the pi agent directory (e.g.
	// ~/.ai/just/pi, ~/.ai/getic/pi). Default: ~/.ai/just/pi.
	// Set via --pi flag. The path is exported to spawned pi processes
	// through PI_CODING_AGENT_DIR so the pi extension picks it up.
	piAgentDir string

	// statusReader caches status.json reads by mtime to avoid re-reading
	// unchanged files on every periodic refresh.
	statusReader *status.CachedReader
}

// newApp creates the full application layout.
func newApp(profile, piAgentDir string) (a *App) {
	w := warp.New()

	t := tree.New()
	t.Profile = profile
	t.SetTreeWidth(30)
	t.SetPlanWidth(40)

	em := portalis.NewEmulator("", "", "", nil)
	tp := ui.NewTermPanel(em)

	container := ui.NewContainer(tp)
	container.SetProfile(profile)
	container.SetOnTaskAssigned(func(sessionID, taskTitle string) {
		// Ensure emulator is running for this chat
		if _, ok := a.emulatorCache[sessionID]; !ok {
			em := a.createChatEmulator(sessionID)
			if em == nil {
				return
			}
			a.emulatorCache[sessionID] = em
			a.activeSessions[sessionID] = struct{}{}
			a.tree.SetActiveSessions(a.activeSessions)
			// Start synchronously so the PTY is ready immediately. The env is
			// already recorded on the emulator via SetStartEnv.
			if err := em.StartSync(nil); err != nil {
				return
			}
			// Start listening for output
			if cmd := em.Listen(); cmd != nil {
				_ = cmd
			}
		}
	})

	w.SetTabPosition(warp.TabNone)
	tg := w.Root().(*warp.TabGroup)
	tab := tg.ActiveTab()

	tab.SetRootPanel(t)
	tab.SplitVertical(t, 0.3, container)
	// Collapse symbol "<" on the border at row 0 (header row).
	// When clicked, collapses the left (Tree) panel and broadcasts a
	// ResizeMsg so all leaf panels (chat + knowledge) re-render at their
	// new allocated widths. Without this the inner chat/knowledge split
	// keeps its pre-collapse fraction and the chat stays narrow.
	tab.SetSplitCollapse(t, 0, func() tea.Cmd {
		t.Collapsed = !t.Collapsed
		return func() tea.Msg {
			return tab.BroadcastResize()
		}
	})

	t.LoadState()

	sm := &stateManager{tab: tab}
	tab.SetFocus(t)

	t.SetOnSelectChat(func(item *tree.Item) {
		sessionID := slug.SessionName(item.Path(), item.Name)
		if profile != "" {
			sessionID = slug.Slug(profile) + "__" + sessionID
		}
		a.currentSessionID = sessionID

		// Reuse existing emulator if available.
		em, ok := a.emulatorCache[sessionID]
		if !ok {
			em = a.createChatEmulator(sessionID)
			if em == nil {
				return
			}
			a.emulatorCache[sessionID] = em
		}

		cp := ui.NewChatPanel(em, sessionID, profile)
		cp.SetOnClearSession(func(sid, cwd string) tea.Cmd {
			return a.clearSessionCmd(sid, cwd, cp.FamiliarSessionIDs())
		})
		cp.SetCreateFamiliarEmulator(func(familiarID string) (*portalis.Emulator, []string) {
			return a.createFamiliarEmulator(familiarID)
		})
		container.SetChat(cp, sessionID)
	})

	t.SetOnSelectFolder(func(item *tree.Item) {
		container.SetFolder(item)
		a.updateChatList(item)
	})

	// Profile + cross-parent move handling: when an item is moved between
	// parents (its just-pi session id changes), rename the session directory,
	// invalidate any running emulator, and update the active session set.
	t.SetProfile(profile)
	t.SetOnItemMoved(func(item *tree.Item, oldID, newID string) {
		prefix := ""
		if profile != "" {
			prefix = slug.Slug(profile) + "__"
		}
		fullOld := prefix + oldID
		fullNew := prefix + newID
		if fullOld == fullNew {
			return
		}

		// Stop the running emulator for the old session id (if any). The next
		// time the user clicks the chat we will create a fresh emulator under
		// the new session id.
		if em, ok := a.emulatorCache[fullOld]; ok {
			em.Stop()
			delete(a.emulatorCache, fullOld)
		}

		// Move the on-disk session directory. This is what just-pi and the
		// pi extensions use to store history, plans, jobs, etc.
		oldDir := filepath.Join(a.sessionBaseDir(), fullOld)
		newDir := filepath.Join(a.sessionBaseDir(), fullNew)
		if _, err := os.Stat(oldDir); err == nil {
			if _, err := os.Stat(newDir); err == nil {
				log.Printf("automata: cannot rename session %s -> %s: target already exists", fullOld, fullNew)
			} else if err := os.Rename(oldDir, newDir); err != nil {
				log.Printf("automata: rename session dir %s -> %s: %v", oldDir, newDir, err)
			}
		}

		// Update the active sessions set and the current session id.
		if _, ok := a.activeSessions[fullOld]; ok {
			delete(a.activeSessions, fullOld)
			a.activeSessions[fullNew] = struct{}{}
		}
		if a.currentSessionID == fullOld {
			a.currentSessionID = fullNew
		}
		a.tree.SetActiveSessions(a.activeSessions)
	})

	t.SetOnStopSession(func(item *tree.Item) {
		sessionID := slug.SessionName(item.Path(), item.Name)
		if profile != "" {
			sessionID = slug.Slug(profile) + "__" + sessionID
		}
		if em, ok := a.emulatorCache[sessionID]; ok {
			em.Stop()
		}
		delete(a.activeSessions, sessionID)
		a.tree.SetActiveSessions(a.activeSessions)

		// Kill all running jobs for this session.
		if path, err := exec.LookPath("ai-knowledge"); err == nil {
			exec.Command(path, "kill-session", sessionID).Run()
		}
	})

	container.SetOnPlanWidthChange(func(w int) {
		t.SetPlanWidth(w)
		t.SaveState()
	})

	return &App{
		warp:           w,
		tree:           t,
		container:      container,
		sm:             sm,
		blink:          true,
		mouseEnabled:   true,
		activeSessions: make(map[string]struct{}),
		emulatorCache:  make(map[string]*portalis.Emulator),
		profile:        profile,
		piAgentDir:     piAgentDir,
		statusReader:   status.NewCachedReader(profile),
	}
}

func (a *App) Init() tea.Cmd {
	a.updateChatList(nil)
	return tea.Batch(
		a.warp.Init(),
		a.blinkCmd(),
		a.restoreSessions(),
	)
}

// updateChatList builds the chat list from the tree and passes it to the
// container for the kanban chat picker. If folder is non-nil, only chats
// within that folder (and its children) are included.
func (a *App) updateChatList(folder *tree.Item) {
	if a.tree == nil || a.container == nil {
		return
	}
	var chats []ui.ChatInfo
	items := a.tree.AllItems()
	if folder != nil {
		// Only include children of the folder
		items = folder.Children
	}
	for _, item := range items {
		if item == nil || item.IsFolder || item.IsTerminal {
			continue
		}
		sessionID := a.tree.SessionKeyOf(item)
		chats = append(chats, ui.ChatInfo{
			Name:      item.Name,
			SessionID: sessionID,
		})
	}
	a.container.SetChats(chats)
}

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// PTY output must return to the emulator that started the listen command,
	// even when its chat is no longer part of the visible Warp tree. Otherwise
	// the pull-based Listen chain stops and the PTY output queue eventually
	// blocks the background process.
	if cmd, handled := a.routeCachedEmulatorMessage(msg); handled {
		return a, cmd
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Use fixed tree width to compute split fraction.
		treeW := a.tree.TreeWidth()
		if treeW > 0 && msg.Width > 0 {
			frac := float64(treeW) / float64(msg.Width)
			a.sm.tab.SetSplitFraction(a.tree, frac)
		}
		_, cmd := a.warp.Update(msg)
		return a, cmd

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return a, tea.Quit
		}
		if msg.String() == "f6" {
			a.sm.tab.SetFocus(a.tree)
			return a, nil
		}
		if msg.String() == "f8" {
			a.mouseEnabled = !a.mouseEnabled
			if a.mouseEnabled {
				return a, enableMouse()
			}
			return a, disableMouse()
		}
		_, cmd := a.warp.Update(msg)
		return a, cmd

	case tea.MouseMsg:
		if !a.mouseEnabled {
			// Mouse disabled — skip to allow text selection.
			return a, nil
		}
		_, cmd := a.warp.Update(msg)
		return a, cmd

	case blinkMsg:
		a.blink = !a.blink
		a.blinkT = time.Now()
		now := time.Now()
		if now.Sub(a.lastKnowledgeRefresh) >= 5*time.Second {
			a.refreshTreeStatusBadges(now)
			a.lastKnowledgeRefresh = now
			return a, tea.Batch(a.container.RefreshKnowledgeCmd(), a.blinkCmd())
		}
		return a, a.blinkCmd()
	case ui.KnowledgeRefreshMsg:
		a.container.ApplyKnowledgeRefresh(msg)
		return a, nil

	case tree.TreeCollapsedMsg:
		// Sync the warp node's collapse state when the tree panel
		// is expanded by clicking on the collapsed panel.
		a.sm.tab.ToggleSplitCollapse(a.tree)
		// Broadcast resize so leaf panels (chat + knowledge) get their
		// new allocated widths. Without this the chat terminal keeps the
		// pre-collapse width when the tree is re-expanded.
		var cmds []tea.Cmd
		cmds = append(cmds, func() tea.Msg { return a.sm.tab.BroadcastResize() })
		// Forward to warp so it re-renders with updated collapse state
		// and broadcasts to all panels.
		_, warpCmd := a.warp.Update(msg)
		if warpCmd != nil {
			cmds = append(cmds, warpCmd)
		}
		return a, tea.Batch(cmds...)

	case tree.ItemSelectedMsg:
		if a.container != nil {
			var cmds []tea.Cmd
			active := a.container.Active()
			if tp, ok := active.(*ui.TermPanel); ok && tp != nil {
				cmd := tp.Start()
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
				a.sm.tab.SetFocus(tp)
			} else if cp, ok := active.(*ui.ChatPanel); ok && cp != nil {
				// ChatPanel handles its own emulator lifecycle.
				if em := cp.ActiveEmulator(); em != nil {
					cmd := em.Start()
					if cmd != nil {
						cmds = append(cmds, cmd)
					}
				}
				a.sm.tab.SetFocus(cp)
			}
			// Mark session as active for the stop button.
			if a.currentSessionID != "" {
				a.activeSessions[a.currentSessionID] = struct{}{}
				a.tree.SetActiveSessions(a.activeSessions)
			}
			w, h := a.warp.Width(), a.warp.Height()
			if w > 0 && h > 0 {
				_, warpCmd := a.warp.Update(warp.ResizeMsg{Width: w, Height: h})
				if warpCmd != nil {
					cmds = append(cmds, warpCmd)
				}
			}
			return a, tea.Batch(cmds...)
		}
		_, cmd := a.warp.Update(msg)
		return a, cmd

	case tree.FolderSelectedMsg:
		if a.container != nil {
			a.container.SetFolder(msg.Item)
			w, h := a.warp.Width(), a.warp.Height()
			if w > 0 && h > 0 {
				_, warpCmd := a.warp.Update(warp.ResizeMsg{Width: w, Height: h})
				if warpCmd != nil {
					return a, warpCmd
				}
			}
		}
		return a, nil

	default:
		_, cmd := a.warp.Update(msg)
		return a, cmd
	}
}

func (a *App) View() string {
	// Always render once ready, even with zero dimensions.
	// This ensures TabGroup.View is called and tg.width/height are set.
	return a.warp.View()
}

// restoreSessions re-creates emulators for all chats that were active when
// Automata last shut down. The sessions start in the background; each will
// receive a "continue" message once its PTY reports ready.
func (a *App) restoreSessions() tea.Cmd {
	if a.tree == nil {
		return nil
	}
	ids := a.tree.ActiveSessionIDs()
	if len(ids) == 0 {
		return nil
	}

	var cmds []tea.Cmd
	for _, sessionID := range ids {
		if _, ok := a.emulatorCache[sessionID]; ok {
			continue
		}
		if a.currentSessionID == sessionID {
			// The current session is already open; don't duplicate it.
			continue
		}

		em := a.createChatEmulator(sessionID)
		if em == nil {
			continue
		}

		a.emulatorCache[sessionID] = em
		a.activeSessions[sessionID] = struct{}{}
		if cmd := em.StartWithEnv(nil); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	a.tree.SetActiveSessions(a.activeSessions)
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// piLaunch resolves how to spawn a pi session: binary, arguments and extra
// environment. It is the single place deciding PI_CODING_AGENT_DIR and
// AUTOMATA_PROFILE, so every launch path (tree click, session restore,
// clear-restart, task assign, familiar) produces an identical result.
//
// Priority:
//  1. PI_CMD env override (used by e2e tests).
//  2. a.piAgentDir (set via --pi, default ~/.ai/<tag>/pi) — direct
//     /usr/local/bin/pi with PI_CODING_AGENT_DIR exported.
//  3. just-pi wrapper found on PATH (only when --pi "" was passed
//     explicitly) — the wrapper exports PI_CODING_AGENT_DIR itself.
//
// Returns cmd == "" when nothing is available; callers must NOT fall back
// to a shell for pi sessions.
func (a *App) piLaunch(sessionID string) (cmd string, args []string, env []string) {
	if a.profile != "" {
		env = append(env, "AUTOMATA_PROFILE="+slug.Slug(a.profile))
	}
	if piCmd := os.Getenv("PI_CMD"); piCmd != "" {
		return piCmd, nil, env
	}
	if a.piAgentDir != "" {
		env = append(env, "PI_CODING_AGENT_DIR="+a.piAgentDir)
		return "/usr/local/bin/pi", []string{"--session-id", sessionID}, env
	}
	if path, err := exec.LookPath("just-pi"); err == nil {
		return path, []string{"--session-id", sessionID}, env
	}
	return "", nil, nil
}

// createChatEmulator builds an emulator for a chat session, using the item's
// effective bound path as the initial CWD when available. Terminal items get
// a plain shell; pi sessions get the binary/env from piLaunch. The env is
// recorded via SetStartEnv so any later Start variant keeps it. Returns nil
// when no pi command is available.
func (a *App) createChatEmulator(sessionID string) *portalis.Emulator {
	item := a.tree.FindItemBySessionID(sessionID)

	var cmd string
	var args, env []string
	if item != nil && item.IsTerminal {
		// Terminals run a plain shell, not pi.
		cmd, args = portalis.DefaultShell()
	} else {
		cmd, args, env = a.piLaunch(sessionID)
		if cmd == "" {
			log.Printf("automata: no pi command for session %q (piAgentDir=%q, PI_CMD unset, just-pi not on PATH)", sessionID, a.piAgentDir)
			return nil
		}
	}

	name := sessionID
	if item != nil {
		name = item.Name
	}
	em := portalis.NewEmulator(sessionID, name, cmd, args)
	em.SetStartEnv(env)
	em.SetScrollbackLimit(1000)

	if item != nil {
		initialCWD := item.CWD
		if bound := item.EffectiveBoundPath(); bound != "" {
			if info, err := os.Stat(bound); err == nil && info.IsDir() {
				initialCWD = bound
			}
		}
		if initialCWD != "" {
			em.SetInitialCWD(initialCWD)
		}
		if len(item.CommandHistory) > 0 {
			em.SetCommandHistory(item.CommandHistory)
		}
		em.OnCWDChange = func(path string) {
			item.CWD = path
			a.tree.SaveState()
		}
		em.OnCommandHistoryChanged = func(history []string) {
			item.CommandHistory = history
			a.tree.SaveState()
		}
	}

	return em
}

// createFamiliarEmulator builds an emulator for a familiar tab.
// Uses the same pi command resolution as createChatEmulator but without
// tree item lookup (familiars don't have tree items).
// Returns the emulator and its launch env for StartWithEnv (ChatPanel
// appends PI_OWNER_SESSION before starting).
func (a *App) createFamiliarEmulator(sessionID string) (*portalis.Emulator, []string) {
	cmd, args, env := a.piLaunch(sessionID)
	if cmd == "" {
		log.Printf("automata: no pi command for familiar %q (piAgentDir=%q, PI_CMD unset, just-pi not on PATH)", sessionID, a.piAgentDir)
		return nil, nil
	}

	em := portalis.NewEmulator(sessionID, sessionID, cmd, args)
	em.SetStartEnv(env)
	em.SetScrollbackLimit(1000)
	return em, env
}

// spawnParallelChatEmulator builds a fresh pi-agent for the parallel-chat
// split. The agent is launched with `--no-session` (ephemeral, never
// persisted) and receives the lower chat's session file path as its first
// user message through stdin. The agent can then read status.json / plans /
// the .jsonl transcript of the main session and answer progress questions
// without disturbing the chat below.
//
// Returns (nil, nil) if just-pi is not on PATH and PI_CMD is empty —
// in that case the ChatPanel keeps its placeholder panel so the user still sees
// something instead of a silent broken split. Otherwise it returns the
// TermPanel plus the tea.Cmd returned by em.Listen(), which MUST be
// chained through bubbletea's command pipeline — without it just-pi's
// PTY output never reaches the screen and the panel renders blank.
// something instead of a silent broken split.
func (a *App) routeCachedEmulatorMessage(msg tea.Msg) (tea.Cmd, bool) {
	sessionID, ok := ptyMessageSessionID(msg)
	if !ok {
		return nil, false
	}

	em, ok := a.emulatorCache[sessionID]
	if !ok || em == nil {
		// Unknown session IDs fall through to Warp for regular routing.
		return nil, false
	}

	return em.Update(msg), true
}

func ptyMessageSessionID(msg tea.Msg) (string, bool) {
	switch msg := msg.(type) {
	case portalis.PtyReadyMsg:
		return msg.SessionID, true
	case portalis.PtyOutputMsg:
		return msg.SessionID, true
	case portalis.RenderTickMsg:
		return msg.SessionID, true
	case portalis.PtyExitMsg:
		return msg.SessionID, true
	default:
		return "", false
	}
}

func (a *App) startEmulatorSync(em *portalis.Emulator, env []string) error {
	if a.startEmulatorSyncFn != nil {
		return a.startEmulatorSyncFn(em, env)
	}
	return em.StartSync(env)
}

func (a *App) clearSessionCmd(sessionID, cwd string, familiarSIDs []string) tea.Cmd {
	return func() tea.Msg {
		// 0. Kill this session's familiars (stop emulators + delete their JSONL).
		// Order: stop emulators first, then delete JSONL, then clear familiars.json
		// so that a still-running familiar cannot rewrite familiars.json after we
		// cleared it.
		for _, sid := range familiarSIDs {
			if em, ok := a.emulatorCache[sid]; ok {
				em.Stop()
				delete(a.emulatorCache, sid)
			}
			delete(a.activeSessions, sid)
		}
		for _, sid := range familiarSIDs {
			if deleted, err := paths.DeleteSessionJSONL(sid, cwd); err != nil {
				log.Printf("clearSession: familiar %q: %v", sid, err)
			} else if deleted != "" {
				log.Printf("clearSession: familiar %q deleted %s", sid, deleted)
			}
		}
		if err := paths.ClearFamiliarsJSONL(a.profile, sessionID); err != nil {
			log.Printf("clearSession: clear familiars.json: %v", err)
		}

		// 1. Stop and dispose the running emulator
		if em, ok := a.emulatorCache[sessionID]; ok {
			em.Stop()
			delete(a.emulatorCache, sessionID)
		}
		delete(a.activeSessions, sessionID)

		// 2. Delete the .jsonl file for this session
		deleted, err := paths.DeleteSessionJSONL(sessionID, cwd)
		if err != nil {
			log.Printf("clearSession: %v", err)
		} else {
			log.Printf("clearSession: deleted %s", deleted)
		}

		// 3. Recreate the emulator with the same session id so pi starts fresh
		newEm := a.createChatEmulator(sessionID)
		if newEm != nil {
			a.emulatorCache[sessionID] = newEm
			a.activeSessions[sessionID] = struct{}{}
			a.tree.SetActiveSessions(a.activeSessions)
			if err := a.startEmulatorSync(newEm, nil); err != nil {
				log.Printf("clearSession: restart %q: %v", sessionID, err)
				return nil
			}
			return portalis.PtyReadyMsg{SessionID: sessionID}
		}
		return nil
	}
}

// refreshTreeStatusBadges walks every chat item, reads its on-disk status.json
// (cheap: a single tiny file) and pushes an emoji-per-session map to the Tree
// so that each chat row can show a 🧠/📖/✏️/🔍/⚙️/💤 indicator next to its
// name. Best-effort: missing files are treated as idle (no badge).
func (a *App) refreshTreeStatusBadges(_ time.Time) {
	if a.tree == nil {
		return
	}
	badges := map[string]string{}
	for _, it := range a.tree.AllItems() {
		if it == nil || it.IsFolder || it.IsTerminal {
			continue
		}
		key := a.tree.SessionKeyOf(it)
		if key == "" {
			continue
		}
		action := a.statusReader.Read(key)
		if emoji := status.Emoji(action); emoji != "" {
			badges[key] = emoji
		}
	}
	a.tree.SetStatusBadges(badges)
}

type blinkMsg struct{}

func (a *App) blinkCmd() tea.Cmd {
	return tea.Tick(1*time.Second, func(t time.Time) tea.Msg {
		return blinkMsg{}
	})
}

type stateManager struct {
	tab *warp.Tab
}

// sessionBaseDir returns the directory under which just-pi stores one
// subdirectory per session id. Format: ~/.ai/automata/profiles/<slug>/sessions/<id>
// or ~/.ai/automata/sessions/<id> when no profile is set.
func (a *App) sessionBaseDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "automata", "sessions")
	}
	base := filepath.Join(home, ".ai", "automata")
	if a.profile != "" {
		base = filepath.Join(base, "profiles", slug.Slug(a.profile))
	}
	return filepath.Join(base, "sessions")
}

// enableMouse returns a command that enables mouse tracking in the terminal.
// Mode 1000 = click, 1002 = cell-motion (only while button held).
// We do NOT enable 1003 (all-motion) — it floods the update loop with one
// msg per cursor pixel, easily spinning CPU to 100% just from a hover.
func enableMouse() tea.Cmd {
	return func() tea.Msg {
		os.Stdout.Write([]byte("\x1b[?1000h\x1b[?1002h\x1b[?1006h"))
		return nil
	}
}

// disableMouse returns a command that disables mouse tracking in the terminal.
func disableMouse() tea.Cmd {
	return func() tea.Msg {
		os.Stdout.Write([]byte("\x1b[?1000l\x1b[?1002l\x1b[?1006l"))
		return nil
	}
}

func main() {
	profile := flag.String("profile", "", "profile name for state isolation")
	piTag := flag.String("pi", "just", "pi agent tag (e.g. 'just', 'getic', 'magic') — sets PI_CODING_AGENT_DIR to ~/.ai/<tag>/pi")
	debugLog := flag.String("debug-log", "", "path to write familiar debug log (default: /tmp/automata-familiar.log)")
	cpuProfile := flag.String("cpuprofile", "", "path to write CPU profile (e.g. /tmp/automata-cpu.prof)")
	flag.Parse()

	piAgentDir := ""
	if *piTag != "" {
		home, _ := os.UserHomeDir()
		if home == "" {
			home = "/Users/a"
		}
		piAgentDir = filepath.Join(home, ".ai", *piTag, "pi")
	}

	if *cpuProfile != "" {
		f, err := os.Create(*cpuProfile)
		if err != nil {
			log.Fatalf("cannot create CPU profile: %v", err)
		}
		defer f.Close()
		if err := pprof.StartCPUProfile(f); err != nil {
			log.Fatalf("cannot start CPU profile: %v", err)
		}
		defer pprof.StopCPUProfile()
	}

	// Configure log output. Default is /tmp/automata-familiar.log so we
	// keep familiar diagnostics even when cuetty's screen buffer scrolls.
	logPath := *debugLog
	if logPath == "" {
		logPath = filepath.Join(os.TempDir(), "automata-familiar.log")
	}
	if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND|os.O_TRUNC, 0644); err == nil {
		log.SetOutput(f)
		log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	}
	log.Printf("[main] START pid=%d profile=%q", os.Getpid(), *profile)

	app := newApp(*profile, piAgentDir)
	// All-motion (mode 1003) is required for hover effects — it fires a
	// motion event for every cursor pixel even with no button held. The
	// flood is tamed by time-based throttling in Tree.handleMouseMotion
	// (~30 FPS), so the update loop stays light while hover icons still
	// appear promptly.
	p := tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseAllMotion())
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		p.Quit()
	}()
	if _, err := p.Run(); err != nil {
		log.Printf("[main] p.Run err: %v", err)
		log.Fatal(err)
	}
}
