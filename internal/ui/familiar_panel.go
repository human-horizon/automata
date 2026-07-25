package ui

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Starframe/portalis"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	warp "github.com/starframe-dev/warp"
)

// FamiliarPanel displays a tmux pane in real-time using tmux control mode.
// It spawns `tmux -f /dev/null -C attach-session -t <tmuxID>`, reads the
// `%output <pane-id> <data>` lines from the control stream, and feeds the
// terminal data through a portalis Screen/Parser. A goroutine pushes data
// into dataCh; the bubbletea loop pulls from it via readFromChannel().
type FamiliarPanel struct {
	tmuxID string

	// Process state.
	cmd    *exec.Cmd
	stdinW *os.File // kept open so tmux control mode doesn't exit on EOF
	cancel func()

	// Pane identification (from %output lines).
	paneIDMu sync.RWMutex
	paneID   string

	// Screen state.
	screenMu sync.RWMutex
	screen   *portalis.Screen
	parser   *portalis.Parser
	width    int
	height   int

	// Channels.
	dataCh chan []byte
	stopCh chan struct{}
}

type familiarOutputMsg struct {
	sessionID string
	data      []byte
}

type familiarPaneIDMsg struct {
	id string
}

// familiarFlushMsg is sent ~30ms after the last output chunk to commit the
// pending frame (synchronized output — ESC[?2026h/l).
type familiarFlushMsg struct{}

// scheduleFlush returns a tea.Cmd that emits a flush message after a short
// delay. When the next output chunk arrives before the delay elapses, the
// pending flush is effectively replaced (only the latest one matters) —
// but since bubbletea runs both, we just commit the latest frame.
func (p *FamiliarPanel) scheduleFlush() tea.Cmd {
	return tea.Tick(30*time.Millisecond, func(t time.Time) tea.Msg {
		return familiarFlushMsg{}
	})
}

// NewFamiliarPanel creates a new panel that attaches to the given tmux
// session via control mode.
func NewFamiliarPanel(tmuxID string) *FamiliarPanel {
	return &FamiliarPanel{
		tmuxID: tmuxID,
		dataCh: make(chan []byte, 256),
		stopCh: make(chan struct{}),
	}
}

