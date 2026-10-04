package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/HumanHorizon/automata/internal/ai-knowledge/human"
	"github.com/HumanHorizon/automata/internal/paths"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
)

const (
	schemaVersion   = uint64(1)
	historyName     = "history.json"
	liveName        = "live.json"
	inputName       = "input.json"
	inputLogName    = "fixture-input.log"
	ptyInputLogName = "fixture-pty-input.bin"

	pasteStart = "\x1b[200~"
	mouseStart = "\x1b[<"
	pasteEnd   = "\x1b[201~"
)

type entryKind string

type activityStatus string

type itemKind string

const (
	entryUser     entryKind = "user"
	entryAnswer   entryKind = "answer"
	entryActivity entryKind = "activity"

	activityWorking activityStatus = "working"
	activityDone    activityStatus = "done"

	itemThinking   itemKind = "thinking"
	itemCommentary itemKind = "commentary"
	itemToolCall   itemKind = "toolCall"
	itemToolResult itemKind = "toolResult"
)

type fixtureEntry struct {
	Kind      entryKind      `json:"kind"`
	ID        string         `json:"id"`
	Text      string         `json:"text,omitempty"`
	Truncated bool           `json:"truncated,omitempty"`
	Status    activityStatus `json:"status,omitempty"`
	Items     []fixtureItem  `json:"items,omitempty"`
}

type fixtureItem struct {
	Kind       itemKind `json:"kind"`
	ID         string   `json:"id"`
	Text       string   `json:"text"`
	ToolCallID string   `json:"toolCallId,omitempty"`
	Name       string   `json:"name,omitempty"`
	IsError    bool     `json:"isError,omitempty"`
}

type historyDocument struct {
	SchemaVersion uint64         `json:"schemaVersion"`
	Profile       string         `json:"profile"`
	SessionID     string         `json:"sessionId"`
	Epoch         string         `json:"epoch"`
	Revision      uint64         `json:"revision"`
	Entries       []fixtureEntry `json:"entries"`
}

type liveDocument struct {
	SchemaVersion   uint64        `json:"schemaVersion"`
	Profile         string        `json:"profile"`
	SessionID       string        `json:"sessionId"`
	Epoch           string        `json:"epoch"`
	Revision        uint64        `json:"revision"`
	HistoryRevision uint64        `json:"historyRevision"`
	Entry           *fixtureEntry `json:"entry"`
}

type fixtureCursor struct {
	Row int `json:"row"`
	Col int `json:"col"`
}

type inputDocument struct {
	SchemaVersion uint64         `json:"schemaVersion"`
	Profile       string         `json:"profile"`
	SessionID     string         `json:"sessionId"`
	Epoch         string         `json:"epoch"`
	Revision      uint64         `json:"revision"`
	Width         int            `json:"width"`
	Lines         []string       `json:"lines"`
	Cursor        *fixtureCursor `json:"cursor"`
	Native        bool           `json:"native"`
}

type fixtureState struct {
	profile       string
	sessionID     string
	directory     string
	epoch         string
	width         int
	draft         string
	native        bool
	history       historyDocument
	inputRevision uint64
	liveRevision  uint64
	tempSequence  uint64
}

type inputRead struct {
	data []byte
	err  error
}

type inputEvent struct {
	text   []byte
	escape bool
	paste  bool
}

