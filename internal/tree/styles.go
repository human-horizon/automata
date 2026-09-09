package tree

import (
	"github.com/HumanHorizon/automata/internal/theme"
	"github.com/charmbracelet/lipgloss"
)

type treeStyles struct {
	headerStyle          lipgloss.Style
	titleStyle           lipgloss.Style
	collapseStyle        lipgloss.Style
	folderStyle          lipgloss.Style
	chatStyle            lipgloss.Style
	archivedStyle        lipgloss.Style
	selectedStyle        lipgloss.Style
	statusStyle          lipgloss.Style
	activeStyle          lipgloss.Style
	idleStyle            lipgloss.Style
	branchStyle          lipgloss.Style
	emptyStyle           lipgloss.Style
	scrollbarStyle       lipgloss.Style
	modalBorderStyle     lipgloss.Style
	modalTitleStyle      lipgloss.Style
	modalInputStyle      lipgloss.Style
	modalHintStyle       lipgloss.Style
	modalCloseStyle      lipgloss.Style
	dimStyle             lipgloss.Style
	actionIconStyle      lipgloss.Style
	actionIconHoverStyle lipgloss.Style
}

func newTreeStyles(palette theme.Theme) treeStyles {
	return treeStyles{
		headerStyle:    lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextMuted)).Bold(true),
		titleStyle:     lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextStrong)),
		collapseStyle:  lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Text)),
		folderStyle:    lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextStrong)),
		chatStyle:      lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Text)),
		archivedStyle:  lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextDim)),
		selectedStyle:  lipgloss.NewStyle().Background(lipgloss.Color(palette.SelectionBackground)).Foreground(lipgloss.Color(palette.SelectionForeground)),
		statusStyle:    lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextMuted)),
		activeStyle:    lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Lime)),
		idleStyle:      lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextDim)),
		branchStyle:    lipgloss.NewStyle().Foreground(lipgloss.Color(palette.BorderMuted)),
		emptyStyle:     lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextDim)),
		scrollbarStyle: lipgloss.NewStyle().Foreground(lipgloss.Color(palette.BorderMuted)),
		modalBorderStyle: lipgloss.NewStyle().
			Background(lipgloss.Color(palette.Surface)).
			Foreground(lipgloss.Color(palette.TextStrong)).
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(palette.Border)).
			Padding(1, 2),
		modalTitleStyle: lipgloss.NewStyle().
			Background(lipgloss.Color(palette.Surface)).
			Foreground(lipgloss.Color(palette.TextStrong)).
			Bold(true),
		modalInputStyle: lipgloss.NewStyle().
			Background(lipgloss.Color(palette.Background)).
			Foreground(lipgloss.Color(palette.TextStrong)),
		modalHintStyle: lipgloss.NewStyle().
			Background(lipgloss.Color(palette.Surface)).
			Foreground(lipgloss.Color(palette.TextMuted)),
		modalCloseStyle: lipgloss.NewStyle().
			Background(lipgloss.Color(palette.Surface)).
			Foreground(lipgloss.Color(palette.Error)),
		dimStyle:             lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextMuted)),
		actionIconStyle:      lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextMuted)),
		actionIconHoverStyle: lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextStrong)),
	}
}

func (t *Tree) styles() treeStyles {
	return newTreeStyles(theme.Resolve(t.Theme))
}