// Start spawns the tmux control mode process and the reader goroutine.
// It also creates a default-sized screen so View() can render immediately,
// even before the pane ID is known.
func (p *FamiliarPanel) Start() tea.Cmd {
	log.Printf("[familiar] Start tmuxID=%q", p.tmuxID)

	// If the backing tmux session is dead, recreate it with bash so the
	// user has something to interact with. Without this, control mode
	// reports "no sessions" and exits immediately.
	if err := exec.Command("tmux", "has-session", "-t", p.tmuxID).Run(); err != nil {
		log.Printf("[familiar] tmux session %q missing, recreating with bash", p.tmuxID)
		// Start a detached session running bash. The user can then run
		// whatever they want inside it.
		if err := exec.Command("tmux", "new-session", "-d", "-s", p.tmuxID, "bash").Run(); err != nil {
			log.Printf("[familiar] new-session err=%v", err)
		}
		// Belt-and-braces: also reset per-session options so the new
		// session doesn't inherit any green status bar.
		for _, opt := range [][]string{{"status", "off"}, {"status-style", "bg=default,fg=default"}} {
			_ = exec.Command("tmux", append([]string{"set-option", "-t", p.tmuxID}, opt...)...).Run()
		}
	}

	// Schedule async pane-ID discovery. tmux control mode does not push
	// the pane ID on attach — it only sends %output events when the
	// pane emits data, which never happens for idle panes. We query
	// the pane ID ourselves via list-panes and feed the initial screen
	// content via capture-pane.
	go func() {
		time.Sleep(150 * time.Millisecond)
		out, err := exec.Command("tmux", "list-panes", "-t", p.tmuxID, "-F", "#{pane_id}").Output()
		if err != nil {
			log.Printf("[familiar] list-panes err=%v", err)
			return
		}
		pid := strings.TrimSpace(string(out))
		// first pane only
		if i := strings.IndexAny(pid, "\n"); i > 0 {
			pid = pid[:i]
		}
		if pid == "" {
			return
		}
		log.Printf("[familiar] discovered paneID=%q", pid)
		p.paneIDMu.Lock()
		if p.paneID == "" {
			p.paneID = pid
		}
		p.paneIDMu.Unlock()
		select {
		case p.dataCh <- append([]byte("__PANE__"), pid...):
		case <-p.stopCh:
			return
		}
	}()

	// Explicitly disable status-bar options on the target session BEFORE
	// attaching. -f /dev/null prevents loading the user config but tmux
	// still applies the built-in default status-line, which shows a green
	// strip at the bottom. Forcing it off per-session keeps the pane clean.
	opts := [][]string{
		{"status", "off"},
		{"status-style", "bg=default,fg=default"},
		{"status-interval", "0"},
		{"monitor-activity", "off"},
		{"pane-border-status", "off"},
		{"pane-border-lines", "single"},
		{"clock-mode-colour", "default"},
		{"display-panes-colour", "default"},
	}
	for _, opt := range opts {
		args := append([]string{"set-option", "-t", p.tmuxID}, opt...)
		if out, err := exec.Command("tmux", args...).CombinedOutput(); err != nil {
			log.Printf("[familiar] set-option %v err=%v out=%q", opt, err, string(out))
		}
	}
	// Same options for the global server defaults so new panes inherit
	// clean styling too.
	globalOpts := [][]string{
		{"status", "off"},
		{"status-style", "bg=default,fg=default"},
	}
	for _, opt := range globalOpts {
		args := append([]string{"set-option", "-g"}, opt...)
		if out, err := exec.Command("tmux", args...).CombinedOutput(); err != nil {
			log.Printf("[familiar] set-option -g %v err=%v out=%q", opt, err, string(out))
		}
	}

	// tmux -f /dev/null: empty config disables user overrides.
	cmd := exec.Command("tmux", "-f", "/dev/null", "-C", "attach-session", "-t", p.tmuxID)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Printf("[familiar] Start: stdout pipe err=%v", err)
		return nil
	}
	// Keep stdin open — tmux control mode exits immediately when stdin
	// reaches EOF (e.g. /dev/null). Hold a writer to a pipe open for
	// the lifetime of the process.
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		log.Printf("[familiar] Start: stdin pipe err=%v", err)
		return nil
	}
	cmd.Stdin = stdinR
	if err := cmd.Start(); err != nil {
		stdinR.Close()
		stdinW.Close()
		log.Printf("[familiar] Start: cmd.Start err=%v", err)
		return nil
	}
	p.cmd = cmd
	p.stdinW = stdinW

	// Re-apply status-off after attach in case the session options got
	// reverted during attach handshake.
	go func() {
		time.Sleep(200 * time.Millisecond)
		for _, opt := range []string{
			"status off",
			"status-style bg=default,fg=default",
			"pane-border-status off",
		} {
			_ = exec.Command("tmux", "set-option", "-t", p.tmuxID, "-q", opt).Run()
		}
	}()

	// Only seed a default-sized screen if addFamiliar hasn't already
	// sized us via ResizeMsg.
	if p.screen == nil {
		p.ensureScreen(24, 80)
	}

	go p.readControlMode(stdout)
	log.Printf("[familiar] Start: launched pid=%d", cmd.Process.Pid)
	return p.readFromChannel()
}

// Stop terminates the tmux process and signals the goroutine to exit.
func (p *FamiliarPanel) Stop() {
	select {
	case <-p.stopCh:
		// already stopped
	default:
		close(p.stopCh)
	}
	if p.stdinW != nil {
		_ = p.stdinW.Close()
		p.stdinW = nil
	}
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
		_, _ = p.cmd.Process.Wait()
		p.cmd = nil
	}
}

// ensureScreen creates the Screen/Parser pair at the given size if it
// does not exist yet, or resizes the existing one.
func (p *FamiliarPanel) ensureScreen(rows, cols int) {
	p.screenMu.Lock()
	defer p.screenMu.Unlock()
	if p.screen == nil {
		p.screen = portalis.NewScreen(rows, cols)
		p.parser = portalis.NewParser(p.screen)
	} else {
		p.screen.Resize(rows, cols)
	}
}