type inputDecoder struct {
	pending     []byte
	pasting     bool
	escapeSince time.Time
}

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "fake Pi: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if os.Getenv("AUTOMATA_HUMAN_EXPORT") != "1" {
		return errors.New("AUTOMATA_HUMAN_EXPORT must be 1")
	}
	profile := os.Getenv("AUTOMATA_PROFILE")
	if profile == "" || profile != paths.ProfileSlug(profile) {
		return errors.New("AUTOMATA_PROFILE is missing or not canonical")
	}
	if os.Getenv("AI_DATA_HOME") == "" {
		return errors.New("AI_DATA_HOME is missing")
	}
	sessionID, err := parseSessionID(os.Args[1:])
	if err != nil {
		return err
	}
	if err := paths.ValidateSessionID(sessionID); err != nil {
		return fmt.Errorf("invalid --session-id: %w", err)
	}
	width, _, err := term.GetSize(os.Stdin.Fd())
	if err != nil {
		return fmt.Errorf("read initial PTY size: %w", err)
	}
	if width < 1 {
		return fmt.Errorf("invalid initial PTY width %d", width)
	}
	state, err := newFixtureState(profile, sessionID, width)
	if err != nil {
		return err
	}
	rawState, err := term.MakeRaw(os.Stdin.Fd())
	if err != nil {
		return fmt.Errorf("set fake Pi PTY raw mode: %w", err)
	}
	defer func() { _ = term.Restore(os.Stdin.Fd(), rawState) }()

	if err := state.publishInitial(); err != nil {
		return err
	}
	state.drawNative("FAKE_PI_READY")

	resizeAndStop := make(chan os.Signal, 4)
	signal.Notify(resizeAndStop, syscall.SIGWINCH, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(resizeAndStop)

	reads := make(chan inputRead, 4)
	go readPTY(reads)

	decoder := inputDecoder{}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case received := <-reads:
			if len(received.data) > 0 {
				if err := state.writeRawInput(received.data); err != nil {
					return err
				}
				for _, event := range decoder.feed(received.data) {
					if err := state.handleInput(event); err != nil {
						return err
					}
				}
			}
			if received.err != nil {
				if errors.Is(received.err, io.EOF) {
					return nil
				}
				return fmt.Errorf("read fake Pi PTY: %w", received.err)
			}
		case receivedSignal := <-resizeAndStop:
			if receivedSignal == syscall.SIGWINCH {
				width, _, err := term.GetSize(os.Stdin.Fd())
				if err != nil {
					return fmt.Errorf("read resized PTY size: %w", err)
				}
				if width < 1 {
					return fmt.Errorf("invalid resized PTY width %d", width)
				}
				state.width = width
				if err := state.publishInput(); err != nil {
					return err
				}
				continue
			}
			return nil
		case <-ticker.C:
			if decoder.flushEscape(time.Now()) {
				if err := state.handleInput(inputEvent{escape: true}); err != nil {
					return err
				}
			}
		}
	}
}

func parseSessionID(args []string) (string, error) {
	for index := 0; index < len(args); index++ {
		if args[index] != "--session-id" {
			continue
		}
		if index+1 >= len(args) || args[index+1] == "" {
			return "", errors.New("--session-id requires a value")
		}
		return args[index+1], nil
	}
	return "", errors.New("--session-id was not provided")
}

func newFixtureState(profile, sessionID string, width int) (*fixtureState, error) {
	directory := human.Directory(profile, sessionID)
	if directory == "" {
		return nil, errors.New("profile or session ID is not safe for a Human projection")
	}
	if err := paths.EnsurePrivateDir(directory); err != nil {
		return nil, fmt.Errorf("create private Human projection directory: %w", err)
	}
	userText := "UserVisibleMarker: Inspect the local repository. MainOnlyUserMarker"
	answerText := "AnswerVisibleMarker: The local check is complete. MainOnlyAnswerMarker"
	if strings.HasSuffix(sessionID, "__fixture-familiar") {
		userText = "FamiliarOnlyUserMarker: Inspect the isolated familiar fixture."
		answerText = "FamiliarOnlyAnswerMarker: The familiar fixture is complete."
	}
	state := &fixtureState{
		profile:   profile,
		sessionID: sessionID,
		directory: directory,
		epoch:     "fake-pi-" + strconv.Itoa(os.Getpid()),
		width:     width,
		history: historyDocument{
			SchemaVersion: schemaVersion,
			Profile:       profile,
			SessionID:     sessionID,
			Epoch:         "fake-pi-" + strconv.Itoa(os.Getpid()),
			Revision:      1,
			Entries: []fixtureEntry{
				{Kind: entryUser, ID: "user-1", Text: userText},
				{
					Kind:   entryActivity,
					ID:     "activity-chain-1",
					Status: activityDone,
					Items: []fixtureItem{
						{Kind: itemThinking, ID: "thinking-1", Text: "ThinkingSecretMarker: I will inspect the file."},
						{Kind: itemCommentary, ID: "commentary-1", Text: "CommentarySecretMarker: Checking the project tree."},
						{Kind: itemToolCall, ID: "call-1", ToolCallID: "tool-call-1", Name: "read", Text: "ToolCallOneSecretMarker: README.md"},
						{Kind: itemToolResult, ID: "result-1", ToolCallID: "tool-call-1", Name: "read", Text: "ToolResultOneSecretMarker: local fixture data"},
						{Kind: itemThinking, ID: "thinking-2", Text: "ThinkingSecondSecretMarker: I will verify the match."},
						{Kind: itemToolCall, ID: "call-2", ToolCallID: "tool-call-2", Name: "grep", Text: "ToolCallTwoSecretMarker: TODO"},
						{Kind: itemToolResult, ID: "result-2", ToolCallID: "tool-call-2", Name: "grep", Text: "ToolResultTwoSecretMarker: one result"},
					},
				},
				{Kind: entryAnswer, ID: "answer-1", Text: answerText},
			},
		},
		inputRevision: 1,
		liveRevision:  1,
	}
	return state, nil
}

