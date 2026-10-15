package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/HumanHorizon/automata/internal/ai-knowledge/notesactualize"
	tea "github.com/charmbracelet/bubbletea"
	warp "github.com/starframe-dev/warp"
)

// NotesActualizer is the UI boundary for project-scoped consolidation and Undo.
type NotesActualizer interface {
	LoadModelSettings() (notesactualize.Settings, error)
	ActualizeWithOptions(context.Context, string, string, notesactualize.RunOptions) (notesactualize.Result, error)
	UndoLast(string, string) (string, error)
}

type notesActualizeConfig struct {
	settings     notesactualize.Settings
	options      notesactualize.RunOptions
	modelMenu    *warp.DropdownMenu
	thinkingMenu *warp.DropdownMenu
	focused      int
	modelRow     int
	thinkingRow  int
	runRow       int
	cancelRow    int
}

const (
	actualizeFocusModel = iota
	actualizeFocusThinking
	actualizeFocusRun
	actualizeFocusCancel
)

type notesActualizeMsg struct {
	profile    string
	domain     string
	generation uint64
	result     notesactualize.Result
	err        error
	undo       bool
	undoID     string
}

func (c *ContextPanel) SetNotesActualizer(actualizer NotesActualizer) {
	c.notesActualizer = actualizer
}

func (c *ContextPanel) beginNotesActualize(undo bool) tea.Cmd {
	if !undo {
		return c.openNotesActualizeConfig()
	}
	profile, domain := c.profile, c.domain
	if domain == "" {
		c.notesStatus = "✗ No project selected"
		return nil
	}
	key := notesProjectKey(profile, domain)
	if c.notesActualizeRunning[key] {
		return nil
	}
	if c.notesActualizer == nil {
		c.notesStatus = "✗ AI runner unavailable"
		return nil
	}
	if undo && c.notesUndoIDs[key] == "" {
		return nil
	}

	c.notesActualizeRunning[key] = true
	c.notesActualizeGeneration++
	generation := c.notesActualizeGeneration
	actualizer := c.notesActualizer
	c.notesStatus = "Undoing…"
	return func() tea.Msg {
		undoID, err := actualizer.UndoLast(profile, domain)
		return notesActualizeMsg{profile: profile, domain: domain, generation: generation, undo: true, undoID: undoID, err: err}
	}
}

func (c *ContextPanel) openNotesActualizeConfig() tea.Cmd {
	if c.domain == "" {
		c.notesStatus = "✗ No project selected"
		return nil
	}
	key := notesProjectKey(c.profile, c.domain)
	if c.notesActualizeRunning[key] {
		return nil
	}
	if c.notesActualizer == nil {
		c.notesStatus = "✗ AI runner unavailable"
		return nil
	}
	settings, err := c.notesActualizer.LoadModelSettings()
	if err != nil {
		c.notesStatus = "✗ Model settings: " + err.Error()
		return nil
	}
	config := &notesActualizeConfig{
		settings: settings,
		options: notesactualize.RunOptions{
			Model: settings.DefaultModel, ThinkingLevel: settings.DefaultThinkingLevel,
		},
	}
	modelItems := make([]warp.DropdownItem, len(settings.Models))
	for index, model := range settings.Models {
		modelItems[index] = warp.DropdownItem{Label: model, Selected: model == config.options.Model}
	}
	thinkingItems := make([]warp.DropdownItem, len(settings.ThinkingLevels))
	for index, level := range settings.ThinkingLevels {
		thinkingItems[index] = warp.DropdownItem{Label: level, Selected: level == config.options.ThinkingLevel}
	}
	config.modelMenu = warp.NewDropdownMenu("Model: "+config.options.Model, modelItems)
	config.thinkingMenu = warp.NewDropdownMenu("Thinking: "+config.options.ThinkingLevel, thinkingItems)
	config.modelMenu.OnSelect = func(index int) {
		if index >= 0 && index < len(settings.Models) {
			config.options.Model = settings.Models[index]
			config.modelMenu.Label = "Model: " + config.options.Model
		}
	}
	config.thinkingMenu.OnSelect = func(index int) {
		if index >= 0 && index < len(settings.ThinkingLevels) {
			config.options.ThinkingLevel = settings.ThinkingLevels[index]
			config.thinkingMenu.Label = "Thinking: " + config.options.ThinkingLevel
		}
	}
	c.notesActualizeConfig = config
	c.notesStatus = "Choose model and thinking level"
	return nil
}

