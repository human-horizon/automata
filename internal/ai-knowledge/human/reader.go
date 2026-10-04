// Package human reads the validated Human v1 chat projection for one session.
package human

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/HumanHorizon/automata/internal/paths"
)

const (
	directoryName = "human"
	historyName   = "history.json"
	liveName      = "live.json"
	inputName     = "input.json"
	schemaVersion = uint64(1)
	maxIdentifier = 256
)

// EntryKind identifies one already-classified row in the Human projection.
type EntryKind string

const (
	EntryKindUser     EntryKind = "user"
	EntryKindAnswer   EntryKind = "answer"
	EntryKindActivity EntryKind = "activity"
)

// ActivityStatus is the state of a collapsed activity chain.
type ActivityStatus string

const (
	ActivityStatusWorking ActivityStatus = "working"
	ActivityStatusDone    ActivityStatus = "done"
	ActivityStatusAborted ActivityStatus = "aborted"
	ActivityStatusError   ActivityStatus = "error"
)

// ItemKind identifies one render-ready item within an activity chain.
type ItemKind string

const (
	ItemKindThinking   ItemKind = "thinking"
	ItemKindCommentary ItemKind = "commentary"
	ItemKindToolCall   ItemKind = "toolCall"
	ItemKindToolResult ItemKind = "toolResult"
)

// Entry is one user message, final answer, or collapsed activity chain.
type Entry struct {
	Kind      EntryKind
	ID        string
	Text      string
	Truncated bool
	Status    ActivityStatus
	Items     []Item
}

// Item is one ordered, render-ready element within an activity chain.
type Item struct {
	Kind       ItemKind
	ID         string
	Text       string
	ToolCallID string
	Name       string
	IsError    bool
}

// Cursor is a zero-based terminal-cell position in the exported input frame.
type Cursor struct {
	Row int
	Col int
}

// Input is the exported native Pi editor frame.
type Input struct {
	Revision uint64
	Width    int
	Lines    []string
	Cursor   *Cursor
	Native   bool
}

// Data contains one internally consistent Human v1 projection.
type Data struct {
	Epoch           string
	HistoryRevision uint64
	LiveRevision    uint64
	Entries         []Entry
	Input           Input
}

// Directory returns the Human projection directory for a canonical profile and
// session ID. Invalid identities return an empty path; readers and Clear report
// the validation error instead.
func Directory(profile, sessionID string) string {
	if _, err := validateIdentity(profile, sessionID); err != nil {
		return ""
	}
	return filepath.Join(paths.SessionDir(profile, sessionID), directoryName)
}

// ReadForProfile reads and validates the complete Human v1 projection for one
// canonical profile and session. Any missing, unsafe, corrupt, or mismatched
// file is returned as a diagnostic so callers can use the native PTY fallback.
func ReadForProfile(profile, sessionID string) (*Data, error) {
	canonicalProfile, err := validateIdentity(profile, sessionID)
	if err != nil {
		return nil, err
	}
	directory := filepath.Join(paths.SessionDir(canonicalProfile, sessionID), directoryName)
	root, missing, err := openCanonicalRoot(directory)
	if err != nil {
		return nil, fmt.Errorf("inspect Human projection directory: %w", err)
	}
	if missing {
		return nil, fmt.Errorf("human projection directory %q is missing", directory)
	}
	data, readErr := readFS(rootedFS{root: root}, canonicalProfile, sessionID)
	closeErr := root.Close()
	if readErr != nil || closeErr != nil {
		return nil, errors.Join(readErr, wrapCloseError("close Human projection directory", closeErr))
	}
	return data, nil
}

// ClearForProfile removes only the validated human/ directory belonging to the
// requested session. A missing root or projection is already clean.
func ClearForProfile(profile, sessionID string) error {
	canonicalProfile, err := validateIdentity(profile, sessionID)
	if err != nil {
		return err
	}

	sessionDirectory := paths.SessionDir(canonicalProfile, sessionID)
	root, missing, err := openCanonicalRoot(sessionDirectory)
	if err != nil {
		return fmt.Errorf("inspect Human session directory: %w", err)
	}
	if missing {
		return nil
	}
	info, err := root.Lstat(directoryName)
	if errors.Is(err, fs.ErrNotExist) {
		return wrapCloseError("close Human session directory", root.Close())
	}
	if err != nil {
		return errors.Join(fmt.Errorf("inspect Human projection directory: %w", err), wrapCloseError("close Human session directory", root.Close()))
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return errors.Join(fmt.Errorf("human projection path %q is not a real directory", filepath.Join(sessionDirectory, directoryName)), wrapCloseError("close human session directory", root.Close()))
	}
	if err := root.RemoveAll(directoryName); err != nil {
		return errors.Join(fmt.Errorf("remove Human projection directory %q: %w", filepath.Join(sessionDirectory, directoryName), err), wrapCloseError("close Human session directory", root.Close()))
	}
	return wrapCloseError("close Human session directory", root.Close())
}