// readControlMode reads lines from the tmux control mode stdout and pushes
// %output data into dataCh. Other control mode lines (begin/end markers,
// session info, etc.) are ignored.
func (p *FamiliarPanel) readControlMode(r io.Reader) {
	fmt.Fprintf(os.Stderr, "[familiar] readControlMode started\n")
	scanner := bufio.NewScanner(r)
	// Control mode lines can be large; allow up to 1 MB.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()

		// Capture pane ID from any control-mode line that mentions one.
		if p.getPaneID() == "" {
			if paneID := extractPaneID(line); paneID != "" {
				p.paneIDMu.Lock()
				p.paneID = paneID
				select {
				case p.dataCh <- append([]byte("__PANE__"), paneID...):
				case <-p.stopCh:
					p.paneIDMu.Unlock()
					return
				}
				p.paneIDMu.Unlock()
			}
		}

		if !bytes.HasPrefix(line, []byte("%output ")) {
			continue
		}
		// %output <pane-id> <data...>
		sp1 := bytes.IndexByte(line, ' ')
		if sp1 < 0 {
			continue
		}
		sp2 := bytes.IndexByte(line[sp1+1:], ' ')
		if sp2 < 0 {
			continue
		}
		paneID := string(line[sp1+1 : sp1+1+sp2])
		data := line[sp1+1+sp2+1:]

		// Capture pane ID on first output (in case extractPaneID missed).
		p.paneIDMu.Lock()
		if p.paneID == "" {
			p.paneID = paneID
			select {
			case p.dataCh <- append([]byte("__PANE__"), paneID...):
			case <-p.stopCh:
				p.paneIDMu.Unlock()
				return
			}
		}
		p.paneIDMu.Unlock()

		// Forward the terminal data to the bubbletea loop.
		select {
		case p.dataCh <- append([]byte(nil), data...):
		case <-p.stopCh:
			return
		}
	}
}

// extractPaneID returns the first pane ID mentioned in a control-mode line.
// Handles %session-changed, %layouts-changed, and %output lines.
// Returns "" if no pane ID is found.
func extractPaneID(line []byte) string {
	// %session-changed $0 x__test  → pane IDs are not directly here; need to
	// query tmux. Skip for now.
	if bytes.HasPrefix(line, []byte("%session-changed ")) {
		// Format: %session-changed <session-id> <session-name>
		// To get pane IDs we'd need another command. Skip — wait for %output.
		return ""
	}
	if bytes.HasPrefix(line, []byte("%output ")) {
		sp1 := bytes.IndexByte(line, ' ')
		if sp1 < 0 {
			return ""
		}
		sp2 := bytes.IndexByte(line[sp1+1:], ' ')
		if sp2 < 0 {
			return ""
		}
		return string(line[sp1+1 : sp1+1+sp2])
	}
	// %pane-resized, etc. — not currently parsed.
	return ""
}

// readFromChannel returns a tea.Cmd that blocks until either data arrives
// on dataCh or the panel is stopped.
func (p *FamiliarPanel) readFromChannel() tea.Cmd {
	return func() tea.Msg {
		select {
		case data := <-p.dataCh:
			if bytes.HasPrefix(data, []byte("__PANE__")) {
				return familiarPaneIDMsg{id: string(data[8:])}
			}
			return familiarOutputMsg{sessionID: p.tmuxID, data: data}
		case <-p.stopCh:
			return nil
		}
	}
}

// paneID returns the captured pane ID (empty string if not yet seen).
func (p *FamiliarPanel) getPaneID() string {
	p.paneIDMu.RLock()
	defer p.paneIDMu.RUnlock()
	return p.paneID
}

// View renders the screen at the requested size. The screen is kept at the
// pane's logical size (Cols x Rows); lines are padded or truncated to fit
// the panel using ANSI-aware padding.
func (p *FamiliarPanel) View(width, height int) string {
	p.screenMu.RLock()
	defer p.screenMu.RUnlock()
	if width <= 0 || height <= 0 {
		return ""
	}
	if p.screen == nil {
		// Lazy fallback: render an empty box if the screen wasn't initialised
		// (e.g. Start failed and no ResizeMsg arrived yet).
		var b strings.Builder
		for r := 0; r < height; r++ {
			if r > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(strings.Repeat(" ", width))
		}
		return b.String()
	}
	result := renderFamiliarScreen(p.screen, width, height)
	log.Printf("[familiar] View rows=%d cols=%d panel=%dx%d result_len=%d first_line=%q",
		p.screen.Rows, p.screen.Cols, width, height, len(result),
		strings.SplitN(result, "\n", 2)[0])
	return result
}