func (c *ContextPanel) startNotesActualize(options notesactualize.RunOptions) tea.Cmd {
	profile, domain := c.profile, c.domain
	key := notesProjectKey(profile, domain)
	if c.notesActualizeRunning[key] || c.notesActualizer == nil {
		return nil
	}
	if err := c.notesActualizeConfig.settings.ValidateOptions(options); err != nil {
		c.notesStatus = "✗ Model selection: " + err.Error()
		return nil
	}
	c.notesActualizeConfig = nil
	c.notesActualizeRunning[key] = true
	c.notesActualizeGeneration++
	generation := c.notesActualizeGeneration
	ctx, cancel := context.WithCancel(context.Background())
	c.notesActualizeCancel[key] = cancel
	c.notesStatus = "Actualizing…"
	actualizer := c.notesActualizer
	return func() tea.Msg {
		result, err := actualizer.ActualizeWithOptions(ctx, profile, domain, options)
		return notesActualizeMsg{profile: profile, domain: domain, generation: generation, result: result, err: err}
	}
}

func (c *ContextPanel) cancelNotesActualizeConfig() {
	c.notesActualizeConfig = nil
	c.notesStatus = "Actualize canceled"
}

func (c *ContextPanel) handleNotesActualizeConfigKey(msg tea.KeyMsg) tea.Cmd {
	config := c.notesActualizeConfig
	if config == nil {
		return nil
	}
	if config.modelMenu.Open {
		if msg.String() == "tab" {
			config.modelMenu.Close()
			config.focused = actualizeFocusThinking
			return nil
		}
		config.modelMenu.Update(msg)
		return nil
	}
	if config.thinkingMenu.Open {
		if msg.String() == "tab" {
			config.thinkingMenu.Close()
			config.focused = actualizeFocusRun
			return nil
		}
		config.thinkingMenu.Update(msg)
		return nil
	}
	switch msg.String() {
	case "esc":
		c.cancelNotesActualizeConfig()
	case "tab", "down":
		config.focused = (config.focused + 1) % 4
	case "shift+tab", "up":
		config.focused = (config.focused + 3) % 4
	case "enter":
		switch config.focused {
		case actualizeFocusModel:
			config.modelMenu.Open = true
			config.modelMenu.Hovered = selectedDropdownItem(config.modelMenu.Items)
		case actualizeFocusThinking:
			config.thinkingMenu.Open = true
			config.thinkingMenu.Hovered = selectedDropdownItem(config.thinkingMenu.Items)
		case actualizeFocusRun:
			return c.startNotesActualize(config.options)
		case actualizeFocusCancel:
			c.cancelNotesActualizeConfig()
		}
	}
	return nil
}

func (c *ContextPanel) renderNotesActualizeConfig(tabBar string) string {
	config := c.notesActualizeConfig
	if config == nil {
		return tabBar
	}
	width := c.width
	if width <= 0 {
		width = 80
	}
	lines := []string{tabBar, strings.Repeat(" ", width), c.renderNotesToolbar(width), strings.Repeat(" ", width)}
	lines = append(lines, truncatePanelText("Actualize — choose model and reasoning", width))
	modelLabel := "Model"
	if config.focused == actualizeFocusModel {
		modelLabel = "▸ " + modelLabel
	}
	lines = append(lines, modelLabel)
	config.modelRow = len(lines)
	modelLabelText := "Model: " + config.options.Model
	if config.focused == actualizeFocusModel {
		modelLabelText = "▸ " + modelLabelText
	}
	config.modelMenu.Label = modelLabelText
	modelLines := strings.Split(config.modelMenu.View(width, max(1, c.height-len(lines))), "\n")
	lines = append(lines, modelLines...)
	lines = append(lines, strings.Repeat(" ", width))
	thinkingTitle := "Thinking level"
	if config.focused == actualizeFocusThinking {
		thinkingTitle = "▸ " + thinkingTitle
	}
	lines = append(lines, thinkingTitle)
	config.thinkingRow = len(lines)
	thinkingLabelText := "Thinking: " + config.options.ThinkingLevel
	if config.focused == actualizeFocusThinking {
		thinkingLabelText = "▸ " + thinkingLabelText
	}
	config.thinkingMenu.Label = thinkingLabelText
	thinkingLines := strings.Split(config.thinkingMenu.View(width, max(1, c.height-len(lines))), "\n")
	lines = append(lines, thinkingLines...)
	lines = append(lines, strings.Repeat(" ", width))
	config.runRow = len(lines)
	config.cancelRow = config.runRow
	runLabel := "[ Run Actualize ]  [ Cancel ]"
	switch config.focused {
	case actualizeFocusRun:
		runLabel = "▸ " + runLabel
	case actualizeFocusCancel:
		runLabel = "  [ Run Actualize ]  ▸ [ Cancel ]"
	}
	lines = append(lines, truncatePanelText(runLabel, width))
	for len(lines) < c.height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	if len(lines) > c.height {
		lines = lines[:c.height]
	}
	return strings.Join(lines, "\n")
}

