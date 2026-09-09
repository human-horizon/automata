package main

import (
	"strings"

	apptheme "github.com/HumanHorizon/automata/internal/theme"
	"github.com/HumanHorizon/automata/internal/tree"
	warp "github.com/starframe-dev/warp"
)

const settingsOverlayWidth = 42

func (a *App) closeAppOverlay() {
	a.appModal = nil
	a.appPopover = nil
}

func (a *App) openHelpOverlay() {
	if a.appModal != nil {
		a.closeAppOverlay()
		return
	}
	a.appPopover = nil
	a.appModal = warp.NewModal(
		"Keyboard help",
		tree.KeyboardHelpText(),
		[]warp.ModalButton{{Label: "Close", Action: a.closeAppOverlay}},
		a.closeAppOverlay,
	)
}

func (a *App) openSettingsOverlay() {
	if a.appPopover != nil {
		a.closeAppOverlay()
		return
	}
	a.appModal = nil

	items := make([]warp.PopoverItem, 0, len(apptheme.All()))
	activeID := ""
	if a.tree != nil {
		activeID = a.tree.ThemeID()
	}
	for _, item := range apptheme.All() {
		item := item
		label := item.Name
		if item.ID == activeID {
			label = "✓ " + label
		}
		items = append(items, warp.PopoverItem{
			Name: label,
			Action: func() {
				if a.tree != nil {
					a.tree.SetTheme(item.ID)
				}
				a.closeAppOverlay()
			},
		})
	}

	width := settingsOverlayWidth
	if a.warp.Width() > 0 && width > a.warp.Width() {
		width = a.warp.Width()
	}
	x := 0
	if a.warp.Width() > width {
		x = (a.warp.Width() - width) / 2
	}
	y := 0
	if a.warp.Height() > len(items)+2 {
		y = (a.warp.Height() - len(items) - 2) / 2
	}
	a.appPopover = &warp.Popover{
		Items:   items,
		X:       x,
		Y:       y,
		Width:   width,
		OnClose: a.closeAppOverlay,
	}
}

func (a *App) renderAppOverlay(base string) string {
	if a == nil || a.warp == nil || (a.appModal == nil && a.appPopover == nil) {
		return base
	}
	width := a.warp.Width()
	height := a.warp.Height()
	if width <= 0 || height <= 0 {
		return base
	}

	lines := strings.Split(base, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}

	if a.appPopover != nil {
		lines = a.appPopover.Overlay(lines, width, height)
	}
	if a.appModal != nil {
		lines = a.appModal.Overlay(lines, width, height)
	}
	return strings.Join(lines, "\n")
}