// Update handles bubbletea messages: data from the control mode stream,
// resize events, and pane ID updates.
func (p *FamiliarPanel) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case warp.ResizeMsg:
		p.width = msg.Width
		p.height = msg.Height
		p.ensureScreen(msg.Height, msg.Width)
		// Try to resize the tmux pane to match our panel.
		if pid := p.getPaneID(); pid != "" {
			_ = exec.Command("tmux", "resize-pane", "-t", pid,
				"-x", strconv.Itoa(msg.Width),
				"-y", strconv.Itoa(msg.Height)).Run()
		}
		return nil

	case tea.KeyMsg:
		// Forward keystrokes to the tmux pane.
		if pid := p.getPaneID(); pid != "" {
			p.sendKey(pid, msg)
		}
		return nil

	case familiarPaneIDMsg:
		// If addFamiliar already set our panel size via ResizeMsg, resize the
		// tmux pane to match. Otherwise query the tmux pane size.
		if p.width <= 0 || p.height <= 0 {
			if out, err := exec.Command("tmux", "display", "-p", "-t", msg.id,
				"#{pane_width} #{pane_height}").Output(); err == nil {
				fields := strings.Fields(strings.TrimSpace(string(out)))
				if len(fields) == 2 {
					w, _ := strconv.Atoi(fields[0])
					h, _ := strconv.Atoi(fields[1])
					if w > 0 && h > 0 {
						p.width, p.height = w, h
						p.ensureScreen(h, w)
					}
				}
			}
		} else {
			// Ensure our screen matches the panel size first.
			p.ensureScreen(p.height, p.width)
			_ = exec.Command("tmux", "resize-pane", "-t", msg.id,
				"-x", strconv.Itoa(p.width),
				"-y", strconv.Itoa(p.height)).Run()
		}
		// Capture current pane content as initial state. tmux control mode
		// does not push existing content on attach — only new output.
		// Capture exactly the screen's worth of lines so the Portalis
		// screen stays at the correct size and we don't push content
		// into Portalis scrollback (which would render as a wall of
		// raw ANSI escape sequences).
		rows := p.height
		if rows <= 0 {
			rows = 24
		}
		if out, err := exec.Command("tmux", "capture-pane", "-p", "-J",
			"-S", fmt.Sprintf("-%d", rows), "-t", msg.id).Output(); err == nil {
			snapshot := string(out)
			log.Printf("[familiar] initial capture %d bytes, first 200: %q", len(snapshot), snapshot[:min(len(snapshot), 200)])
			p.screenMu.Lock()
			if p.parser != nil {
				p.parser.Feed([]byte(snapshot))
				p.screen.ResetView()
			}
			p.screenMu.Unlock()
		}
		// Continue reading the data stream.
		return p.readFromChannel()

	case familiarOutputMsg:
		p.screenMu.Lock()
		if p.parser != nil {
			p.parser.Feed(msg.data)
		}
		p.screenMu.Unlock()
		// Continue reading.
		return p.readFromChannel()
	}
	return nil
}