func (state *fixtureState) publishInitial() error {
	if err := state.writeHistory(); err != nil {
		return err
	}
	if err := state.writeLive(nil, state.history.Revision); err != nil {
		return err
	}
	return state.writeInput(state.width)
}

func (state *fixtureState) writeHistory() error {
	data, err := json.Marshal(state.history)
	if err != nil {
		return fmt.Errorf("encode Human history: %w", err)
	}
	if err := state.writePrivateJSON(historyName, data); err != nil {
		return fmt.Errorf("publish Human history: %w", err)
	}
	return nil
}

func (state *fixtureState) writeLive(entry *fixtureEntry, historyRevision uint64) error {
	state.liveRevision++
	document := liveDocument{
		SchemaVersion:   schemaVersion,
		Profile:         state.profile,
		SessionID:       state.sessionID,
		Epoch:           state.epoch,
		Revision:        state.liveRevision,
		HistoryRevision: historyRevision,
		Entry:           entry,
	}
	data, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode Human live state: %w", err)
	}
	if err := state.writePrivateJSON(liveName, data); err != nil {
		return fmt.Errorf("publish Human live state: %w", err)
	}
	return nil
}

func (state *fixtureState) writeInput(width int) error {
	lines, cursor := makeInputFrame(state.draft, width)
	document := inputDocument{
		SchemaVersion: schemaVersion,
		Profile:       state.profile,
		SessionID:     state.sessionID,
		Epoch:         state.epoch,
		Revision:      state.inputRevision,
		Width:         width,
		Lines:         lines,
		Cursor:        &cursor,
		Native:        state.native,
	}
	data, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode Human input frame: %w", err)
	}
	if err := state.writePrivateJSON(inputName, data); err != nil {
		return fmt.Errorf("publish Human input frame: %w", err)
	}
	return nil
}

func (state *fixtureState) publishInput() error {
	return state.publishInputWithWidth(state.width)
}

func (state *fixtureState) publishInputWithWidth(width int) error {
	if width < 1 {
		return fmt.Errorf("invalid Human input width %d", width)
	}
	state.inputRevision++
	return state.writeInput(width)
}

