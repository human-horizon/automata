package ui

import (
	apptheme "github.com/HumanHorizon/automata/internal/theme"
	"github.com/charmbracelet/lipgloss"
)

type kanbanStyles struct {
	columns         []lipgloss.Style
	title           lipgloss.Style
	item            lipgloss.Style
	cardHover       lipgloss.Style
	cardPath        lipgloss.Style
	assigned        lipgloss.Style
	substatus       lipgloss.Style
	delete          lipgloss.Style
	transit         lipgloss.Style
	transitHover    lipgloss.Style
	button          lipgloss.Style
	buttonHover     lipgloss.Style
	border          lipgloss.Style
	background      lipgloss.Style
	backgroundHover lipgloss.Style
}

func newKanbanStyles(palette apptheme.Theme) kanbanStyles {
	buttonText := lipgloss.Color(palette.SelectionForeground)
	return kanbanStyles{
		columns: []lipgloss.Style{
			lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextMuted)),
			lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Warning)),
			lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextMuted)),
			lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Lime)),
		},
		title:           lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(palette.TextStrong)),
		item:            lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Text)),
		cardHover:       lipgloss.NewStyle().Padding(0, 1).Background(lipgloss.Color(palette.Raised)),
		cardPath:        lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextDim)),
		assigned:        lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextDim)).Padding(0, 1),
		substatus:       lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextMuted)).Italic(true).Padding(0, 1),
		delete:          lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Error)).Bold(true),
		transit:         lipgloss.NewStyle().Foreground(buttonText).Background(lipgloss.Color(palette.Raised)).Padding(0, 1),
		transitHover:    lipgloss.NewStyle().Foreground(buttonText).Background(lipgloss.Color(palette.Border)).Bold(true).Padding(0, 1),
		button:          lipgloss.NewStyle().Foreground(buttonText).Background(lipgloss.Color(palette.Raised)).Bold(true).Padding(0, 1),
		buttonHover:     lipgloss.NewStyle().Foreground(buttonText).Background(lipgloss.Color(palette.Border)).Bold(true).Padding(0, 1),
		border:          lipgloss.NewStyle().Foreground(lipgloss.Color(palette.BorderMuted)),
		background:      lipgloss.NewStyle(),
		backgroundHover: lipgloss.NewStyle().Background(lipgloss.Color(palette.Surface)),
	}
}

func (k *KanbanPanel) styles() kanbanStyles {
	return newKanbanStyles(k.palette)
}
