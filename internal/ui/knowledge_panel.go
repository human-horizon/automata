package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/HumanHorizon/automata/internal/paths"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/fsnotify/fsnotify"
	warp "github.com/starframe-dev/warp"

	akcontext "github.com/HumanHorizon/automata/internal/ai-knowledge/context"
	akjobs "github.com/HumanHorizon/automata/internal/ai-knowledge/jobs"
	akui "github.com/HumanHorizon/automata/internal/ai-knowledge/ui"
	"github.com/HumanHorizon/automata/internal/kanban"
	apptheme "github.com/HumanHorizon/automata/internal/theme"
)

// knowledgeChangedMsg is sent by watchKnowledgeCmd when fsnotify reports a
// change to status.json, plans.json, settings.json or notes.json in the
// current session directory. The Cmd re-arms itself after every event so
// the UI is event-driven and consumes 0 CPU while idle.
type knowledgeChangedMsg struct{}

type knowledgeWatcherErrorMsg struct {
	err error
}

// jobsChangedMsg is the analogous event for any change inside the session's
// jobs/ directory (new job, completed job, status flip in job.json).
type jobsChangedMsg struct{}

type jobsWatcherErrorMsg struct {
	err error
}

// sessionDataPath returns the per-session directory that owns status.json,
// plans.json, settings.json and the jobs/ subdirectory. Notes live one level
// up under the domain directory, so they have their own watcher set up by
// the ContextPanel.
func sessionDataPath(profile, sessionID string) string {
	return paths.SessionDir(profile, sessionID)
}

// KnowledgePanel renders ai-knowledge data for a single session. The data
// layer (status.json, plans.json, jobs, notes) is owned by ai-knowledge; we
// just read on a tick and hand the resulting structures to the ai-knowledge
// reusable renderer.
type KnowledgePanel struct {
	profile   string
	sessionID string
	domain    string
	palette   apptheme.Theme

	width        int
	height       int
	scrollOffset int

	lastRefresh time.Time
	data        *akcontext.Data
	jobs        []akjobs.Job

	// Cached readers prevent re-reading unchanged files on every tick.
	contextReader *akcontext.CachedReader
	jobsReader    *akjobs.CachedReader

	// Settings state for header buttons.
	autoContinue   bool
	dual           bool
	settingsError  string
	settingsWriter func(path string, data []byte) error

	// Current task title (from kanban) shown before plans
	currentTask     string
	lastTaskRefresh time.Time

	// knowledgeWatcher observes status.json, plans.json and settings.json
	// for the current session. The knowledge panel reacts to its events
	// directly without relying on a periodic poll.
	knowledgeWatcher     *fsnotify.Watcher
	knowledgeWatcherPath string

	// jobsWatcher observes the per-session jobs/ directory so newly
	// spawned or finished jobs surface in the right panel immediately.
	jobsWatcher     *fsnotify.Watcher
	jobsWatcherPath string

	// knowledgeWatchPending and jobsWatchPending guard against stacking
	// multiple blocking watchJobsCmd/watchKnowledgeCmd Cmds.
	knowledgeWatchPending bool
	jobsWatchPending      bool
}

// NewKnowledgePanel creates an empty panel; data loads on the first Refresh.
func NewKnowledgePanel() *KnowledgePanel {
	return &KnowledgePanel{
		palette:       apptheme.Default(),
		contextReader: akcontext.NewCachedReader(),
		jobsReader:    akjobs.NewCachedReader(),
	}
}

// SetProfile updates the canonical profile used by all session readers and
// re-arms watchers for the active session when the profile changes.
func (k *KnowledgePanel) SetProfile(profile string) {
	if k.profile == profile {
		return
	}
	k.closeWatchers()
	k.profile = profile
	k.data = nil
	k.jobs = nil
	k.contextReader.Invalidate(k.profile, k.sessionID)
	k.readSettings()
	if k.sessionID != "" {
		k.setupWatchers()
	}
}

