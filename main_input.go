package main

import (
	tea "github.com/charmbracelet/bubbletea"
)

func (a *App) handleKeyMsg(msg tea.KeyMsg) tea.Cmd {
	// Ctrl+C is the application exit shortcut. Handle it before the focus
	// router so chat, terminal, and overlays cannot consume it.
	if msg.Type == tea.KeyCtrlC || msg.String() == "ctrl+c" {
		return tea.Quit
	}
	if a.appModal != nil {
		switch msg.Type {
		case tea.KeyEsc, tea.KeyEnter, tea.KeyF1:
			a.closeAppOverlay()
		}
		return nil
	}
	if a.appPopover != nil {
		if msg.Type == tea.KeyF1 {
			a.closeAppOverlay()
			return nil
		}
		a.appPopover.HandleKey(msg)
		return nil
	}
	if (msg.Type == tea.KeyF1 || msg.String() == "f1") && a.tree != nil {
		a.openHelpOverlay()
		return nil
	}
	if msg.Type == tea.KeyF6 {
		a.setFocusArea(focusTree)
		return nil
	}
	if (msg.Type == tea.KeyF5 || msg.Type == tea.KeyF7) &&
		(a.tree == nil || !a.tree.IsModalOpen()) {
		if msg.Type == tea.KeyF5 {
			a.cycleFocus(-1)
		} else {
			a.cycleFocus(1)
		}
		return nil
	}
	if msg.String() == "f8" {
		a.mouseEnabled = !a.mouseEnabled
		if a.mouseEnabled {
			return enableMouse()
		}
		return disableMouse()
	}
	if a.currentFocusArea() != focusTree && a.container != nil {
		return a.container.Update(msg)
	}
	_, cmd := a.warp.Update(msg)
	return cmd
}

func (a *App) handleMouseMsg(msg tea.MouseMsg) tea.Cmd {
	if a.appModal != nil {
		a.appModal.EnsureDimensions(a.warp.Width(), a.warp.Height())
		a.appModal.HandleMouse(msg)
		return nil
	}
	if a.appPopover != nil {
		a.appPopover.HandleMouse(msg)
		return nil
	}
	if !a.mouseEnabled {
		// Mouse disabled — skip to allow text selection.
		return nil
	}
	_, cmd := a.warp.Update(msg)
	return cmd
}
