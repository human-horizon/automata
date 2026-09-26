package ui

import (
	"bufio"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"github.com/HumanHorizon/automata/internal/scrollback"
	"github.com/Starframe/portalis"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	warp "github.com/starframe-dev/warp"
)

// tmuxOutputMsg carries raw terminal data from the tmux control client.
type tmuxOutputMsg struct {
	data []byte
}

// TmuxPanePanel reads a tmux pane's content in real-time via tmux control mode
// (-C attach-session). Uses portalis Screen/Parser for ANSI rendering.
type TmuxPanePanel struct {
	tmuxID          string
	screen          *portalis.Screen
	parser          *portalis.Parser
	width           int
	height          int
	started         bool
	scrollbackLimit int

	mu      sync.Mutex
	cmd     *exec.Cmd
	scanner *bufio.Scanner
	dataCh  chan []byte
	stopCh  chan struct{}
}

// NewTmuxPanePanel creates a real-time tmux pane viewer with the default scrollback limit.
func NewTmuxPanePanel(tmuxID string) *TmuxPanePanel {
	return &TmuxPanePanel{
		tmuxID:          tmuxID,
		scrollbackLimit: scrollback.DefaultLines,
		dataCh:          make(chan []byte, 64),
		stopCh:          make(chan struct{}),
	}
}

// NewTmuxPanePanelWithScrollbackLimit creates a viewer with a configured history limit.
func NewTmuxPanePanelWithScrollbackLimit(tmuxID string, limit int) (*TmuxPanePanel, error) {
	if err := scrollback.Validate(limit); err != nil {
		return nil, err
	}
	panel := NewTmuxPanePanel(tmuxID)
	panel.scrollbackLimit = limit
	return panel, nil
}

// SetScrollbackLimit updates the viewer and any Screen already in use.
func (p *TmuxPanePanel) SetScrollbackLimit(limit int) error {
	if err := scrollback.Validate(limit); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.scrollbackLimit = limit
	if p.screen != nil {
		p.screen.SetScrollbackLimit(limit)
	}
	return nil
}

// View renders the captured tmux pane content.
func (p *TmuxPanePanel) View(width, height int) string {
	p.width = width
	p.height = height

	p.mu.Lock()
	screen := p.screen
	p.mu.Unlock()

	if screen == nil {
		return strings.Repeat("\n", height)
	}

	rendered := screen.Render()
	lines := strings.Split(rendered, "\n")

	// Trim or pad to fit panel height. Pad at top so content sits at bottom.
	if len(lines) > height {
		lines = lines[len(lines)-height:]
	}
	for len(lines) < height {
		lines = append([]string{""}, lines...)
	}

	// Truncate or pad each line to panel width (ANSI-aware).
	for i, line := range lines {
		w := ansi.StringWidth(line)
		if w > width {
			line = ansi.Truncate(line, width, "")
		} else if w < width {
			line = line + strings.Repeat(" ", width-w)
		}
		lines[i] = line + "\x1b[0m"
	}

	return strings.Join(lines, "\n")
}

// Update handles messages for the TmuxPanePanel.
func (p *TmuxPanePanel) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tmuxOutputMsg:
		p.mu.Lock()
		if p.parser != nil {
			p.parser.Feed(msg.data)
		}
		p.mu.Unlock()
		// Continue reading from channel.
		return p.readFromChannel()

	case tea.KeyMsg:
		return p.sendKey(msg)

	case warp.ResizeMsg:
		p.width = msg.Width
		p.height = msg.Height
		p.mu.Lock()
		if p.screen != nil {
			p.screen.Resize(p.height, p.width)
		}
		p.mu.Unlock()
		// Resize the tmux pane to match.
		if p.width > 0 && p.height > 0 {
			exec.Command("tmux", "resize-pane", "-t", p.tmuxID, "-x", fmt.Sprintf("%d", p.width), "-y", fmt.Sprintf("%d", p.height)).Run()
		}
		if !p.started {
			p.started = true
			return p.startControlMode()
		}

	default:
		if !p.started {
			p.started = true
			return p.startControlMode()
		}
	}

	return nil
}

// readFromChannel returns a command that reads one chunk from the data channel.
func (p *TmuxPanePanel) readFromChannel() tea.Cmd {
	return func() tea.Msg {
		select {
		case data := <-p.dataCh:
			return tmuxOutputMsg{data: data}
		case <-p.stopCh:
			return nil
		}
	}
}