// SetTheme updates the palette used by the knowledge panel.
func (k *KnowledgePanel) SetTheme(palette apptheme.Theme) {
	k.palette = palette
}

// SetDomain sets the canonical tree-derived domain used for Kanban lookup.
// Domain identity is never inferred from the session ID.
func (k *KnowledgePanel) SetDomain(domain string) {
	if k.domain == domain {
		return
	}
	k.domain = domain
	k.currentTask = ""
	k.lastTaskRefresh = time.Time{}
	k.refreshCurrentTask()
}

// SetSession switches the panel to a different session. Forces a refresh on
// the next tick.
func (k *KnowledgePanel) SetSession(sessionID string) {
	if sessionID == k.sessionID {
		return
	}
	k.sessionID = sessionID
	k.data = nil
	k.jobs = nil
	k.contextReader.Invalidate(k.profile, sessionID)
	k.readSettings()
	k.closeWatchers()
	if sessionID != "" {
		k.setupWatchers()
	}
}

// closeWatchers releases both the status/plans/settings watcher and the
// jobs/ directory watcher. Called from SetSession and on shutdown so we
// never leak fsnotify descriptors.
func (k *KnowledgePanel) closeWatchers() {
	if k.knowledgeWatcher != nil {
		_ = k.knowledgeWatcher.Close()
		k.knowledgeWatcher = nil
	}
	if k.jobsWatcher != nil {
		_ = k.jobsWatcher.Close()
		k.jobsWatcher = nil
	}
	k.knowledgeWatcherPath = ""
	k.jobsWatcherPath = ""
	k.knowledgeWatchPending = false
	k.jobsWatchPending = false
}

// Close releases all filesystem watchers owned by the panel.
func (k *KnowledgePanel) Close() {
	k.closeWatchers()
}

// setupWatchers attaches fsnotify watchers to the session directory (for
// status/plans/settings) and to the jobs/ subdirectory. Missing directories
// are watched through their nearest existing parent, so creation is handled
// by an event instead of a polling tick.
func (k *KnowledgePanel) setupWatchers() {
	if k.sessionID == "" {
		return
	}
	k.attachKnowledgeWatcherIfMissing()
	k.attachJobsWatcherIfMissing()
}

// attachKnowledgeWatcherIfMissing watches the session directory directly, or
// the profile sessions directory until a new session directory appears.
func (k *KnowledgePanel) attachKnowledgeWatcherIfMissing() {
	if k.sessionID == "" {
		return
	}
	sessionDir := sessionDataPath(k.profile, k.sessionID)
	watchPath := sessionDir
	if info, err := os.Stat(sessionDir); err != nil || !info.IsDir() {
		watchPath = paths.SessionsDir(k.profile)
		if err := os.MkdirAll(watchPath, 0o755); err != nil {
			return
		}
	}
	if k.knowledgeWatcher != nil && k.knowledgeWatcherPath == watchPath {
		return
	}
	if k.knowledgeWatcher != nil {
		_ = k.knowledgeWatcher.Close()
		k.knowledgeWatcher = nil
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}
	if err := w.Add(watchPath); err != nil {
		w.Close()
		return
	}
	k.knowledgeWatcher = w
	k.knowledgeWatcherPath = watchPath
}

// readSettings loads and validates known settings without treating malformed
// files as an empty object.
func (k *KnowledgePanel) readSettings() {
	k.autoContinue = false
	k.dual = false
	k.settingsError = ""
	if k.sessionID == "" {
		return
	}
	data, err := os.ReadFile(k.settingsPath())
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		k.setSettingsError(fmt.Errorf("read settings: %w", err))
		return
	}
	_, autoContinue, dual, err := decodeSettings(data)
	if err != nil {
		k.setSettingsError(err)
		return
	}
	k.autoContinue = autoContinue
	k.dual = dual
}