func selectedDropdownItem(items []warp.DropdownItem) int {
	for index, item := range items {
		if item.Selected {
			return index
		}
	}
	return 0
}

func (c *ContextPanel) handleNotesActualizeConfigMouse(msg tea.MouseMsg) tea.Cmd {
	config := c.notesActualizeConfig
	if config == nil || msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return nil
	}
	if msg.Y == config.runRow {
		if msg.X < 18 {
			return c.startNotesActualize(config.options)
		}
		if msg.X >= 19 {
			c.cancelNotesActualizeConfig()
			return nil
		}
	}
	if menuRowContains(msg.Y, config.modelRow, config.modelMenu) {
		local := msg
		local.Y -= config.modelRow
		config.modelMenu.Update(local)
		if config.modelMenu.Open {
			config.thinkingMenu.Close()
		}
		return nil
	}
	if menuRowContains(msg.Y, config.thinkingRow, config.thinkingMenu) {
		local := msg
		local.Y -= config.thinkingRow
		config.thinkingMenu.Update(local)
		if config.thinkingMenu.Open {
			config.modelMenu.Close()
		}
		return nil
	}
	if !menuRowContains(msg.Y, config.modelRow, config.modelMenu) && !menuRowContains(msg.Y, config.thinkingRow, config.thinkingMenu) {
		config.modelMenu.Close()
		config.thinkingMenu.Close()
	}
	return nil
}

func menuRowContains(row, start int, menu *warp.DropdownMenu) bool {
	if menu == nil {
		return false
	}
	count := 1
	if menu.Open {
		count += len(menu.Items)
	}
	return row >= start && row < start+count
}

func (c *ContextPanel) handleNotesActualize(msg notesActualizeMsg) {
	key := notesProjectKey(msg.profile, msg.domain)
	delete(c.notesActualizeRunning, key)
	if cancel := c.notesActualizeCancel[key]; cancel != nil {
		cancel()
		delete(c.notesActualizeCancel, key)
	}
	if msg.result.Committed {
		if c.notesReader != nil {
			c.notesReader.Invalidate(msg.profile, msg.domain)
		}
		c.notesUndoIDs[key] = msg.result.UndoID
	}
	if msg.undo && (msg.err == nil || msg.undoID != "") {
		if c.notesReader != nil {
			c.notesReader.Invalidate(msg.profile, msg.domain)
		}
		delete(c.notesUndoIDs, key)
	}
	if msg.profile != c.profile || msg.domain != c.domain {
		return
	}
	status := c.notesStatus
	c.refresh()
	if msg.generation != c.notesActualizeGeneration {
		c.notesStatus = status
		return
	}
	if msg.err != nil {
		if msg.undo && msg.undoID != "" {
			c.notesStatus = "⚠ Undo restored; durability warning"
			return
		}
		if msg.undo {
			c.notesStatus = "✗ Undo failed: " + msg.err.Error()
		} else {
			c.notesStatus = "✗ Actualize failed: " + msg.err.Error()
		}
		return
	}
	if msg.undo {
		c.notesStatus = "✓ Undo complete"
		return
	}
	c.notesStatus = fmt.Sprintf("✓ Actualized %d → %d", msg.result.BeforeCount, msg.result.AfterCount)
	if msg.result.Warning != nil {
		c.notesStatus += "; backup warning"
	}
}

func (c *ContextPanel) cancelNotesActualizations() {
	c.notesActualizeConfig = nil
	for key, cancel := range c.notesActualizeCancel {
		cancel()
		delete(c.notesActualizeCancel, key)
		delete(c.notesActualizeRunning, key)
	}
}

func notesProjectKey(profile, domain string) string {
	return profile + "\x00" + domain
}