// sendKey forwards a tea.KeyMsg to the tmux pane via `send-keys`.
// Maps common special keys to their tmux names and supports Ctrl/Alt
// modifiers. Plain runes are sent as literal text.
func (p *FamiliarPanel) sendKey(paneID string, msg tea.KeyMsg) {
	// Special keys first — tmux has named tokens for these.
	switch msg.Type {
	case tea.KeyEnter:
		_ = exec.Command("tmux", "send-keys", "-t", paneID, "Enter").Run()
		return
	case tea.KeyEsc:
		_ = exec.Command("tmux", "send-keys", "-t", paneID, "Escape").Run()
		return
	case tea.KeyTab:
		_ = exec.Command("tmux", "send-keys", "-t", paneID, "Tab").Run()
		return
	case tea.KeyBackspace:
		_ = exec.Command("tmux", "send-keys", "-t", paneID, "BSpace").Run()
		return
	case tea.KeyDelete:
		_ = exec.Command("tmux", "send-keys", "-t", paneID, "DC").Run()
		return
	case tea.KeyUp:
		_ = exec.Command("tmux", "send-keys", "-t", paneID, "Up").Run()
		return
	case tea.KeyDown:
		_ = exec.Command("tmux", "send-keys", "-t", paneID, "Down").Run()
		return
	case tea.KeyLeft:
		_ = exec.Command("tmux", "send-keys", "-t", paneID, "Left").Run()
		return
	case tea.KeyRight:
		_ = exec.Command("tmux", "send-keys", "-t", paneID, "Right").Run()
		return
	case tea.KeyHome:
		_ = exec.Command("tmux", "send-keys", "-t", paneID, "Home").Run()
		return
	case tea.KeyEnd:
		_ = exec.Command("tmux", "send-keys", "-t", paneID, "End").Run()
		return
	case tea.KeyPgUp:
		_ = exec.Command("tmux", "send-keys", "-t", paneID, "PPage").Run()
		return
	case tea.KeyPgDown:
		_ = exec.Command("tmux", "send-keys", "-t", paneID, "NPage").Run()
		return
	case tea.KeySpace:
		_ = exec.Command("tmux", "send-keys", "-t", paneID, "Space").Run()
		return
	}

	// Runes (plain text + Ctrl/Alt combos).
	if msg.Paste {
		_ = exec.Command("tmux", "send-keys", "-t", paneID, "-l", msg.String()).Run()
		return
	}

	// Build modifier-prefixed key name for non-text keys (e.g. C-c, M-x).
	name := tmuxKeyName(msg)
	if name != "" {
		_ = exec.Command("tmux", "send-keys", "-t", paneID, name).Run()
		return
	}

	// Fallback: send literal text.
	if msg.String() != "" {
		_ = exec.Command("tmux", "send-keys", "-t", paneID, "-l", msg.String()).Run()
	}
}

// tmuxKeyName maps a tea.KeyMsg to a tmux send-keys key name for
// non-rune keys (Ctrl/Alt combos, function keys, etc.). Returns "" if
// the key should be sent as literal text.
func tmuxKeyName(msg tea.KeyMsg) string {
	r := msg.Runes
	if len(r) == 0 {
		return ""
	}
	// Single rune + Ctrl modifier → C-x
	if len(r) == 1 && r[0] >= 'a' && r[0] <= 'z' {
		return "C-" + string(r[0])
	}
	// Alt/Meta + letter → M-x
	if len(r) == 1 && r[0] >= 'a' && r[0] <= 'z' {
		// tea doesn't have explicit Alt; it's encoded in the rune sometimes
	}
	return ""
}

// renderFamiliarScreen renders the screen content as plain lines (one line
// per row), trimmed/padded to fit width × height. Reads from s.Cells
// directly to avoid the synchronized-output Render() path which was
// suppressing the initial frame.
func renderFamiliarScreen(s *portalis.Screen, width, height int) string {
	if s == nil {
		if height <= 0 || width <= 0 {
			return ""
		}
		var b strings.Builder
		for r := 0; r < height; r++ {
			if r > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(strings.Repeat(" ", width))
		}
		return b.String()
	}
	// Render each row from s.Cells directly, with ANSI colour sequences.
	// This avoids s.Render() which may include control sequences that
	// bubbletea's terminal renderer shows as literal text.
	var b strings.Builder
	for r := 0; r < height; r++ {
		if r > 0 {
			b.WriteByte('\n')
		}
		if r < s.Rows {
			b.WriteString(screenLinePlain(s, r, width))
		} else {
			b.WriteString(strings.Repeat(" ", width))
		}
	}
	return b.String()
}