func decodeSettings(data []byte) (map[string]json.RawMessage, bool, bool, error) {
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, false, false, fmt.Errorf("decode settings: %w", err)
	}
	if settings == nil {
		return nil, false, false, fmt.Errorf("settings must be a JSON object")
	}
	readBool := func(key string) (bool, error) {
		raw, exists := settings[key]
		if !exists {
			return false, nil
		}
		var value *bool
		if err := json.Unmarshal(raw, &value); err != nil || value == nil {
			if err == nil {
				err = fmt.Errorf("value must be a boolean")
			}
			return false, fmt.Errorf("settings.%s: %w", key, err)
		}
		return *value, nil
	}
	autoContinue, err := readBool("autoContinue")
	if err != nil {
		return nil, false, false, err
	}
	dual, err := readBool("dual")
	if err != nil {
		return nil, false, false, err
	}
	return settings, autoContinue, dual, nil
}

// writeSettings atomically updates known settings while preserving unknown keys.
func (k *KnowledgePanel) writeSettings() error {
	if k.sessionID == "" {
		return fmt.Errorf("cannot write settings without a session")
	}
	if k.autoContinue && k.dual {
		err := fmt.Errorf("autoContinue and dual settings are mutually exclusive")
		k.setSettingsError(err)
		return err
	}
	settingsPath := k.settingsPath()
	settings := make(map[string]json.RawMessage)
	current, err := os.ReadFile(settingsPath)
	if err == nil {
		settings, _, _, err = decodeSettings(current)
		if err != nil {
			k.setSettingsError(err)
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		err = fmt.Errorf("read settings before update: %w", err)
		k.setSettingsError(err)
		return err
	}
	autoContinue, err := json.Marshal(k.autoContinue)
	if err != nil {
		k.setSettingsError(err)
		return err
	}
	dual, err := json.Marshal(k.dual)
	if err != nil {
		k.setSettingsError(err)
		return err
	}
	settings["autoContinue"] = autoContinue
	settings["dual"] = dual
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		err = fmt.Errorf("encode settings: %w", err)
		k.setSettingsError(err)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		err = fmt.Errorf("create settings directory: %w", err)
		k.setSettingsError(err)
		return err
	}
	writer := k.settingsWriter
	if writer == nil {
		writer = writeSettingsAtomic
	}
	if err := writer(settingsPath, data); err != nil {
		k.setSettingsError(err)
		return err
	}
	k.settingsError = ""
	return nil
}

func (k *KnowledgePanel) setSettingsError(err error) {
	if err == nil {
		k.settingsError = ""
		return
	}
	k.settingsError = err.Error()
	log.Printf("automata: settings for %q: %v", k.sessionID, err)
}

type committedSettingsWriteError struct {
	path string
	err  error
}

func (e *committedSettingsWriteError) Error() string {
	return fmt.Sprintf("settings committed to %s but directory sync failed: %v", e.path, e.err)
}

func (e *committedSettingsWriteError) Unwrap() error { return e.err }

func settingsWriteWasCommitted(err error) bool {
	var committed *committedSettingsWriteError
	return errors.As(err, &committed)
}

func writeSettingsAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	temporary, err := os.CreateTemp(dir, ".settings-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary settings: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("chmod temporary settings: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary settings: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync temporary settings: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary settings: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace settings: %w", err)
	}
	directory, err := os.Open(dir)
	if err != nil {
		return &committedSettingsWriteError{path: path, err: err}
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil || closeErr != nil {
		return &committedSettingsWriteError{path: path, err: errors.Join(syncErr, closeErr)}
	}
	return nil
}

// settingsPath returns the path to settings.json for the current session.
func (k *KnowledgePanel) settingsPath() string {
	return filepath.Join(sessionDataPath(k.profile, k.sessionID), "settings.json")
}

