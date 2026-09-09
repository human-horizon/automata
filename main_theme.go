package main

import (
	apptheme "github.com/HumanHorizon/automata/internal/theme"
	warp "github.com/starframe-dev/warp"
)

func (a *App) applyTheme(id string) {
	if a == nil || a.container == nil {
		return
	}
	palette := apptheme.Resolve(id)
	warp.SetTheme(warp.ThemeColors{
		Background:          palette.Background,
		Surface:             palette.Surface,
		Raised:              palette.Raised,
		Border:              palette.Border,
		BorderMuted:         palette.BorderMuted,
		Text:                palette.Text,
		TextMuted:           palette.TextMuted,
		TextStrong:          palette.TextStrong,
		Accent:              palette.Purple,
		AccentMuted:         palette.PurpleMuted,
		Error:               palette.Error,
		Success:             palette.Success,
		Warning:             palette.Warning,
		SelectionBackground: palette.SelectionBackground,
		SelectionForeground: palette.SelectionForeground,
	})
	a.container.SetTheme(palette)
}