func validateIdentity(profile, sessionID string) (string, error) {
	if profile == "" {
		profile = "default"
	} else if profile != paths.ProfileSlug(profile) {
		return "", fmt.Errorf("profile %q is not a canonical profile slug", profile)
	}
	if profile == "" {
		return "", errors.New("profile slug is empty")
	}
	for _, r := range profile {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' {
			return "", fmt.Errorf("profile %q contains an unsafe character", profile)
		}
	}
	if err := paths.ValidateSessionID(sessionID); err != nil {
		return "", fmt.Errorf("invalid session ID: %w", err)
	}
	return profile, nil
}

type wireHeader struct {
	SchemaVersion *uint64 `json:"schemaVersion"`
	Profile       *string `json:"profile"`
	SessionID     *string `json:"sessionId"`
	Epoch         *string `json:"epoch"`
	Revision      *uint64 `json:"revision"`
}

type wireHistory struct {
	wireHeader
	Entries *[]wireEntry `json:"entries"`
}

type wireLive struct {
	wireHeader
	HistoryRevision *uint64         `json:"historyRevision"`
	Entry           json.RawMessage `json:"entry"`
}

type wireInput struct {
	wireHeader
	Width  *int            `json:"width"`
	Lines  *[]string       `json:"lines"`
	Cursor json.RawMessage `json:"cursor"`
	Native *bool           `json:"native"`
}

type wireEntry struct {
	Kind      *EntryKind      `json:"kind"`
	ID        *string         `json:"id"`
	Text      *string         `json:"text"`
	Truncated *bool           `json:"truncated"`
	Status    *ActivityStatus `json:"status"`
	Items     *[]wireItem     `json:"items"`
}

type wireItem struct {
	Kind       *ItemKind `json:"kind"`
	ID         *string   `json:"id"`
	Text       *string   `json:"text"`
	ToolCallID *string   `json:"toolCallId"`
	Name       *string   `json:"name"`
	IsError    *bool     `json:"isError"`
}

type wireCursor struct {
	Row *int `json:"row"`
	Col *int `json:"col"`
}