// Refresh re-reads the data files for the current session. The UI is now
// fully event-driven via fsnotify, but we keep the method so external
// callers (e.g. legacy tick) can request a manual reload on demand.
func (k *KnowledgePanel) Refresh() {
	if k.sessionID == "" {
		return
	}
	if d, err := k.contextReader.ReadForProfile(k.profile, k.sessionID); err == nil {
		k.data = d
	}
	if j, err := k.jobsReader.ListForProfile(k.profile, k.sessionID); err == nil {
		k.jobs = j
	}
	k.refreshCurrentTask()
	k.lastRefresh = time.Now()
}

// SetSize updates the panel's viewport.
func (k *KnowledgePanel) SetSize(w, h int) {
	k.width = w
	k.height = h
}

// Update forwards bubbletea messages and re-arms the blocking fsnotify
// Cmds for the session directory and jobs/ subdirectory. The watch
// commands sleep cheaply on their Events channels and react to every
// real change, so the panel uses 0 CPU while idle.
func (k *KnowledgePanel) Update(msg tea.Msg) tea.Cmd {
	var baseCmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		k.SetSize(msg.Width, msg.Height)
	case warp.ResizeMsg:
		k.SetSize(msg.Width, msg.Height)
	case tea.MouseMsg:
		k.handleMouse(msg)
	case tea.KeyMsg:
		k.handleKey(msg)
	case knowledgeChangedMsg:
		k.knowledgeWatchPending = false
		k.refreshKnowledgeData()
	case knowledgeWatcherErrorMsg:
		k.knowledgeWatchPending = false
		if msg.err != nil {
			log.Printf("automata: knowledge watcher failed: %v", msg.err)
		}
		k.resetKnowledgeWatcher()
		k.attachKnowledgeWatcherIfMissing()
		k.refreshKnowledgeData()
	case jobsChangedMsg:
		k.jobsWatchPending = false
		k.refreshJobsData()
	case jobsWatcherErrorMsg:
		k.jobsWatchPending = false
		if msg.err != nil {
			log.Printf("automata: jobs watcher failed: %v", msg.err)
		}
		k.resetJobsWatcher()
		k.attachJobsWatcherIfMissing()
		k.refreshJobsData()
	}

	if k.knowledgeWatcher != nil && !k.knowledgeWatchPending {
		k.knowledgeWatchPending = true
		baseCmd = tea.Batch(baseCmd, k.watchKnowledgeCmd())
	}
	if k.jobsWatcher != nil && !k.jobsWatchPending {
		k.jobsWatchPending = true
		baseCmd = tea.Batch(baseCmd, k.watchJobsCmd())
	}
	return baseCmd
}

// attachJobsWatcherIfMissing ensures the per-session jobs/ subdirectory has
// an active fsnotify watcher. Until jobs/ exists, the session directory is
// watched so its creation is handled by the next event.
func (k *KnowledgePanel) resetKnowledgeWatcher() {
	if k.knowledgeWatcher != nil {
		_ = k.knowledgeWatcher.Close()
		k.knowledgeWatcher = nil
	}
	k.knowledgeWatcherPath = ""
}

func (k *KnowledgePanel) resetJobsWatcher() {
	if k.jobsWatcher != nil {
		_ = k.jobsWatcher.Close()
		k.jobsWatcher = nil
	}
	k.jobsWatcherPath = ""
}

func (k *KnowledgePanel) refreshKnowledgeData() {
	k.contextReader.Invalidate(k.profile, k.sessionID)
	// The session-level watcher fires both for data changes and for creation
	// of the jobs/ directory. Reattaching both watchers keeps the chain alive
	// after either kind of event.
	k.attachKnowledgeWatcherIfMissing()
	k.attachJobsWatcherIfMissing()
	if d, err := k.contextReader.ReadForProfile(k.profile, k.sessionID); err == nil {
		k.data = d
	}
	if j, err := k.jobsReader.ListForProfile(k.profile, k.sessionID); err == nil {
		k.jobs = j
	}
	k.readSettings()
	k.refreshCurrentTask()
	k.lastRefresh = time.Now()
}

