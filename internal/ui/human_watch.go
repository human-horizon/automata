package ui

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/HumanHorizon/automata/internal/ai-knowledge/human"
	"github.com/HumanHorizon/automata/internal/paths"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/fsnotify/fsnotify"
)

type humanProjectionReader func(profile, sessionID string) (*human.Data, error)

type humanProjectionWatch struct {
	profile      string
	sessionID    string
	active       bool
	generation   uint64
	request      uint64
	readPending  bool
	readAgain    bool
	watcher      *fsnotify.Watcher
	watchPath    string
	watchPending bool
	watchError   string
	reader       humanProjectionReader
}

type humanProjectionLoadedMsg struct {
	target     *humanChat
	generation uint64
	request    uint64
	data       *human.Data
	err        error
}

type humanProjectionChangedMsg struct {
	target     *humanChat
	generation uint64
	watcher    *fsnotify.Watcher
	err        error
}

func (h *humanChat) activate(profile, sessionID string) tea.Cmd {
	profile = paths.ProfileSlug(profile)
	if h.active && h.profile == profile && h.sessionID == sessionID {
		if h.watcher == nil {
			previousGeneration := h.generation
			h.setupWatcher()
			if h.watcher == nil {
				return nil
			}
			if previousGeneration != h.generation {
				h.readPending, h.readAgain = false, false
			}
			return tea.Batch(h.refreshCommand(), h.watchCommand())
		}
		return h.watchCommand()
	}
	if h.profile != profile || h.sessionID != sessionID {
		h.reset()
		clear(h.retiredEpochs)
	}
	h.profile, h.sessionID, h.active = profile, sessionID, true
	h.generation++
	h.setupWatcher()
	return tea.Batch(h.refreshCommand(), h.watchCommand())
}

func (h *humanChat) deactivate() {
	h.active = false
	h.closeWatcher()
	h.readPending, h.readAgain = false, false
}

func (h *humanChat) reset() {
	if h.data != nil {
		h.retireEpoch(h.data.Epoch)
	}
	h.deactivate()
	h.data, h.diagnostic, h.fallback = nil, "", ""
	h.selected, h.listOffset, h.detailOffset = "", 0, 0
	h.followList, h.followDetail = true, false
	h.rendered = false
	clear(h.textCache)
}

func (h *humanChat) refreshCommand() tea.Cmd {
	if !h.active {
		return nil
	}
	if h.readPending {
		h.readAgain = true
		return nil
	}
	h.readPending = true
	h.request++
	reader := h.reader
	if reader == nil {
		reader = human.ReadForProfile
	}
	profile, sessionID, generation, request := h.profile, h.sessionID, h.generation, h.request
	return func() tea.Msg {
		data, err := reader(profile, sessionID)
		return humanProjectionLoadedMsg{target: h, generation: generation, request: request, data: data, err: err}
	}
}

func (h *humanChat) acceptLoaded(msg humanProjectionLoadedMsg) tea.Cmd {
	if !h.active || msg.target != h || msg.generation != h.generation || msg.request != h.request {
		return nil
	}
	h.readPending = false
	if msg.err != nil {
		h.diagnostic = humanText(msg.err.Error())
		h.rendered = false
	} else if msg.data == nil {
		h.diagnostic = "экспорт Human недоступен"
		h.rendered = false
	} else if _, retired := h.retiredEpochs[msg.data.Epoch]; retired {
		h.diagnostic = "устаревший epoch Human"
		h.rendered = false
	} else if h.data != nil && h.data.Epoch == msg.data.Epoch && humanRevisionBefore(msg.data, h.data) {
		h.diagnostic = "устаревшая revision Human"
		h.rendered = false
	} else {
		if h.data != nil && h.data.Epoch != msg.data.Epoch {
			h.retireEpoch(h.data.Epoch)
			h.selected, h.listOffset, h.detailOffset = "", 0, 0
			h.followList, h.followDetail = true, false
			clear(h.textCache)
		}
		h.data, h.diagnostic = msg.data, ""
	}
	if h.readAgain {
		h.readAgain = false
		return h.refreshCommand()
	}
	return nil
}

func humanRevisionBefore(candidate, current *human.Data) bool {
	return candidate.HistoryRevision < current.HistoryRevision ||
		candidate.LiveRevision < current.LiveRevision ||
		candidate.Input.Revision < current.Input.Revision
}

func (h *humanChat) retireEpoch(epoch string) {
	if epoch == "" {
		return
	}
	if h.retiredEpochs == nil {
		h.retiredEpochs = make(map[string]struct{})
	}
	h.retiredEpochs[epoch] = struct{}{}
}