// readFS is the filesystem seam used by tests and the production rooted reader.
func readFS(fsys fs.FS, profile, sessionID string) (*Data, error) {
	canonicalProfile, err := validateIdentity(profile, sessionID)
	if err != nil {
		return nil, err
	}

	historyBytes, err := readCandidate(fsys, historyName)
	if err != nil {
		return nil, err
	}
	liveBytes, err := readCandidate(fsys, liveName)
	if err != nil {
		return nil, err
	}
	inputBytes, err := readCandidate(fsys, inputName)
	if err != nil {
		return nil, err
	}

	var history wireHistory
	if err := decodeStrict(historyBytes, &history); err != nil {
		return nil, fmt.Errorf("decode %s: %w", historyName, err)
	}
	if err := validateHeader(history.wireHeader, canonicalProfile, sessionID, historyName); err != nil {
		return nil, err
	}
	if history.Entries == nil {
		return nil, fmt.Errorf("%s entries must be a JSON array", historyName)
	}
	entries, err := decodeEntries(*history.Entries)
	if err != nil {
		return nil, fmt.Errorf("validate %s entries: %w", historyName, err)
	}
	if err := validateEntries(entries); err != nil {
		return nil, fmt.Errorf("validate %s entries: %w", historyName, err)
	}

	var live wireLive
	if err := decodeStrict(liveBytes, &live); err != nil {
		return nil, fmt.Errorf("decode %s: %w", liveName, err)
	}
	if err := validateHeader(live.wireHeader, canonicalProfile, sessionID, liveName); err != nil {
		return nil, err
	}
	if *live.Epoch != *history.Epoch {
		return nil, fmt.Errorf("%s epoch does not match %s", liveName, historyName)
	}
	if live.HistoryRevision == nil || *live.HistoryRevision != *history.Revision {
		return nil, fmt.Errorf("%s historyRevision does not match %s revision", liveName, historyName)
	}
	var liveEntry *Entry
	if len(live.Entry) == 0 {
		return nil, fmt.Errorf("%s entry field is required", liveName)
	}
	if !bytes.Equal(bytes.TrimSpace(live.Entry), []byte("null")) {
		var raw wireEntry
		if err := decodeStrict(live.Entry, &raw); err != nil {
			return nil, fmt.Errorf("decode %s entry: %w", liveName, err)
		}
		decoded, err := decodeEntry(raw)
		if err != nil {
			return nil, fmt.Errorf("validate %s entry: %w", liveName, err)
		}
		if decoded.Kind != EntryKindActivity {
			return nil, fmt.Errorf("%s entry must be an activity", liveName)
		}
		liveEntry = &decoded
	}
	entries, err = mergeLive(entries, liveEntry)
	if err != nil {
		return nil, fmt.Errorf("merge %s: %w", liveName, err)
	}
	if err := validateEntries(entries); err != nil {
		return nil, fmt.Errorf("validate merged Human entries: %w", err)
	}

	var input wireInput
	if err := decodeStrict(inputBytes, &input); err != nil {
		return nil, fmt.Errorf("decode %s: %w", inputName, err)
	}
	if err := validateHeader(input.wireHeader, canonicalProfile, sessionID, inputName); err != nil {
		return nil, err
	}
	if *input.Epoch != *history.Epoch {
		return nil, fmt.Errorf("%s epoch does not match %s", inputName, historyName)
	}
	if input.Width == nil || *input.Width <= 0 {
		return nil, fmt.Errorf("%s width must be a positive integer", inputName)
	}
	if input.Lines == nil {
		return nil, fmt.Errorf("%s lines must be a JSON array", inputName)
	}
	if input.Native == nil {
		return nil, fmt.Errorf("%s native field is required", inputName)
	}
	if !*input.Native && len(*input.Lines) == 0 {
		return nil, fmt.Errorf("%s non-native frame must contain at least one line", inputName)
	}
	for lineIndex, line := range *input.Lines {
		if strings.ContainsAny(line, "\r\n") {
			return nil, fmt.Errorf("%s line %d contains a line break", inputName, lineIndex)
		}
	}
	if len(input.Cursor) == 0 {
		return nil, fmt.Errorf("%s cursor field is required", inputName)
	}
	var cursor *Cursor
	if !bytes.Equal(bytes.TrimSpace(input.Cursor), []byte("null")) {
		var raw wireCursor
		if err := decodeStrict(input.Cursor, &raw); err != nil {
			return nil, fmt.Errorf("decode %s cursor: %w", inputName, err)
		}
		if raw.Row == nil || raw.Col == nil {
			return nil, fmt.Errorf("%s cursor requires row and col", inputName)
		}
		cursor = &Cursor{Row: *raw.Row, Col: *raw.Col}
		if cursor.Row < 0 || cursor.Row >= len(*input.Lines) || cursor.Col < 0 || cursor.Col >= *input.Width {
			return nil, fmt.Errorf("%s cursor is outside the input frame", inputName)
		}
	}

	lines := make([]string, len(*input.Lines))
	copy(lines, *input.Lines)
	return &Data{
		Epoch:           *history.Epoch,
		HistoryRevision: *history.Revision,
		LiveRevision:    *live.Revision,
		Entries:         entries,
		Input: Input{
			Revision: *input.Revision,
			Width:    *input.Width,
			Lines:    lines,
			Cursor:   cursor,
			Native:   *input.Native,
		},
	}, nil
}