func (k *KnowledgePanel) refreshJobsData() {
	k.attachKnowledgeWatcherIfMissing()
	k.attachJobsWatcherIfMissing()
	// PruneStaleSession is the only place that flips running→exited in
	// job.json. We deliberately do it before re-reading the list so the
	// updated metadata is what the user sees.
	if err := akjobs.PruneStaleSessionForProfile(k.profile, k.sessionID); err == nil {
		if j, err := k.jobsReader.ListForProfile(k.profile, k.sessionID); err == nil {
			k.jobs = j
		}
	}
	k.lastRefresh = time.Now()
}

func (k *KnowledgePanel) attachJobsWatcherIfMissing() {
	if k.sessionID == "" {
		return
	}
	jobsDir := filepath.Join(sessionDataPath(k.profile, k.sessionID), "jobs")
	watchPath := jobsDir
	if info, err := os.Stat(jobsDir); err != nil || !info.IsDir() {
		if _, err := os.Stat(sessionDataPath(k.profile, k.sessionID)); err != nil {
			return
		}
		watchPath = sessionDataPath(k.profile, k.sessionID)
	}
	if k.jobsWatcher != nil && k.jobsWatcherPath == watchPath {
		return
	}
	if k.jobsWatcher != nil {
		_ = k.jobsWatcher.Close()
		k.jobsWatcher = nil
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}
	if err := w.Add(watchPath); err != nil {
		w.Close()
		return
	}
	k.jobsWatcher = w
	k.jobsWatcherPath = watchPath
}

// watchKnowledgeCmd blocks on the session-level fsnotify watcher and
// returns a single knowledgeChangedMsg when an event arrives.
func (k *KnowledgePanel) watchKnowledgeCmd() tea.Cmd {
	if k.knowledgeWatcher == nil {
		return nil
	}
	w := k.knowledgeWatcher
	return func() tea.Msg {
		select {
		case ev, ok := <-w.Events:
			if !ok {
				return knowledgeWatcherErrorMsg{}
			}
			if strings.HasSuffix(ev.Name, ".swp") {
				return knowledgeChangedMsg{}
			}
			return knowledgeChangedMsg{}
		case err, ok := <-w.Errors:
			if !ok {
				return knowledgeWatcherErrorMsg{}
			}
			return knowledgeWatcherErrorMsg{err: err}
		}
	}
}

// watchJobsCmd blocks on the jobs/ directory watcher. Each event triggers
// a re-read of the cached jobs list.
func (k *KnowledgePanel) watchJobsCmd() tea.Cmd {
	if k.jobsWatcher == nil {
		return nil
	}
	w := k.jobsWatcher
	return func() tea.Msg {
		select {
		case _, ok := <-w.Events:
			if !ok {
				return jobsWatcherErrorMsg{}
			}
			return jobsChangedMsg{}
		case err, ok := <-w.Errors:
			if !ok {
				return jobsWatcherErrorMsg{}
			}
			return jobsWatcherErrorMsg{err: err}
		}
	}
}

// refreshCurrentTask reads kanban tasks and finds the one in progress for this session.
func (k *KnowledgePanel) refreshCurrentTask() {
	if k.sessionID == "" || time.Since(k.lastTaskRefresh) < 2*time.Second {
		return
	}
	k.lastTaskRefresh = time.Now()
	domain := k.domain
	if domain == "" {
		k.currentTask = ""
		return
	}
	tasks, err := kanban.ReadAll(domain, k.profile)
	if err != nil {
		log.Printf("automata: read Kanban domain %q for current task: %v", domain, err)
	}
	for _, t := range tasks {
		if t.AssignedTo == k.sessionID && t.Status == "progress" {
			k.currentTask = t.Title
			return
		}
	}
	k.currentTask = ""
}