func (h *humanChat) setupWatcher() {
	if !h.active {
		return
	}
	directory := human.Directory(h.profile, h.sessionID)
	if directory == "" {
		h.watchError = "небезопасная идентичность Human"
		return
	}
	watchPath, err := humanWatchDirectory(directory, paths.BaseDir())
	if err != nil {
		h.watchError = humanText(err.Error())
		return
	}
	if h.watcher != nil && h.watchPath == watchPath {
		return
	}
	h.closeWatcher()
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		h.watchError = humanText(err.Error())
		return
	}
	if err := watcher.Add(watchPath); err != nil {
		closeErr := watcher.Close()
		h.watchError = humanText(errors.Join(err, closeErr).Error())
		return
	}
	h.watcher, h.watchPath, h.watchError = watcher, watchPath, ""
}

func (h *humanChat) closeWatcher() {
	h.generation++
	if h.watcher != nil {
		if err := h.watcher.Close(); err != nil {
			h.watchError = humanText(err.Error())
			log.Printf("automata: close Human watcher for %q: %v", h.sessionID, err)
		}
	}
	h.watcher, h.watchPath, h.watchPending = nil, "", false
}

func (h *humanChat) watchCommand() tea.Cmd {
	if !h.active || h.watcher == nil || h.watchPending {
		return nil
	}
	h.watchPending = true
	watcher, generation := h.watcher, h.generation
	directory := human.Directory(h.profile, h.sessionID)
	return func() tea.Msg {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return humanProjectionChangedMsg{target: h, generation: generation, watcher: watcher, err: fs.ErrClosed}
				}
				name := filepath.Clean(event.Name)
				relevant := strings.HasPrefix(directory+string(filepath.Separator), name+string(filepath.Separator))
				if filepath.Dir(name) == directory {
					base := filepath.Base(name)
					relevant = base == "history.json" || base == "live.json" || base == "input.json"
				}
				if relevant {
					return humanProjectionChangedMsg{target: h, generation: generation, watcher: watcher}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					err = fs.ErrClosed
				}
				return humanProjectionChangedMsg{target: h, generation: generation, watcher: watcher, err: err}
			}
		}
	}
}

func (h *humanChat) acceptChanged(msg humanProjectionChangedMsg) tea.Cmd {
	if !h.active || msg.target != h || msg.generation != h.generation || msg.watcher == nil || msg.watcher != h.watcher {
		return nil
	}
	h.watchPending = false
	previousGeneration := h.generation
	if msg.err != nil {
		h.closeWatcher()
		h.watchError = humanText(msg.err.Error())
	}
	h.setupWatcher()
	if previousGeneration != h.generation {
		h.readPending, h.readAgain = false, false
	}
	return tea.Batch(h.refreshCommand(), h.watchCommand())
}

// humanWatchDirectory finds the closest existing directory inside the owned
// data root and rejects symlink ancestors before registering an observer.
func humanWatchDirectory(directory, root string) (string, error) {
	directory, root = filepath.Clean(directory), filepath.Clean(root)
	if directory == root || !strings.HasPrefix(directory, root+string(filepath.Separator)) {
		return "", errors.New("human watcher path is outside the data root")
	}
	nearest := ""
	for candidate := directory; ; candidate = filepath.Dir(candidate) {
		info, err := os.Lstat(candidate)
		if err == nil {
			if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
				return "", fmt.Errorf("human watcher ancestor %q is not a real directory", candidate)
			}
			if nearest == "" {
				nearest = candidate
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("inspect Human watcher ancestor: %w", err)
		}
		if candidate == root {
			break
		}
	}
	if nearest == "" {
		return "", errors.New("human data root is missing")
	}
	return nearest, nil
}

func (cp *ChatPanel) activateHuman() tea.Cmd {
	if !cp.active || cp.activeIdx < 0 || cp.activeIdx >= len(cp.sessions) {
		return nil
	}
	session := cp.sessions[cp.activeIdx]
	if session == nil || !session.humanEnabled {
		return nil
	}
	if session.human == nil {
		session.human = newHumanChat()
	}
	sessionID := cp.sessionID
	if session.familiarID != "" {
		sessionID = session.familiarID
	}
	return session.human.activate(cp.profile, sessionID)
}

func (cp *ChatPanel) deactivateHuman() {
	for _, session := range cp.sessions {
		if session != nil && session.human != nil {
			session.human.deactivate()
		}
	}
}

func (cp *ChatPanel) handleHumanMessage(msg tea.Msg) (tea.Cmd, bool) {
	var target *humanChat
	switch msg := msg.(type) {
	case humanProjectionLoadedMsg:
		target = msg.target
	case humanProjectionChangedMsg:
		target = msg.target
	default:
		return nil, false
	}
	for _, session := range cp.sessions {
		if session == nil || session.human == nil || session.human != target {
			continue
		}
		switch msg := msg.(type) {
		case humanProjectionLoadedMsg:
			return target.acceptLoaded(msg), true
		case humanProjectionChangedMsg:
			return target.acceptChanged(msg), true
		}
	}
	return nil, true
}
