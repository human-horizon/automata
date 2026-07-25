package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	akcontext "github.com/HumanHorizon/automata/internal/ai-knowledge/context"
	akjobs "github.com/HumanHorizon/automata/internal/ai-knowledge/jobs"
	akui "github.com/HumanHorizon/automata/internal/ai-knowledge/ui"
	"github.com/HumanHorizon/automata/internal/kanban"
)

// KnowledgePanel renders ai-knowledge data for a single session. The data
// layer (status.json, plans.json, jobs, notes) is owned by ai-knowledge; we
// just read on a tick and hand the resulting structures to the ai-knowledge
// reusable renderer.
type KnowledgePanel struct {
	sessionID string

	width        int
	height       int
	scrollOffset int

	lastRefresh time.Time
	data        *akcontext.Data
	jobs        []akjobs.Job

	// Cached readers prevent re-reading unchanged files on every tick.
	contextReader *akcontext.CachedReader
	jobsReader    *akjobs.CachedReader

	// Settings state for header buttons
	autoContinue bool
	dual         bool

	// Current task title (from kanban) shown before plans
	currentTask string
	lastTaskRefresh time.Time
}

// NewKnowledgePanel creates an empty panel; data loads on the first Refresh.
func NewKnowledgePanel() *KnowledgePanel {
	return &KnowledgePanel{
		contextReader: akcontext.NewCachedReader(),
		jobsReader:    akjobs.NewCachedReader(),
	}
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
	k.readSettings()
}

// readSettings loads autoContinue and dual from settings.json.
func (k *KnowledgePanel) readSettings() {
	if k.sessionID == "" {
		return
	}
	settingsPath := k.settingsPath()
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return
	}
	var s struct {
		AutoContinue bool `json:"autoContinue"`
		Dual         bool `json:"dual"`
	}
	if json.Unmarshal(data, &s) == nil {
		k.autoContinue = s.AutoContinue
		k.dual = s.Dual
	}
}

// writeSettings saves autoContinue and dual to settings.json.
func (k *KnowledgePanel) writeSettings() {
	if k.sessionID == "" {
		return
	}
	settingsPath := k.settingsPath()

	var s map[string]interface{}
	if data, err := os.ReadFile(settingsPath); err == nil {
		json.Unmarshal(data, &s)
	}
	if s == nil {
		s = make(map[string]interface{})
	}
	s["autoContinue"] = k.autoContinue
	s["dual"] = k.dual

	os.MkdirAll(filepath.Dir(settingsPath), 0755)
	data, _ := json.MarshalIndent(s, "", "  ")
	os.WriteFile(settingsPath, data, 0644)
}

// settingsPath returns the path to settings.json for the current session.
func (k *KnowledgePanel) settingsPath() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "/Users/a"
	}
	base := os.Getenv("AI_DATA_HOME")
	if base == "" {
		base = filepath.Join(home, ".ai", "automata")
	}
	profile := "default"
	if idx := strings.Index(k.sessionID, "__"); idx > 0 {
		profile = slugify(k.sessionID[:idx])
	}
	return filepath.Join(base, "profiles", profile, "sessions", k.sessionID, "settings.json")
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			if b.Len() > 0 && b.String()[b.Len()-1] != '-' {
				b.WriteRune('-')
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// Refresh re-reads status, plans, jobs and notes from disk for the current
// session. Safe to call from a tick.
func (k *KnowledgePanel) Refresh() {
	if k.sessionID == "" {
		return
	}
	if d, err := k.contextReader.Read(k.sessionID); err == nil {
		k.data = d
	}
	if j, err := k.jobsReader.List(k.sessionID); err == nil {
		k.jobs = j
	}
	k.refreshCurrentTask()
	k.lastRefresh = time.Now()
}

// refreshCurrentTask reads kanban tasks and finds the one in progress for this session.
func (k *KnowledgePanel) refreshCurrentTask() {
	if k.sessionID == "" || time.Since(k.lastTaskRefresh) < 2*time.Second {
		return
	}
	k.lastTaskRefresh = time.Now()
	// Derive domain from sessionID (remove last segment after last dot)
	domain := domainOf(k.sessionID)
	if domain == "" {
		k.currentTask = ""
		return
	}
	// Profile is the first segment before __
	profile := ""
	if idx := strings.Index(k.sessionID, "__"); idx > 0 {
		profile = slugify(k.sessionID[:idx])
	}
	tasks, err := kanban.ReadAll(domain, profile)
	if err != nil {
		k.currentTask = ""
		return
	}
	for _, t := range tasks {
		if t.AssignedTo == k.sessionID && t.Status == "progress" {
			k.currentTask = t.Title
			return
		}
	}
	k.currentTask = ""
}

// SetSize updates the panel's viewport.
func (k *KnowledgePanel) SetSize(w, h int) {
	k.width = w
	k.height = h
}

// Update is the bubbletea Msg handler. We only care about resizes; refresh
// is driven externally by the parent App's tick.
func (k *KnowledgePanel) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		k.SetSize(msg.Width, msg.Height)
	case tea.MouseMsg:
		k.handleMouse(msg)
	case tea.KeyMsg:
		k.handleKey(msg)
	}
	return nil
}

func (k *KnowledgePanel) handleMouse(msg tea.MouseMsg) {
	// Check header button clicks
	if msg.Y == 0 && msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		// Header: " Knowledge  [✓ auto] [× dual]"
		// auto button starts at column 12, dual at column 22
		if msg.X >= 12 && msg.X < 20 {
			k.autoContinue = !k.autoContinue
			if k.autoContinue {
				k.dual = false
			}
			k.writeSettings()
			return
		}
		if msg.X >= 22 && msg.X < 30 {
			k.dual = !k.dual
			if k.dual {
				k.autoContinue = false
			}
			k.writeSettings()
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

	// Build header with auto/dual buttons
	autoLabel := "[× auto]"
	autoStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#cc241d"))
	if k.autoContinue {
		autoLabel = "[✓ auto]"
		autoStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#98971a"))
	}
	dualLabel := "[× dual]"
	dualStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#cc241d"))
	if k.dual {
		dualLabel = "[✓ dual]"
		dualStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#98971a"))
	}

	title := lipgloss.NewStyle().Bold(true).
		Foreground(lipgloss.Color("#edb449")).
		Render("Knowledge ")

	buttons := autoStyle.Render(autoLabel) + " " + dualStyle.Render(dualLabel)
	header := title + buttons

	if k.height < 2 {
		return header
	}
	body := akui.View(k.width, k.height-1, k.data, k.jobs, k.currentTask)
	lines := strings.Split(body, "\n")
	if len(lines) > k.height-1 {
		maxOffset := len(lines) - (k.height - 1)
		if k.scrollOffset > maxOffset {
			k.scrollOffset = maxOffset
		}
		if k.scrollOffset < 0 {
			k.scrollOffset = 0
		}
		lines = lines[k.scrollOffset:]
		if len(lines) > k.height-1 {
			lines = lines[:k.height-1]
		}
	} else {
		k.scrollOffset = 0
	}
	for len(lines) < k.height-1 {
		lines = append(lines, strings.Repeat(" ", k.width))
	}
	return header + "\n" + strings.Join(lines, "\n")
}

// domainOf derives the same "domain" string that ai-knowledge uses to scope
// notes. The domain is the session id without the trailing chat segment.
func domainOf(sessionID string) string {
	for i := len(sessionID) - 1; i >= 0; i-- {
		if sessionID[i] == '.' {
			return sessionID[:i]
		}
	}
	return sessionID
}