func (state *fixtureState) writePrivateJSON(name string, data []byte) error {
	state.tempSequence++
	temporary := filepath.Join(state.directory, fmt.Sprintf(".%s.%d.%d.tmp", name, os.Getpid(), state.tempSequence))
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create temporary %s: %w", name, err)
	}
	if err := file.Chmod(0o600); err != nil {
		return cleanupTemporary(file, temporary, fmt.Errorf("set private mode on %s: %w", name, err))
	}
	if _, err := file.Write(data); err != nil {
		return cleanupTemporary(file, temporary, fmt.Errorf("write %s: %w", name, err))
	}
	if err := file.Sync(); err != nil {
		return cleanupTemporary(file, temporary, fmt.Errorf("sync %s: %w", name, err))
	}
	if err := file.Close(); err != nil {
		return errors.Join(fmt.Errorf("close %s: %w", name, err), os.Remove(temporary))
	}
	if err := os.Rename(temporary, filepath.Join(state.directory, name)); err != nil {
		return errors.Join(fmt.Errorf("replace %s: %w", name, err), os.Remove(temporary))
	}
	directory, err := os.Open(state.directory)
	if err != nil {
		return fmt.Errorf("open Human directory after %s: %w", name, err)
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil || closeErr != nil {
		return errors.Join(fmt.Errorf("sync Human directory after %s: %w", name, syncErr), closeErr)
	}
	return nil
}

func cleanupTemporary(file *os.File, path string, cause error) error {
	return errors.Join(cause, file.Close(), os.Remove(path))
}

func (state *fixtureState) writeRawInput(data []byte) error {
	path := filepath.Join(state.directory, ptyInputLogName)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open raw PTY input log: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		return errors.Join(fmt.Errorf("set raw PTY input log permissions: %w", err), file.Close())
	}
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return errors.Join(writeErr, syncErr, closeErr)
	}
	return nil
}

func (state *fixtureState) writeInputLog(value string) error {
	path := filepath.Join(state.directory, inputLogName)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open fixture input log: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		return errors.Join(fmt.Errorf("set input log permissions: %w", err), file.Close())
	}
	_, writeErr := io.WriteString(file, value+"\n")
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return errors.Join(writeErr, syncErr, closeErr)
	}
	return nil
}