func (k *KnowledgePanel) handleMouse(msg tea.MouseMsg) {
	// Check header button clicks
	if msg.Y == 0 && msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		// Header: " Knowledge  [✓ auto] [× dual]"
		// auto button starts at column 12, dual at column 22
		if msg.X >= 12 && msg.X < 20 {
			oldAutoContinue, oldDual := k.autoContinue, k.dual
			k.autoContinue = !k.autoContinue
			if k.autoContinue {
				k.dual = false
			}
			if err := k.writeSettings(); err != nil && !settingsWriteWasCommitted(err) {
				k.autoContinue, k.dual = oldAutoContinue, oldDual
			}
			return
		}
		if msg.X >= 22 && msg.X < 30 {
			oldAutoContinue, oldDual := k.autoContinue, k.dual
			k.dual = !k.dual
			if k.dual {
				k.autoContinue = false
			}
			if err := k.writeSettings(); err != nil && !settingsWriteWasCommitted(err) {
				k.autoContinue, k.dual = oldAutoContinue, oldDual
			}
			return
		}
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		k.scrollOffset -= 3
		if k.scrollOffset < 0 {
			k.scrollOffset = 0
		}
	case tea.MouseButtonWheelDown:
		k.scrollOffset += 3
	}
}

func (k *KnowledgePanel) handleKey(msg tea.KeyMsg) {
	switch msg.String() {
	case "up":
		k.scrollOffset--
		if k.scrollOffset < 0 {
			k.scrollOffset = 0
		}
	case "down":
		k.scrollOffset++
	case "pgup":
		k.scrollOffset -= 10
		if k.scrollOffset < 0 {
			k.scrollOffset = 0
		}
	case "pgdown":
		k.scrollOffset += 10
	}
}

// View renders the panel as a string for the parent App's View(). The signature
// matches warp.Panel, so KnowledgePanel can be embedded directly into a split
// without any wrapper.
func (k *KnowledgePanel) View(width, height int) string {
	if width > 0 {
		k.width = width
	}
	if height > 0 {
		k.height = height
	}
	if k.width <= 0 {
		k.width = 80
	}
	if k.height <= 0 {
		k.height = 24
	}

	// Build header with auto/dual buttons.
	autoLabel := "[× auto]"
	autoStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(k.palette.Error))
	if k.autoContinue {
		autoLabel = "[✓ auto]"
		autoStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(k.palette.Success))
	}
	dualLabel := "[× dual]"
	dualStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(k.palette.Error))
	if k.dual {
		dualLabel = "[✓ dual]"
		dualStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(k.palette.Success))
	}

	title := lipgloss.NewStyle().Bold(true).
		Foreground(lipgloss.Color(k.palette.TextStrong)).
		Render("Knowledge ")

	buttons := autoStyle.Render(autoLabel) + " " + dualStyle.Render(dualLabel)
	header := title + buttons

	if k.height < 2 {
		return header
	}
	bodyHeight := k.height - 1
	warning := ""
	if k.settingsError != "" {
		warning = lipgloss.NewStyle().Foreground(lipgloss.Color(k.palette.Error)).Render(
			ansi.Truncate("Settings error: "+k.settingsError, k.width, "…"),
		)
		bodyHeight--
	}
	lines := []string{}
	if bodyHeight > 0 {
		body := akui.ViewWithTheme(k.width, bodyHeight, k.data, k.jobs, k.currentTask, k.palette)
		lines = strings.Split(body, "\n")
	}
	if len(lines) > bodyHeight {
		maxOffset := len(lines) - bodyHeight
		if k.scrollOffset > maxOffset {
			k.scrollOffset = maxOffset
		}
		if k.scrollOffset < 0 {
			k.scrollOffset = 0
		}
		lines = lines[k.scrollOffset:]
		if len(lines) > bodyHeight {
			lines = lines[:bodyHeight]
		}
	} else {
		k.scrollOffset = 0
	}
	for len(lines) < bodyHeight {
		lines = append(lines, strings.Repeat(" ", k.width))
	}
	content := strings.Join(lines, "\n")
	if warning != "" {
		if content != "" {
			content = warning + "\n" + content
		} else {
			content = warning
		}
	}
	return header + "\n" + content
}
