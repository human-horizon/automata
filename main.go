package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"time"

	akjobs "github.com/HumanHorizon/automata/internal/ai-knowledge/jobs"
	"github.com/HumanHorizon/automata/internal/paths"
	"github.com/HumanHorizon/automata/internal/slug"
	"github.com/HumanHorizon/automata/internal/status"
	"github.com/HumanHorizon/automata/internal/tree"
	"github.com/HumanHorizon/automata/internal/ui"
	"github.com/Starframe/portalis"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/fsnotify/fsnotify"
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

	// emulatorCache reuses tree chat/terminal emulators by session ID so
	// clicking an existing item resumes the same terminal instead of creating
	// a new one.
	emulatorCache map[string]*portalis.Emulator

	// familiarEmulatorCache keeps references to familiar emulators that live
	// inside ChatPanel tabs. Familiars are not part of Tree.ActiveSessions, but
	// they still must be stopped before their owner session is migrated.
	familiarEmulatorCache map[string]*portalis.Emulator

	// runningSessions records emulators that reached PtyReady. It lets a
	// failed tree move restore real PTYs without starting test-only cached
	// emulator values that were never running.
	runningSessions map[string]struct{}

	// startEmulatorSync is injectable so Clear can be tested without
	// starting a real pi process.
	startEmulatorSyncFn func(*portalis.Emulator, []string) error

	// mouseEnabled toggles mouse capture. When disabled, mouse events are
	// not captured, allowing text selection in the terminal. Toggle with F8.
	mouseEnabled bool

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

	// statusWatcher observes sessionBaseDir() for newly created/removed
	// session subdirectories so we can attach/detach per-session watchers.
	statusWatcher *fsnotify.Watcher

	// sessionWatchers maps sessionID -> fsnotify.Watcher on that session's
	// directory. status.json changes inside a session fire here and trigger
	// a Tree badge refresh. Mirrored against the set of visible chat items
	// from the Tree so we never leak descriptors for archived/hidden chats.
	sessionWatchers map[string]*fsnotify.Watcher

	// statusWatchPending guards against stacking blocking watch cmds while
	// one is already in flight. Reset on treeStatusChangedMsg, set on
	// re-arm.
	statusWatchPending bool

	// sessionWatchPending guarantees one blocking command per session watcher.
	sessionWatchPending map[string]bool

	// movePlans stores external migrations between the pre-move and post-move
	// tree callbacks. The plan is consumed after the state snapshot succeeds.
	pendingMovePlans map[*tree.Item]*renamePlan

	// pendingRuntimeCmds carries PTY restart/listen commands from a move
	// rollback into Bubble Tea's command pipeline.
	pendingRuntimeCmds []tea.Cmd

	// Root overlays are rendered by App.View so Help and Settings cover the
	// complete Automata viewport instead of only the Tree panel.
	appModal   *warp.Modal
	appPopover *warp.Popover
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
	container.SetOnTaskAssigned(func(sessionID, taskTitle string) tea.Cmd {
		// Ensure the emulator is running for this chat. The returned Listen
		// command must reach Bubble Tea; dropping it leaves the PTY silent.
		if _, ok := a.emulatorCache[sessionID]; ok {
			return nil
		}
		em := a.createChatEmulator(sessionID)
		if em == nil {
			return nil
		}
		// Start synchronously so the PTY is ready immediately. The env is
		// already recorded on the emulator via SetStartEnv.
		if err := em.StartSync(nil); err != nil {
			return nil
		}
		a.emulatorCache[sessionID] = em
		if a.runningSessions == nil {
			a.runningSessions = make(map[string]struct{})
		}
		a.runningSessions[sessionID] = struct{}{}
		a.activeSessions[sessionID] = struct{}{}
		a.tree.SetActiveSessions(a.activeSessions)
		return em.Listen()
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
		cp.SetOnCloseFamiliar(func(familiarID string, em *portalis.Emulator) {
			a.closeFamiliar(familiarID, em)
		})
		cp.SetCreateFamiliarEmulator(func(familiarID string) (*portalis.Emulator, []string) {
			return a.createFamiliarEmulator(familiarID)
		})
		container.SetChat(cp, sessionID)
		container.SetChatDomain(item.Domain(profile))
	})

	t.SetOnSelectFolder(func(item *tree.Item) {
		container.SetFolder(item)
		a.updateChatList(item)
	})

	// Profile + cross-parent move handling reuses the complete rename
	// migration. External data is moved before the tree mutates; the returned
	// rollback is used if the atomic tree snapshot cannot be committed.
	t.SetProfile(profile)
	t.SetOnBeforeItemMoved(func(item, newParent *tree.Item) (func() error, error) {
		plan, err := buildMovePlan(item, newParent, a.profile)
		if err != nil {
			return nil, err
		}
		rollback, err := a.applyRenamePlan(plan)
		if err != nil {
			return nil, err
		}
		if a.pendingMovePlans == nil {
			a.pendingMovePlans = make(map[*tree.Item]*renamePlan)
		}
		a.pendingMovePlans[item] = plan
		return func() error {
			delete(a.pendingMovePlans, item)
			return rollback()
		}, nil
	})
	t.SetOnItemMoved(func(item *tree.Item, _, _ string) {
		plan := a.pendingMovePlans[item]
		delete(a.pendingMovePlans, item)
		if plan != nil {
			a.applyRenameMappings(plan)
		}
	})

	t.SetOnRename(func(item *tree.Item, newName string) error {
		return a.renameTreeItem(item, newName)
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

	t.SetOnBeforeDelete(func(item *tree.Item) error {
		return a.cleanupDeletedTreeItem(item)
	})

	container.SetOnPlanWidthChange(func(w int) {
		t.SetPlanWidth(w)
		t.SaveState()
	})

	a = &App{
		warp:                  w,
		tree:                  t,
		container:             container,
		sm:                    sm,
		blink:                 true,
		mouseEnabled:          true,
		activeSessions:        make(map[string]struct{}),
		emulatorCache:         make(map[string]*portalis.Emulator),
		familiarEmulatorCache: make(map[string]*portalis.Emulator),
		runningSessions:       make(map[string]struct{}),
		profile:               profile,
		piAgentDir:            piAgentDir,
		statusReader:          status.NewCachedReader(profile),
		sessionWatchers:       make(map[string]*fsnotify.Watcher),
		sessionWatchPending:   make(map[string]bool),
		pendingMovePlans:      make(map[*tree.Item]*renamePlan),
	}
	t.SetOnOpenHelp(a.openHelpOverlay)
	t.SetOnOpenSettings(a.openSettingsOverlay)
	t.SetOnThemeChange(a.applyTheme)
	a.applyTheme(t.ThemeID())
	return a
}

func (a *App) Init() tea.Cmd {
	a.updateChatList(nil)
	// Mount the status watcher synchronously so the first recomputeTreeStatusBadges
	// below can rely on the watcher already being primed. Any failure here is
	// non-fatal: we just won't get reactive badges and the Tree will keep
	// showing its previous values until the user restarts.
	a.setupStatusWatcher()
	a.recomputeTreeStatusBadges()
	sessionCmds := a.syncSessionWatchers()
	allCmds := append([]tea.Cmd(nil), sessionCmds...)
	allCmds = append(allCmds,
		a.warp.Init(),
		a.blinkCmd(),
		a.restoreSessions(),
		a.watchTreeStatusCmd(),
	)
	return tea.Batch(allCmds...)
}

// updateChatList builds the chat list from the tree and passes it to the
// container for the kanban chat picker. If folder is non-nil, only chats
// within that folder (and its children) are included.
func (a *App) updateChatList(folder *tree.Item) {
	if a.tree == nil || a.container == nil {
		return
	}
	var chats []ui.ChatInfo
	var collect func([]*tree.Item)
	collect = func(items []*tree.Item) {
		for _, item := range items {
			if item == nil {
				continue
			}
			if item.IsFolder {
				collect(item.Children)
				continue
			}
			if item.IsTerminal {
				continue
			}
			chats = append(chats, ui.ChatInfo{
				Name:      item.Name,
				SessionID: a.tree.SessionKeyOf(item),
			})
		}
	}
	if folder != nil {
		collect(folder.Children)
	} else {
		collect(a.tree.Root())
	}
	a.container.SetChats(chats)
}

func (a *App) Update(msg tea.Msg) (model tea.Model, command tea.Cmd) {
	defer func() {
		if len(a.pendingRuntimeCmds) == 0 {
			return
		}
		pending := append([]tea.Cmd(nil), a.pendingRuntimeCmds...)
		a.pendingRuntimeCmds = nil
		commands := make([]tea.Cmd, 0, len(pending)+1)
		commands = append(commands, pending...)
		if command != nil {
			commands = append(commands, command)
		}
		command = tea.Batch(commands...)
	}()

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
		// Ctrl+C is the application exit shortcut. Handle it before the focus
		// router so chat, terminal, and overlays cannot consume it.
		if msg.Type == tea.KeyCtrlC || msg.String() == "ctrl+c" {
			return a, tea.Quit
		}
		if a.appModal != nil {
			switch msg.Type {
			case tea.KeyEsc, tea.KeyEnter, tea.KeyF1:
				a.closeAppOverlay()
			}
			return a, nil
		}
		if a.appPopover != nil {
			if msg.Type == tea.KeyF1 {
				a.closeAppOverlay()
				return a, nil
			}
			if a.appPopover.HandleKey(msg) {
				return a, nil
			}
			return a, nil
		}
		if (msg.Type == tea.KeyF1 || msg.String() == "f1") && a.tree != nil {
			a.openHelpOverlay()
			return a, nil
		}
		if msg.Type == tea.KeyF6 {
			a.setFocusArea(focusTree)
			return a, nil
		}
		if (msg.Type == tea.KeyF5 || msg.Type == tea.KeyF7) &&
			(a.tree == nil || !a.tree.IsModalOpen()) {
			if msg.Type == tea.KeyF5 {
				a.cycleFocus(-1)
			} else {
				a.cycleFocus(1)
			}
			return a, nil
		}
		if msg.String() == "f8" {
			a.mouseEnabled = !a.mouseEnabled
			if a.mouseEnabled {
				return a, enableMouse()
			}
			return a, disableMouse()
		}
		if a.currentFocusArea() != focusTree && a.container != nil {
			return a, a.container.Update(msg)
		}
		_, cmd := a.warp.Update(msg)
		return a, cmd

	case tea.MouseMsg:
		if a.appModal != nil {
			a.appModal.EnsureDimensions(a.warp.Width(), a.warp.Height())
			a.appModal.HandleMouse(msg)
			return a, nil
		}
		if a.appPopover != nil {
			a.appPopover.HandleMouse(msg)
			return a, nil
		}
		if !a.mouseEnabled {
			// Mouse disabled — skip to allow text selection.
			return a, nil
		}
		_, cmd := a.warp.Update(msg)
		return a, cmd

	case blinkMsg:
		a.blink = !a.blink
		a.blinkT = time.Now()
		return a, a.blinkCmd()
	case ui.KnowledgeRefreshMsg:
		a.container.ApplyKnowledgeRefresh(msg)
		return a, nil

	case statusWatcherClosedMsg:
		// A closed watcher must never be re-armed.
		a.statusWatchPending = false
		a.statusWatcher = nil
		return a, nil

	case sessionWatcherClosedMsg:
		if w, ok := a.sessionWatchers[msg.sessionID]; ok {
			_ = w.Close()
			delete(a.sessionWatchers, msg.sessionID)
		}
		delete(a.sessionWatchPending, msg.sessionID)
		return a, nil

	case treeStatusChangedMsg:
		// The status watcher fired — either sessionBaseDir() saw a new
		// session directory appear or one per-session watcher saw status.json
		// change. Only the command that delivered this event becomes free;
		// re-arming every other watcher would create a second reader.
		a.statusWatchPending = false
		if msg.sessionID != "" {
			a.sessionWatchPending[msg.sessionID] = false
		}
		newCmds := a.syncSessionWatchers()
		a.recomputeTreeStatusBadges()
		allCmds := append([]tea.Cmd(nil), newCmds...)
		allCmds = append(allCmds, a.rearmSessionWatchers()...)
		if cmd := a.watchTreeStatusCmd(); cmd != nil {
			allCmds = append(allCmds, cmd)
		}
		if len(allCmds) > 0 {
			return a, tea.Batch(allCmds...)
		}
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
				a.container.SetFocus(cp)
				a.sm.tab.SetFocus(a.container)
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
	base := a.warp.View()
	return a.renderAppOverlay(base)
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

	// Persist only sessions that reach PtyReadyMsg. A failed restore must not
	// leave stale IDs marked active in state.json.
	a.activeSessions = make(map[string]struct{})
	a.tree.SetActiveSessions(a.activeSessions)

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
		if cmd := em.StartWithEnv(nil); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
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
//  1. PI_CMD env override, resolved as one executable without shell parsing.
//  2. pi found on PATH.
//  3. just-pi wrapper found on PATH for installations that expose the wrapper.
//
// Returns cmd == "" when nothing is available; callers must NOT fall back
// to a shell for pi sessions.
func (a *App) piLaunch(sessionID string) (cmd string, args []string, env []string) {
	if a.profile != "" {
		env = append(env, "AUTOMATA_PROFILE="+slug.Slug(a.profile))
	}
	if configured := strings.TrimSpace(os.Getenv("PI_CMD")); configured != "" {
		path, err := exec.LookPath(configured)
		if err != nil {
			return "", nil, nil
		}
		return path, []string{"--session-id", sessionID}, env
	}
	if a.piAgentDir != "" {
		env = append(env, "PI_CODING_AGENT_DIR="+a.piAgentDir)
	}
	for _, candidate := range []string{"pi", "just-pi"} {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, []string{"--session-id", sessionID}, env
		}
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
	if a.familiarEmulatorCache == nil {
		a.familiarEmulatorCache = make(map[string]*portalis.Emulator)
	}
	if em, ok := a.familiarEmulatorCache[sessionID]; ok && em != nil {
		return em, em.StartEnv()
	}

	cmd, args, env := a.piLaunch(sessionID)
	if cmd == "" {
		log.Printf("automata: no pi command for familiar %q (piAgentDir=%q, PI_CMD unset, just-pi not on PATH)", sessionID, a.piAgentDir)
		return nil, nil
	}

	em := portalis.NewEmulator(sessionID, sessionID, cmd, args)
	em.SetStartEnv(env)
	em.SetScrollbackLimit(1000)
	a.familiarEmulatorCache[sessionID] = em
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

	if _, isExit := msg.(portalis.PtyExitMsg); isExit {
		if _, isFamiliar := a.familiarEmulatorCache[sessionID]; isFamiliar {
			delete(a.familiarEmulatorCache, sessionID)
			delete(a.runningSessions, sessionID)
			delete(a.activeSessions, sessionID)
			if a.tree != nil {
				a.tree.SetActiveSessions(a.activeSessions)
			}
			// Let the active ChatPanel remove the dead tab and keep polling.
			return nil, false
		}
		if _, isCached := a.emulatorCache[sessionID]; isCached {
			delete(a.emulatorCache, sessionID)
			delete(a.runningSessions, sessionID)
			delete(a.activeSessions, sessionID)
			if a.tree != nil {
				a.tree.SetActiveSessions(a.activeSessions)
			}
			// The active panel still owns the exit message and can render its
			// stopped state; the cache must not retain the dead emulator.
			return nil, false
		}
	}

	em, ok := a.emulatorCache[sessionID]
	isFamiliar := false
	if !ok || em == nil {
		em, ok = a.familiarEmulatorCache[sessionID]
		isFamiliar = ok
	}
	if !ok || em == nil {
		// Unknown session IDs fall through to Warp for regular routing.
		return nil, false
	}

	if _, isReady := msg.(portalis.PtyReadyMsg); isReady {
		if a.runningSessions == nil {
			a.runningSessions = make(map[string]struct{})
		}
		a.runningSessions[sessionID] = struct{}{}
	}
	if _, isReady := msg.(portalis.PtyReadyMsg); isReady && !isFamiliar {
		if a.activeSessions == nil {
			a.activeSessions = make(map[string]struct{})
		}
		if _, alreadyActive := a.activeSessions[sessionID]; !alreadyActive {
			a.activeSessions[sessionID] = struct{}{}
			if a.tree != nil {
				a.tree.SetActiveSessions(a.activeSessions)
			}
		}
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
		// Fail-safe: without a known pi agent, refuse to delete any JSONL.
		// One profile must never delete another profile's sessions.
		agentDir := a.piAgentDir
		if agentDir == "" {
			log.Printf("clearSession: refuse to clear %q without piAgentDir", sessionID)
			return nil
		}

		// 0. Kill this session's familiars (stop emulators + delete their JSONL).
		// Order: stop emulators first, then delete JSONL, then clear familiars.json
		// so that a still-running familiar cannot rewrite familiars.json after we
		// cleared it.
		for _, sid := range familiarSIDs {
			if em, ok := a.emulatorCache[sid]; ok {
				em.Stop()
				delete(a.emulatorCache, sid)
			}
			if em, ok := a.familiarEmulatorCache[sid]; ok {
				em.Stop()
				delete(a.familiarEmulatorCache, sid)
			}
			delete(a.activeSessions, sid)
		}
		for _, sid := range familiarSIDs {
			if deleted, err := paths.DeleteSessionJSONL(sid, cwd, agentDir); err != nil {
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

		// 2. Delete the .jsonl file for this session (scoped to agentDir)
		deleted, err := paths.DeleteSessionJSONL(sessionID, cwd, agentDir)
		if err != nil {
			log.Printf("clearSession: %v", err)
		} else {
			log.Printf("clearSession: deleted %s", deleted)
		}

		// 3. Recreate the emulator with the same session id so pi starts fresh
		newEm := a.createChatEmulator(sessionID)
		if newEm != nil {
			if err := a.startEmulatorSync(newEm, nil); err != nil {
				newEm.Stop()
				log.Printf("clearSession: restart %q: %v", sessionID, err)
				return nil
			}
			a.emulatorCache[sessionID] = newEm
			// 4. Point the active ChatPanel's chatSession at the new emulator.
			// Without this, the UI keeps rendering the stopped (old) emulator
			// and the user sees an empty screen instead of a fresh pi prompt.
			if a.container != nil {
				if panel := a.container.Active(); panel != nil {
					if cp, ok := panel.(*ui.ChatPanel); ok {
						for _, s := range cp.Sessions() {
							if s.Em() != nil && s.Em().SessionID == sessionID {
								s.SetEm(newEm)
								break
							}
						}
					}
				}
			}
			return portalis.PtyReadyMsg{SessionID: sessionID}
		}
		return nil
	}
}

func deletedSessionIDs(t *tree.Tree, item *tree.Item) map[string]struct{} {
	ids := make(map[string]struct{})
	var visit func(*tree.Item)
	visit = func(current *tree.Item) {
		if current == nil {
			return
		}
		if !current.IsFolder {
			ids[t.SessionKeyOf(current)] = struct{}{}
		}
		for _, child := range current.Children {
			visit(child)
		}
	}
	visit(item)
	return ids
}

// cleanupDeletedTreeItem stops every runtime owned by a deleted chat or folder
// subtree before the tree state is persisted. Any failure aborts deletion so
// the tree cannot claim a runtime that was not safely cleaned up.
func (a *App) cleanupDeletedTreeItem(item *tree.Item) error {
	if a.tree == nil || item == nil {
		return nil
	}
	ids := deletedSessionIDs(a.tree, item)
	var firstErr error
	for ownerID := range ids {
		if em, ok := a.emulatorCache[ownerID]; ok {
			if em != nil {
				em.Stop()
			}
			delete(a.emulatorCache, ownerID)
		}
		if w, ok := a.sessionWatchers[ownerID]; ok {
			_ = w.Close()
			delete(a.sessionWatchers, ownerID)
		}
		delete(a.sessionWatchPending, ownerID)
		if err := akjobs.KillSessionForProfile(a.profile, ownerID); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("stop jobs for %s: %w", ownerID, err)
		}
		for sessionID := range a.activeSessions {
			if sessionID == ownerID || strings.HasPrefix(sessionID, ownerID+"__") {
				delete(a.activeSessions, sessionID)
			}
		}
		for sessionID, em := range a.familiarEmulatorCache {
			if sessionID != ownerID && !strings.HasPrefix(sessionID, ownerID+"__") {
				continue
			}
			if em != nil {
				em.Stop()
			}
			delete(a.familiarEmulatorCache, sessionID)
		}
	}
	if a.container != nil {
		if panel := a.container.Active(); panel != nil {
			if cp, ok := panel.(*ui.ChatPanel); ok {
				for _, session := range cp.Sessions() {
					em := session.Em()
					if em == nil {
						continue
					}
					for ownerID := range ids {
						if em.SessionID == ownerID || strings.HasPrefix(em.SessionID, ownerID+"__") {
							em.Stop()
							break
						}
					}
				}
			}
		}
	}
	if a.currentSessionID != "" {
		for ownerID := range ids {
			if a.currentSessionID == ownerID || strings.HasPrefix(a.currentSessionID, ownerID+"__") {
				a.currentSessionID = ""
				break
			}
		}
	}
	a.tree.SetActiveSessions(a.activeSessions)
	return firstErr
}

// closeFamiliarCmd cleans up after the user confirms closing a familiar:
// stops the emulator (idempotent), removes the JSONL, and strips the
// entry from familiars.json. ChatPanel has already dropped the tab by
// the time this fires, so we only deal with host-side state.
func (a *App) closeFamiliar(familiarID string, em *portalis.Emulator) {
	log.Printf("closeFamiliar: start familiarID=%q em=%v profile=%q activeChat=%q",
		familiarID, em != nil, a.profile, a.activeChatSessionID())
	// 1. Stop the emulator. ChatPanel may have already stopped it,
	// but Stop is safe to call twice.
	if em != nil {
		em.Stop()
	}
	delete(a.emulatorCache, familiarID)
	delete(a.familiarEmulatorCache, familiarID)
	delete(a.activeSessions, familiarID)
	if a.tree != nil {
		a.tree.SetActiveSessions(a.activeSessions)
	}

	// 2. Drop the familiar's JSONL. If it doesn't exist (or pi never
	// wrote one), we log but don't fail the close.
	if em != nil {
		cwd := em.CWD()
		if _, err := paths.DeleteSessionJSONL(familiarID, cwd, a.piAgentDir); err != nil {
			log.Printf("closeFamiliar: delete JSONL %q: %v", familiarID, err)
		}
	}

	// 3. Strip the entry from familiars.json so the next poll doesn't
	// resurrect it.
	if err := paths.RemoveFamiliar(a.profile, a.activeChatSessionID(), familiarID); err != nil {
		log.Printf("closeFamiliar: remove from familiars.json: %v", err)
	}

	log.Printf("closeFamiliar: done familiarID=%q", familiarID)
}

// activeChatSessionID returns the session id of whichever chat is
// currently focused in the right pane. We need it to know which
// familiars.json to update when a familiar is closed.
func (a *App) activeChatSessionID() string {
	if a.container == nil {
		return ""
	}
	if panel := a.container.Active(); panel != nil {
		if cp, ok := panel.(*ui.ChatPanel); ok {
			return cp.SessionID()
		}
	}
	return ""
}

// treeStatusChangedMsg is sent by watchTreeStatusCmd (and the per-session
// watchers) whenever any file under sessionBaseDir() changes. Receiving it
// in Update triggers a full re-read of status.json for every visible chat
// and a sync of the per-session watcher set against the current Tree items.
type treeStatusChangedMsg struct {
	sessionID string
}

type statusWatcherClosedMsg struct{}

type sessionWatcherClosedMsg struct {
	sessionID string
}

// setupStatusWatcher attaches fsnotify watchers to sessionBaseDir() and to
// every currently visible chat's subdirectory. Best-effort: a missing base
// directory is created on the fly, and any other error is logged and
// ignored — the UI will simply keep whatever badges it had.
//
// Visible chats drive sessionWatchers so we never waste a file descriptor
// on archived or hidden sessions. syncSessionWatchers (called from Update
// on every treeStatusChangedMsg) keeps the set in step with Tree changes.
func (a *App) setupStatusWatcher() {
	if a.statusWatcher != nil {
		_ = a.statusWatcher.Close()
		a.statusWatcher = nil
		a.statusWatchPending = false
	}
	base := a.sessionBaseDir()
	if err := os.MkdirAll(base, 0755); err != nil {
		log.Printf("automata: cannot create %s: %v", base, err)
		return
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("automata: cannot create status watcher: %v", err)
		return
	}
	if err := w.Add(base); err != nil {
		log.Printf("automata: cannot watch %s: %v", base, err)
		w.Close()
		return
	}
	a.statusWatcher = w
	// Note: per-session watchers are attached by the next syncSessionWatchers
	// call (Init or the first treeStatusChangedMsg). We deliberately don't
	// invoke it here — it now returns []tea.Cmd for the new cmd-chains and
	// must be called by the caller that owns the bubbletea program loop.
}

// syncSessionWatchers makes the sessionWatchers map mirror the set of
// chat items currently visible in the Tree. New chats get a fresh watcher
// on their <id>/ subdirectory; chats that disappeared (archived, deleted,
// moved) get theirs closed and removed. Safe to call on a nil App.status
// watcher — it then just reconciles the inner map and lets the next
// setupStatusWatcher populate the parent watcher.
func (a *App) syncSessionWatchers() []tea.Cmd {
	if a.tree == nil {
		return nil
	}
	if a.sessionWatchers == nil {
		a.sessionWatchers = make(map[string]*fsnotify.Watcher)
	}
	if a.sessionWatchPending == nil {
		a.sessionWatchPending = make(map[string]bool)
	}
	visible := make(map[string]struct{}, len(a.sessionWatchers))
	var newCmds []tea.Cmd
	for _, it := range a.tree.AllItems() {
		if it == nil || it.IsFolder || it.IsTerminal {
			continue
		}
		key := a.tree.SessionKeyOf(it)
		if key == "" {
			continue
		}
		visible[key] = struct{}{}
		if _, ok := a.sessionWatchers[key]; ok {
			continue
		}
		// Attach a watcher on the new session's directory so status.json
		// changes inside it are picked up without polling.
		dir := paths.SessionDir(a.profile, key)
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		sw, err := fsnotify.NewWatcher()
		if err != nil {
			log.Printf("automata: cannot create session watcher for %s: %v", key, err)
			continue
		}
		if err := sw.Add(dir); err != nil {
			log.Printf("automata: cannot watch session %s: %v", key, err)
			sw.Close()
			continue
		}
		a.sessionWatchers[key] = sw
		a.sessionWatchPending[key] = false
		if cmd := a.watchSessionCmd(key); cmd != nil {
			newCmds = append(newCmds, cmd)
		}
	}
	for key, sw := range a.sessionWatchers {
		if _, ok := visible[key]; ok {
			continue
		}
		_ = sw.Close()
		delete(a.sessionWatchers, key)
		delete(a.sessionWatchPending, key)
	}
	return newCmds
}

// watchTreeStatusCmd blocks on the parent status watcher and returns a
// single treeStatusChangedMsg when any event lands. Update re-arms it
// after every event so the watcher stays alive without a busy heartbeat.
// We intentionally ignore the event's op and name — recomputing badges
// and resyncing the per-session watcher set is cheap and idempotent.
func (a *App) watchTreeStatusCmd() tea.Cmd {
	if a.statusWatcher == nil || a.statusWatchPending {
		return nil
	}
	a.statusWatchPending = true
	w := a.statusWatcher
	return func() tea.Msg {
		_, ok := <-w.Events
		if !ok {
			return statusWatcherClosedMsg{}
		}
		return treeStatusChangedMsg{}
	}
}

// watchSessionCmd blocks on a per-session fsnotify watcher and returns a
// single treeStatusChangedMsg when status.json inside that session's
// directory changes. Update re-arms the parent chain on every event; the
// next syncSessionWatchers will re-arm this one if the watcher still
// exists in a.sessionWatchers. Returns nil if the watcher is gone (e.g.
// the chat was removed from the Tree), so callers can safely drop it.
func (a *App) watchSessionCmd(key string) tea.Cmd {
	sw, ok := a.sessionWatchers[key]
	if !ok || sw == nil {
		return nil
	}
	if a.sessionWatchPending == nil {
		a.sessionWatchPending = make(map[string]bool)
	}
	if a.sessionWatchPending[key] {
		return nil
	}
	a.sessionWatchPending[key] = true
	return func() tea.Msg {
		_, ok := <-sw.Events
		if !ok {
			return sessionWatcherClosedMsg{sessionID: key}
		}
		return treeStatusChangedMsg{sessionID: key}
	}
}

// rearmSessionWatchers returns commands only for mounted watchers whose
// previous command delivered an event. Existing pending commands remain the
// sole reader for their watcher, preventing duplicate channel consumers.
func (a *App) rearmSessionWatchers() []tea.Cmd {
	if len(a.sessionWatchers) == 0 {
		return nil
	}
	cmds := make([]tea.Cmd, 0, len(a.sessionWatchers))
	for key := range a.sessionWatchers {
		if cmd := a.watchSessionCmd(key); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

// recomputeTreeStatusBadges walks every chat item, reads its on-disk status.json
// (cheap: a single tiny file, served from the mtime-cached statusReader) and
// pushes an emoji-per-session map to the Tree so that each chat row can show a
// 🧠/📖/✏️/🔍/⚙️/💤 indicator next to its name. Best-effort: missing files
// are treated as idle (no badge).
func (a *App) recomputeTreeStatusBadges() {
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

// Close releases all resources owned by the Bubble Tea application. It is
// called after Program.Run returns, so no goroutine can schedule new UI work.
func (a *App) Close() {
	if a == nil {
		return
	}
	if a.statusWatcher != nil {
		_ = a.statusWatcher.Close()
		a.statusWatcher = nil
	}
	for key, watcher := range a.sessionWatchers {
		_ = watcher.Close()
		delete(a.sessionWatchers, key)
	}
	a.statusWatchPending = false
	for key, em := range a.emulatorCache {
		if em != nil {
			em.Stop()
		}
		delete(a.emulatorCache, key)
	}
	for key, em := range a.familiarEmulatorCache {
		if em != nil {
			em.Stop()
		}
		delete(a.familiarEmulatorCache, key)
	}
	a.runningSessions = make(map[string]struct{})
	// Keep activeSessions persisted: restoreSessions uses this snapshot on
	// the next launch. Runtime emulators are stopped above, but shutdown must
	// not erase the restore contract.
	if a.container != nil {
		a.container.Close()
	}
}

type stateManager struct {
	tab *warp.Tab
}

// sessionBaseDir returns the canonical sessions directory for the active
// profile, including AI_DATA_HOME and the default profile layout.
func (a *App) sessionBaseDir() string {
	return paths.SessionsDir(a.profile)
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
			log.Printf("cannot create CPU profile: %v", err)
			return
		}
		defer f.Close()
		if err := pprof.StartCPUProfile(f); err != nil {
			log.Printf("cannot start CPU profile: %v", err)
			return
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
	defer app.Close()
	if _, err := p.Run(); err != nil {
		log.Printf("[main] p.Run err: %v", err)
	}
}
