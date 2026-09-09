package ui

import (
	"github.com/Starframe/portalis"
	tea "github.com/charmbracelet/bubbletea"
	warp "github.com/starframe-dev/warp"
)

// TermPanel adapts a portalis.Emulator to the warp.Panel interface.
type TermPanel struct {
	em *portalis.Emulator
}

// NewTermPanel wraps a portalis emulator into a warp panel.
func NewTermPanel(em *portalis.Emulator) *TermPanel {
	return &TermPanel{em: em}
}

// View renders the terminal at the given size.
func (p *TermPanel) View(width, height int) string {
	return p.em.View(width, height)
}

// Update forwards messages to the emulator, translating warp.ResizeMsg to
// portalis.ResizeMsg.
func (p *TermPanel) Update(msg tea.Msg) tea.Cmd {
	if r, ok := msg.(warp.ResizeMsg); ok {
		msg = portalis.ResizeMsg{Width: r.Width, Height: r.Height}
	}
	return p.em.Update(msg)
}

// Start spawns the PTY process.
func (p *TermPanel) Start() tea.Cmd {
	return p.em.Start()
}

// SetEm atomically replaces the underlying emulator. Used by clearSessionCmd
// to swap in a freshly-started PTY without restarting the surrounding panel.
// Does not call Stop or Start — the caller is responsible for lifecycle.
func (p *TermPanel) SetEm(em *portalis.Emulator) {
	p.em = em
}

var _ warp.Panel = (*TermPanel)(nil)