// startControlMode spawns a tmux control client that reads real-time output.
func (p *TmuxPanePanel) startControlMode() tea.Cmd {
	return func() tea.Msg {
		// Initialize screen/parser with default size.
		rows, cols := 24, 80
		if p.height > 0 {
			rows = p.height
		}
		if p.width > 0 {
			cols = p.width
		}

		p.mu.Lock()
		p.screen = portalis.NewScreen(rows, cols)
		p.screen.SetScrollbackLimit(p.scrollbackLimit)
		p.parser = portalis.NewParser(p.screen)
		p.mu.Unlock()

		// First, resize the tmux pane to match panel size.
		if p.width > 0 && p.height > 0 {
			exec.Command("tmux", "resize-pane", "-t", p.tmuxID, "-x", fmt.Sprintf("%d", p.width), "-y", fmt.Sprintf("%d", p.height)).Run()
		}

		// Capture the current pane content.
		out, err := exec.Command("tmux", "capture-pane", "-p", "-e", "-t", p.tmuxID).Output()
		if err == nil {
			p.mu.Lock()
			p.parser.Feed(out)
			p.mu.Unlock()
		}

		// Spawn tmux control mode for real-time updates.
		// Use empty config to disable tmux status bar (green bar).
		cmd := exec.Command("tmux", "-f", "/dev/null", "-C", "attach-session", "-t", p.tmuxID)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil
		}
		if err := cmd.Start(); err != nil {
			return nil
		}

		p.mu.Lock()
		p.cmd = cmd
		p.scanner = bufio.NewScanner(stdout)
		p.scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		p.mu.Unlock()

		// Read output in a goroutine, send data through channel.
		go p.readLoop()

		// Return a command to start reading from the channel.
		return nil
	}
}

// readLoop reads tmux control output and sends data through the channel.
func (p *TmuxPanePanel) readLoop() {
	p.mu.Lock()
	scanner := p.scanner
	p.mu.Unlock()

	if scanner == nil {
		return
	}

	for scanner.Scan() {
		line := scanner.Text()
		// Parse tmux control mode output.
		// %output <pane-id> <base64-data>
		if strings.HasPrefix(line, "%output ") {
			parts := strings.SplitN(line, " ", 3)
			if len(parts) >= 3 {
				data := []byte(parts[2])
				// Send to channel for processing in bubbletea loop.
				select {
				case p.dataCh <- data:
				case <-p.stopCh:
					return
				}
			}
		}
	}

	// Scanner finished.
	p.mu.Lock()
	p.cmd = nil
	p.scanner = nil
	p.mu.Unlock()
}

// sendKey sends a keystroke to the tmux session.
func (p *TmuxPanePanel) sendKey(msg tea.KeyMsg) tea.Cmd {
	return func() tea.Msg {
		data := keyToTmuxBytes(msg)
		if len(data) == 0 {
			return nil
		}
		exec.Command("tmux", "send-keys", "-t", p.tmuxID, string(data)).Run()
		return nil
	}
}

// keyToTmuxBytes converts a tea.KeyMsg to raw bytes for tmux send-keys.
func keyToTmuxBytes(msg tea.KeyMsg) []byte {
	if msg.Alt && (msg.Type == tea.KeyBackspace || msg.Type == tea.KeyCtrlH) {
		if msg.Type == tea.KeyCtrlH {
			return []byte("")
		}
		return []byte("")
	}
	switch msg.Type {
	case tea.KeyUp:
		return []byte("\x1b[A")
	case tea.KeyDown:
		return []byte("\x1b[B")
	case tea.KeyRight:
		return []byte("\x1b[C")
	case tea.KeyLeft:
		return []byte("\x1b[D")
	case tea.KeyHome:
		return []byte("\x1b[H")
	case tea.KeyEnd:
		return []byte("\x1b[F")
	case tea.KeyPgUp:
		return []byte("\x1b[5~")
	case tea.KeyPgDown:
		return []byte("\x1b[6~")
	case tea.KeyDelete:
		return []byte("\x1b[3~")
	case tea.KeyInsert:
		return []byte("\x1b[2~")
	case tea.KeyTab:
		return []byte("\t")
	case tea.KeyShiftTab:
		return []byte("\x1b[Z")
	case tea.KeyEnter:
		return []byte("\r")
	case tea.KeyBackspace:
		return []byte("\x7f")
	case tea.KeyEscape:
		return []byte("\x1b")
	case tea.KeySpace:
		return []byte(" ")
	case tea.KeyCtrlC:
		return []byte("\x03")
	case tea.KeyCtrlD:
		return []byte("\x04")
	case tea.KeyCtrlL:
		return []byte("\x0c")
	case tea.KeyCtrlZ:
		return []byte("\x1a")
	case tea.KeyRunes:
		return []byte(string(msg.Runes))
	}
	return nil
}

// Ensure TmuxPanePanel implements warp.Panel.
var _ warp.Panel = (*TmuxPanePanel)(nil)