func (state *fixtureState) handleInput(event inputEvent) error {
	if event.escape {
		if !state.native {
			return nil
		}
		state.native = false
		state.draft = ""
		if err := state.publishInput(); err != nil {
			return err
		}
		state.drawNative("FAKE_PI_EDITOR_ACTIVE")
		return nil
	}
	if state.native {
		return nil
	}
	for _, character := range string(event.text) {
		switch character {
		case '\r', '\n':
			if event.paste {
				state.draft += "\n"
				if err := state.publishInput(); err != nil {
					return err
				}
				continue
			}
			if err := state.submit(); err != nil {
				return err
			}
		case '\b', '\x7f':
			runes := []rune(state.draft)
			if len(runes) > 0 {
				state.draft = string(runes[:len(runes)-1])
			}
			if err := state.publishInput(); err != nil {
				return err
			}
		default:
			if unicode.IsControl(character) {
				continue
			}
			state.draft += string(character)
			if err := state.publishInput(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (state *fixtureState) submit() error {
	value := state.draft
	state.draft = ""
	if err := state.writeInputLog(value); err != nil {
		return err
	}
	if err := state.publishInput(); err != nil {
		return err
	}
	switch value {
	case "/live":
		return state.startSecondChain()
	case "/finish":
		return state.finishSecondChain()
	case "/historical-live":
		return state.publishClosedHistoricalActivity()
	case "/stale-frame":
		if err := state.publishInputWithWidth(state.width + 1); err != nil {
			return err
		}
		state.drawNative("FAKE_PI_STALE_FRAME")
		return nil
	case "/settings":
		state.native = true
		if err := state.publishInput(); err != nil {
			return err
		}
		state.drawNative("FAKE_PI_NATIVE_DIALOG")
		return nil
	case "/corrupt":
		if err := state.writePrivateJSON(historyName, []byte("{corrupt Human history")); err != nil {
			return err
		}
		state.drawNative("FAKE_PI_HISTORY_CORRUPTED")
		return nil
	case "/mismatch":
		if err := state.writeLive(nil, state.history.Revision+1); err != nil {
			return err
		}
		state.drawNative("FAKE_PI_MISMATCH_PUBLISHED")
		return nil
	default:
		_, err := fmt.Fprintf(os.Stdout, "\r\nFAKE_PI_RECEIVED:%s\r\n", strings.ReplaceAll(value, "\n", "\\n"))
		return err
	}
}

func (state *fixtureState) startSecondChain() error {
	if len(state.history.Entries) != 3 || state.history.Entries[2].Kind != entryAnswer {
		return errors.New("/live requires the initial answer-delimited chain")
	}
	state.history.Entries = append(state.history.Entries, fixtureEntry{
		Kind: entryUser,
		ID:   "user-2",
		Text: "SecondUserVisibleMarker: Continue after the first answer.",
	})
	state.history.Revision++
	if err := state.writeHistory(); err != nil {
		return err
	}
	activity := secondChainActivity(activityWorking)
	return state.writeLive(&activity, state.history.Revision)
}

func (state *fixtureState) finishSecondChain() error {
	if len(state.history.Entries) != 4 || state.history.Entries[3].ID != "user-2" {
		return errors.New("/finish requires /live to have started chain-2")
	}
	state.history.Entries = append(state.history.Entries,
		secondChainActivity(activityDone),
		fixtureEntry{
			Kind: entryAnswer,
			ID:   "answer-2",
			Text: "SecondAnswerVisibleMarker: The follow-up is complete.",
		},
	)
	state.history.Revision++
	if err := state.writeHistory(); err != nil {
		return err
	}
	return state.writeLive(nil, state.history.Revision)
}

func (state *fixtureState) publishClosedHistoricalActivity() error {
	if len(state.history.Entries) < 3 || state.history.Entries[1].ID != "activity-chain-1" || state.history.Entries[2].Kind != entryAnswer {
		return errors.New("/historical-live requires the initial closed chain")
	}
	activity := state.history.Entries[1]
	if err := state.writeLive(&activity, state.history.Revision); err != nil {
		return err
	}
	state.drawNative("FAKE_PI_CLOSED_HISTORICAL_LIVE")
	return nil
}

func secondChainActivity(status activityStatus) fixtureEntry {
	return fixtureEntry{
		Kind:   entryActivity,
		ID:     "activity-chain-2",
		Status: status,
		Items: []fixtureItem{
			{Kind: itemThinking, ID: "thinking-chain-2", Text: "ChainTwoThinkingLiveMarker: The follow-up is in progress."},
			{Kind: itemCommentary, ID: "commentary-chain-2", Text: "ChainTwoCommentaryLiveMarker: Checking the second request."},
			{Kind: itemToolCall, ID: "call-chain-2", ToolCallID: "tool-call-chain-2", Name: "read", Text: "ChainTwoToolCallLiveMarker: follow-up.md"},
			{Kind: itemToolResult, ID: "result-chain-2", ToolCallID: "tool-call-chain-2", Name: "read", Text: "ChainTwoToolResultLiveMarker: one follow-up result"},
		},
	}
}

func (state *fixtureState) drawNative(marker string) {
	_, _ = fmt.Fprintf(os.Stdout, "\x1b[2J\x1b[H%s\r\nprofile=%s\r\nsession=%s\r\nType /settings for a native dialog.\r\n", marker, state.profile, state.sessionID)
}

func makeInputFrame(value string, width int) ([]string, fixtureCursor) {
	if width < 1 {
		width = 1
	}
	lines := []string{""}
	cursor := fixtureCursor{}
	for _, character := range value {
		if character == '\r' {
			continue
		}
		if character == '\n' {
			lines = append(lines, "")
			cursor.Row++
			cursor.Col = 0
			continue
		}
		cells := ansi.StringWidth(string(character))
		if cells < 1 {
			cells = 0
		}
		if cursor.Col+cells > width {
			lines = append(lines, "")
			cursor.Row++
			cursor.Col = 0
		}
		lines[cursor.Row] += string(character)
		cursor.Col += cells
		if cursor.Col >= width {
			lines = append(lines, "")
			cursor.Row++
			cursor.Col = 0
		}
	}
	return lines, cursor
}

func readPTY(output chan<- inputRead) {
	buffer := make([]byte, 4096)
	for {
		read, err := os.Stdin.Read(buffer)
		if read > 0 {
			chunk := append([]byte(nil), buffer[:read]...)
			output <- inputRead{data: chunk}
		}
		if err != nil {
			output <- inputRead{err: err}
			return
		}
	}
}

func (decoder *inputDecoder) feed(data []byte) []inputEvent {
	decoder.pending = append(decoder.pending, data...)
	defer func() {
		if len(decoder.pending) > 0 && decoder.pending[0] == 0x1b {
			if decoder.escapeSince.IsZero() {
				decoder.escapeSince = time.Now()
			}
			return
		}
		decoder.escapeSince = time.Time{}
	}()
	var events []inputEvent
	for len(decoder.pending) > 0 {
		if decoder.pasting {
			end := bytes.Index(decoder.pending, []byte(pasteEnd))
			if end >= 0 {
				if end > 0 {
					chunk := append([]byte(nil), decoder.pending[:end]...)
					events = append(events, inputEvent{text: chunk, paste: true})
				}
				decoder.pending = decoder.pending[end+len(pasteEnd):]
				decoder.pasting = false
				continue
			}
			keep := suffixPrefixLength(decoder.pending, []byte(pasteEnd))
			usable := len(decoder.pending) - keep
			complete := completeUTF8Length(decoder.pending[:usable])
			if complete > 0 {
				events = append(events, inputEvent{text: append([]byte(nil), decoder.pending[:complete]...), paste: true})
				decoder.pending = decoder.pending[complete:]
			}
			return events
		}
		if bytes.HasPrefix(decoder.pending, []byte(mouseStart)) {
			end := bytes.IndexAny(decoder.pending[len(mouseStart):], "Mm")
			if end < 0 {
				return events
			}
			decoder.pending = decoder.pending[len(mouseStart)+end+1:]
			continue
		}
		if isPrefixOf([]byte(mouseStart), decoder.pending) {
			return events
		}
		if bytes.HasPrefix(decoder.pending, []byte(pasteStart)) {
			decoder.pending = decoder.pending[len(pasteStart):]
			decoder.pasting = true
			continue
		}
		if isPrefixOf([]byte(pasteStart), decoder.pending) {
			return events
		}
		if decoder.pending[0] == 0x1b {
			events = append(events, inputEvent{escape: true})
			decoder.pending = decoder.pending[1:]
			continue
		}
		escape := bytes.IndexByte(decoder.pending, 0x1b)
		limit := len(decoder.pending)
		if escape >= 0 {
			limit = escape
		}
		complete := completeUTF8Length(decoder.pending[:limit])
		if complete > 0 {
			events = append(events, inputEvent{text: append([]byte(nil), decoder.pending[:complete]...)})
			decoder.pending = decoder.pending[complete:]
			continue
		}
		return events
	}
	return events
}

func (decoder *inputDecoder) flushEscape(now time.Time) bool {
	if decoder.pasting || len(decoder.pending) != 1 || decoder.pending[0] != 0x1b || decoder.escapeSince.IsZero() || now.Sub(decoder.escapeSince) < 200*time.Millisecond {
		return false
	}
	decoder.pending = nil
	decoder.escapeSince = time.Time{}
	return true
}

func isPrefixOf(whole, prefix []byte) bool {
	return len(prefix) <= len(whole) && bytes.Equal(whole[:len(prefix)], prefix)
}

func suffixPrefixLength(value, target []byte) int {
	limit := min(len(value), len(target)-1)
	for length := limit; length > 0; length-- {
		if bytes.Equal(value[len(value)-length:], target[:length]) {
			return length
		}
	}
	return 0
}

func completeUTF8Length(value []byte) int {
	complete := 0
	for complete < len(value) {
		if !utf8.FullRune(value[complete:]) {
			break
		}
		_, size := utf8.DecodeRune(value[complete:])
		if size < 1 {
			break
		}
		complete += size
	}
	return complete
}