// screenLinePlain returns the visible cells of a single row as a string
// (with ANSI colour sequences). Empty cells render as spaces. Continuation
// cells (wide character tails) are skipped.
func screenLinePlain(s *portalis.Screen, row, cols int) string {
	if row < 0 || row >= s.Rows {
		return ""
	}
	cells := s.Cells[row]
	var b strings.Builder
	var lastFG, lastBG string
	var lastStyle byte
	resetStyle := func() {
		if lastFG != "" || lastBG != "" || lastStyle != 0 {
			b.WriteString("\x1b[0m")
			lastFG, lastBG, lastStyle = "", "", 0
		}
	}
	for c := 0; c < cols; c++ {
		var rn rune = ' '
		var fg, bg string
		var style byte
		if c < len(cells) {
			cell := cells[c]
			if cell.Continuation {
				// Wide character tail — skip, already rendered by base cell.
				continue
			}
			if cell.Rune != 0 {
				rn = cell.Rune
			}
			fg = string(cell.FG)
			bg = string(cell.BG)
			style = byte(cell.Style)
		}
		if fg != lastFG || bg != lastBG || style != lastStyle {
			resetStyle()
			seq := styleSeq(fg, bg, style)
			if seq != "" {
				b.WriteString(seq)
				lastFG, lastBG, lastStyle = fg, bg, style
			}
		}
		b.WriteRune(rn)
		// Append combining characters (accents, variation selectors, etc.)
		if c < len(cells) && cells[c].Combining != "" {
			b.WriteString(cells[c].Combining)
		}
	}
	resetStyle()
	return b.String()
}

// styleSeq builds the SGR escape for the given style. Matches Portalis
// renderStyle + sgrColor: handles true color (#RRGGBB), 256-color (numeric
// string), and all StyleBits flags.
func styleSeq(fg, bg string, style byte) string {
	if fg == "" && bg == "" && style == 0 {
		return ""
	}
	parts := []string{"0"}
	if style&1 != 0 {
		parts = append(parts, "1") // bold
	}
	if style&2 != 0 {
		parts = append(parts, "2") // dim
	}
	if style&4 != 0 {
		parts = append(parts, "3") // italic
	}
	if style&8 != 0 {
		parts = append(parts, "4") // underline
	}
	if style&16 != 0 {
		parts = append(parts, "5") // blink
	}
	if style&64 != 0 {
		parts = append(parts, "7") // reverse
	}
	if style&128 != 0 {
		parts = append(parts, "8") // hidden
	}
	if style&32 != 0 {
		parts = append(parts, "9") // strikethrough
	}
	if fg != "" {
		parts = append(parts, sgrColorStr(fg, false))
	}
	if bg != "" {
		parts = append(parts, sgrColorStr(bg, true))
	}
	return "\x1b[" + strings.Join(parts, ";") + "m"
}

// sgrColorStr converts a lipgloss.Color string to an SGR color parameter.
// Matches Portalis sgrColor: handles #RRGGBB (true color) and numeric
// strings (256-color index). Returns empty string for unrecognized formats.
func sgrColorStr(c string, bg bool) string {
	base := 38
	if bg {
		base = 48
	}
	if strings.HasPrefix(c, "#") && len(c) == 7 {
		r, _ := strconv.ParseInt(c[1:3], 16, 0)
		g, _ := strconv.ParseInt(c[3:5], 16, 0)
		b, _ := strconv.ParseInt(c[5:7], 16, 0)
		return fmt.Sprintf("%d;2;%d;%d;%d", base, r, g, b)
	}
	// Assume numeric 256-color index.
	if _, err := strconv.Atoi(c); err == nil {
		return fmt.Sprintf("%d;5;%s", base, c)
	}
	return ""
}

// padOrTruncANSI pads (with spaces) or truncates an ANSI-styled string so
// its visible width matches target. Uses ansi.Truncate for ANSI-safe
// truncation and ansi.StringWidth for width measurement.
func padOrTruncANSI(s string, target int) string {
	if target <= 0 {
		return ""
	}
	visible := ansi.StringWidth(s)
	if visible >= target {
		return ansi.Truncate(s, target, "")
	}
	return s + strings.Repeat(" ", target-visible)
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

// countLines counts the number of lines in s (lines separated by '\n').
// Returns at least 1 even for an empty string.
func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// Ensure FamiliarPanel satisfies the warp.Panel interface.
var _ warp.Panel = (*FamiliarPanel)(nil)