func readCandidate(fsys fs.FS, name string) ([]byte, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read Human projection directory: %w", err)
	}
	for _, entry := range entries {
		if entry.Name() != name {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("inspect %s: %w", name, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s is not a regular file", name)
		}
		var data []byte
		if reader, ok := fsys.(interface {
			ReadRegularFile(string) ([]byte, error)
		}); ok {
			data, err = reader.ReadRegularFile(name)
		} else {
			data, err = fs.ReadFile(fsys, name)
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		return data, nil
	}
	return nil, fmt.Errorf("required Human projection file %s is missing", name)
}

func decodeStrict[T any](data []byte, destination *T) error {
	if !utf8.Valid(data) {
		return errors.New("JSON is not valid UTF-8")
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return fmt.Errorf("trailing JSON data: %w", err)
	}
	return nil
}

func rejectDuplicateKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := scanJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return fmt.Errorf("trailing JSON data: %w", err)
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	switch delimiter {
	case '{':
		keys := make([]string, 0)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			for _, existing := range keys {
				if existing == key {
					return fmt.Errorf("duplicate JSON field %q", key)
				}
			}
			keys = append(keys, key)
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return errors.New("unterminated JSON object")
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return errors.New("unterminated JSON array")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	return nil
}

func validateHeader(header wireHeader, profile, sessionID, filename string) error {
	if header.SchemaVersion == nil || *header.SchemaVersion != schemaVersion {
		return fmt.Errorf("%s has unsupported or missing schemaVersion", filename)
	}
	if header.Profile == nil || *header.Profile != profile {
		return fmt.Errorf("%s profile does not match requested profile", filename)
	}
	if header.SessionID == nil || *header.SessionID != sessionID {
		return fmt.Errorf("%s sessionId does not match requested session", filename)
	}
	if header.Epoch == nil || !validIdentifier(*header.Epoch) {
		return fmt.Errorf("%s epoch is missing or invalid", filename)
	}
	if header.Revision == nil || *header.Revision == 0 {
		return fmt.Errorf("%s revision must be a positive integer", filename)
	}
	return nil
}

func decodeEntries(raw []wireEntry) ([]Entry, error) {
	entries := make([]Entry, len(raw))
	for index := range raw {
		entry, err := decodeEntry(raw[index])
		if err != nil {
			return nil, fmt.Errorf("entry %d: %w", index, err)
		}
		entries[index] = entry
	}
	return entries, nil
}

func decodeEntry(raw wireEntry) (Entry, error) {
	if raw.Kind == nil || raw.ID == nil || !validIdentifier(*raw.ID) {
		return Entry{}, errors.New("entry kind and valid ID are required")
	}
	entry := Entry{Kind: *raw.Kind, ID: *raw.ID}
	switch entry.Kind {
	case EntryKindUser:
		if raw.Text == nil || raw.Truncated != nil || raw.Status != nil || raw.Items != nil {
			return Entry{}, errors.New("user entry requires only text and ID")
		}
		entry.Text = *raw.Text
	case EntryKindAnswer:
		if raw.Text == nil || raw.Status != nil || raw.Items != nil {
			return Entry{}, errors.New("answer entry requires text and cannot have activity fields")
		}
		entry.Text = *raw.Text
		if raw.Truncated != nil {
			entry.Truncated = *raw.Truncated
		}
	case EntryKindActivity:
		if raw.Text != nil || raw.Truncated != nil || raw.Status == nil || raw.Items == nil {
			return Entry{}, errors.New("activity entry requires status and items and cannot have text")
		}
		entry.Status = *raw.Status
		items := make([]Item, len(*raw.Items))
		for index := range *raw.Items {
			item, err := decodeItem((*raw.Items)[index])
			if err != nil {
				return Entry{}, fmt.Errorf("activity item %d: %w", index, err)
			}
			items[index] = item
		}
		entry.Items = items
	default:
		return Entry{}, fmt.Errorf("unknown entry kind %q", entry.Kind)
	}
	return entry, nil
}

func decodeItem(raw wireItem) (Item, error) {
	if raw.Kind == nil || raw.ID == nil || !validIdentifier(*raw.ID) || raw.Text == nil {
		return Item{}, errors.New("item kind, valid ID, and text are required")
	}
	item := Item{Kind: *raw.Kind, ID: *raw.ID, Text: *raw.Text}
	switch item.Kind {
	case ItemKindThinking, ItemKindCommentary:
		if raw.ToolCallID != nil || raw.Name != nil || raw.IsError != nil {
			return Item{}, fmt.Errorf("%s item cannot have tool association fields", item.Kind)
		}
	case ItemKindToolCall:
		if raw.ToolCallID == nil || !validIdentifier(*raw.ToolCallID) || raw.IsError != nil {
			return Item{}, errors.New("toolCall requires a valid toolCallId and cannot be an error result")
		}
		item.ToolCallID = *raw.ToolCallID
		if raw.Name != nil {
			if !validDisplayName(*raw.Name) {
				return Item{}, errors.New("toolCall name is invalid")
			}
			item.Name = *raw.Name
		}
	case ItemKindToolResult:
		if raw.ToolCallID == nil || !validIdentifier(*raw.ToolCallID) {
			return Item{}, errors.New("toolResult requires a valid toolCallId")
		}
		item.ToolCallID = *raw.ToolCallID
		if raw.Name != nil {
			if !validDisplayName(*raw.Name) {
				return Item{}, errors.New("toolResult name is invalid")
			}
			item.Name = *raw.Name
		}
		if raw.IsError != nil {
			item.IsError = *raw.IsError
		}
	default:
		return Item{}, fmt.Errorf("unknown item kind %q", item.Kind)
	}
	return item, nil
}

// Projection identifiers are not filesystem identities. Pi Responses tool IDs
// retain both provider components separated by a pipe.
func validIdentifier(value string) bool {
	if value == "." || value == ".." || len(value) == 0 || len(value) > maxIdentifier {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) || r == '/' || r == '\\' {
			return false
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("._:-|", r) {
			return false
		}
	}
	return true
}

func validDisplayName(value string) bool {
	if value == "" || len(value) > maxIdentifier {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validateEntries(entries []Entry) error {
	seenIDs := make(map[string]struct{}, len(entries))
	toolCalls := make(map[string]bool)
	activitiesSinceAnswer := 0
	for entryIndex, entry := range entries {
		if !validIdentifier(entry.ID) {
			return fmt.Errorf("entry %d has an invalid ID", entryIndex)
		}
		if _, exists := seenIDs[entry.ID]; exists {
			return fmt.Errorf("duplicate entry or item ID %q", entry.ID)
		}
		seenIDs[entry.ID] = struct{}{}
		switch entry.Kind {
		case EntryKindUser:
			if entry.Status != "" || entry.Items != nil || entry.Truncated {
				return fmt.Errorf("user entry %q has fields for another kind", entry.ID)
			}
		case EntryKindAnswer:
			if entry.Status != "" || entry.Items != nil {
				return fmt.Errorf("answer entry %q has fields for another kind", entry.ID)
			}
			activitiesSinceAnswer = 0
		case EntryKindActivity:
			if entry.Text != "" || !validActivityStatus(entry.Status) {
				return fmt.Errorf("activity entry %q has invalid text or status", entry.ID)
			}
			activitiesSinceAnswer++
			if activitiesSinceAnswer > 1 {
				return errors.New("multiple activity entries occur in one answer-delimited chain")
			}
			if err := validateActivityItems(entry.Items, seenIDs, toolCalls); err != nil {
				return fmt.Errorf("activity %q: %w", entry.ID, err)
			}
		default:
			return fmt.Errorf("entry %q has unknown kind %q", entry.ID, entry.Kind)
		}
	}
	return nil
}

func validActivityStatus(status ActivityStatus) bool {
	switch status {
	case ActivityStatusWorking, ActivityStatusDone, ActivityStatusAborted, ActivityStatusError:
		return true
	default:
		return false
	}
}

func validateActivityItems(items []Item, seenIDs map[string]struct{}, toolCalls map[string]bool) error {
	for itemIndex, item := range items {
		if !validIdentifier(item.ID) {
			return fmt.Errorf("item %d has an invalid ID", itemIndex)
		}
		if _, exists := seenIDs[item.ID]; exists {
			return fmt.Errorf("duplicate entry or item ID %q", item.ID)
		}
		seenIDs[item.ID] = struct{}{}
		switch item.Kind {
		case ItemKindThinking, ItemKindCommentary:
			if item.ToolCallID != "" || item.Name != "" || item.IsError {
				return fmt.Errorf("%s item %q has tool-only fields", item.Kind, item.ID)
			}
		case ItemKindToolCall:
			if !validIdentifier(item.ToolCallID) {
				return fmt.Errorf("toolCall item %q has an invalid toolCallId", item.ID)
			}
			if _, exists := toolCalls[item.ToolCallID]; exists {
				return fmt.Errorf("duplicate toolCallId %q", item.ToolCallID)
			}
			toolCalls[item.ToolCallID] = false
		case ItemKindToolResult:
			completed, exists := toolCalls[item.ToolCallID]
			if !exists {
				return fmt.Errorf("toolResult item %q has no preceding toolCall", item.ID)
			}
			if completed {
				return fmt.Errorf("toolCallId %q has multiple tool results", item.ToolCallID)
			}
			toolCalls[item.ToolCallID] = true
		default:
			return fmt.Errorf("item %q has unknown kind %q", item.ID, item.Kind)
		}
	}
	return nil
}

func mergeLive(entries []Entry, live *Entry) ([]Entry, error) {
	if live == nil {
		return entries, nil
	}
	match := -1
	for index := range entries {
		if entries[index].ID != live.ID {
			continue
		}
		if entries[index].Kind != EntryKindActivity {
			return nil, fmt.Errorf("live activity ID %q collides with non-activity entry", live.ID)
		}
		if match >= 0 {
			return nil, fmt.Errorf("live activity ID %q is duplicated in history", live.ID)
		}
		match = index
	}
	if match >= 0 {
		for index := match + 1; index < len(entries); index++ {
			if entries[index].Kind == EntryKindAnswer {
				return nil, fmt.Errorf("live activity %q belongs to a closed historical chain", live.ID)
			}
		}
		entries[match] = *live
		return entries, nil
	}
	for index := len(entries) - 1; index >= 0 && entries[index].Kind != EntryKindAnswer; index-- {
		if entries[index].Kind == EntryKindActivity {
			return nil, errors.New("new live activity would duplicate the current activity chain")
		}
	}
	return append(entries, *live), nil
}

// rootedFS reads regular projection files relative to a pinned session root.
type rootedFS struct {
	root *os.Root
}

func (filesystem rootedFS) Open(name string) (fs.File, error) {
	return filesystem.root.Open(name)
}

func (filesystem rootedFS) ReadRegularFile(name string) ([]byte, error) {
	before, err := filesystem.root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if before.Mode()&fs.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", name)
	}
	file, err := filesystem.root.Open(name)
	if err != nil {
		return nil, err
	}
	opened, statErr := file.Stat()
	if statErr != nil {
		return nil, errors.Join(statErr, file.Close())
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return nil, errors.Join(fmt.Errorf("%s changed while opening", name), file.Close())
	}
	data, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return nil, errors.Join(readErr, closeErr)
	}
	return data, nil
}

// openCanonicalRoot walks from AI_DATA_HOME using lstat/open-root identity
// checks. No path component below the data root may be a symbolic link.
func openCanonicalRoot(target string) (*os.Root, bool, error) {
	base, err := filepath.Abs(paths.BaseDir())
	if err != nil {
		return nil, false, fmt.Errorf("resolve data root: %w", err)
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return nil, false, fmt.Errorf("resolve target directory: %w", err)
	}
	relative, err := filepath.Rel(base, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return nil, false, fmt.Errorf("target directory %q is outside data root %q", target, base)
	}
	baseInfo, err := os.Lstat(base)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("inspect data root %q: %w", base, err)
	}
	if baseInfo.Mode()&fs.ModeSymlink != 0 || !baseInfo.IsDir() {
		return nil, false, fmt.Errorf("data root %q is not a real directory", base)
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, false, fmt.Errorf("open data root %q: %w", base, err)
	}
	openedBase, err := root.Stat(".")
	if err != nil || !os.SameFile(baseInfo, openedBase) {
		cause := err
		if cause == nil {
			cause = errors.New("data root changed while opening")
		}
		return nil, false, errors.Join(fmt.Errorf("verify data root %q: %w", base, cause), root.Close())
	}
	if relative == "." {
		return root, false, nil
	}
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "" || component == "." || component == ".." {
			return nil, false, errors.Join(fmt.Errorf("unsafe directory component %q", component), root.Close())
		}
		info, err := root.Lstat(component)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, true, wrapRootClose(root, nil)
		}
		if err != nil {
			return nil, false, errors.Join(fmt.Errorf("inspect directory component %q: %w", component, err), root.Close())
		}
		if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
			return nil, false, errors.Join(fmt.Errorf("path component %q is not a real directory", component), root.Close())
		}
		next, err := root.OpenRoot(component)
		if err != nil {
			return nil, false, errors.Join(fmt.Errorf("open directory component %q: %w", component, err), root.Close())
		}
		openedInfo, err := next.Stat(".")
		if err != nil || !os.SameFile(info, openedInfo) {
			cause := err
			if cause == nil {
				cause = errors.New("directory changed while opening")
			}
			return nil, false, errors.Join(fmt.Errorf("verify directory component %q: %w", component, cause), root.Close(), next.Close())
		}
		if err := root.Close(); err != nil {
			return nil, false, errors.Join(fmt.Errorf("close parent of directory component %q: %w", component, err), next.Close())
		}
		root = next
	}
	return root, false, nil
}

func wrapRootClose(root *os.Root, cause error) error {
	if closeErr := root.Close(); closeErr != nil {
		return errors.Join(cause, fmt.Errorf("close data directory root: %w", closeErr))
	}
	return cause
}

func wrapCloseError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}
