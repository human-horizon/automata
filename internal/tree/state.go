package tree

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/HumanHorizon/automata/internal/atomicfile"
	"github.com/HumanHorizon/automata/internal/paths"
	apptheme "github.com/HumanHorizon/automata/internal/theme"
)

const legacyStateFileName = ".automata/state.json"

// CommittedStateError reports a directory-sync failure after state.json was
// atomically replaced. Callers must keep the committed in-memory mutation.
type CommittedStateError struct {
	Path string
	Err  error
}

func (e *CommittedStateError) Error() string {
	return fmt.Sprintf("state committed to %s but directory sync failed: %v", e.Path, e.Err)
}

func (e *CommittedStateError) Unwrap() error { return e.Err }

func stateCommitWasApplied(err error) bool {
	var committed *CommittedStateError
	return errors.As(err, &committed)
}

// TreeState is the serializable representation of the tree.
type TreeState struct {
	Version        int          `json:"version"`
	Items          []*StateItem `json:"items"`
	TreeWidth      int          `json:"tree_width,omitempty"`
	PlanWidth      int          `json:"plan_width,omitempty"`
	Theme          string       `json:"theme,omitempty"`
	ActiveSessions []string     `json:"active_sessions,omitempty"`
}

// StateItem is a serializable tree item.
type StateItem struct {
	Name           string       `json:"name"`
	IsFolder       bool         `json:"is_folder"`
	IsTerminal     bool         `json:"is_terminal,omitempty"`
	Archived       bool         `json:"archived,omitempty"`
	Expanded       bool         `json:"expanded,omitempty"`
	Items          []*StateItem `json:"items,omitempty"` // nil for chats/terminals, slice for folders
	CWD            string       `json:"cwd,omitempty"`
	CommandHistory []string     `json:"command_history,omitempty"`
	BoundPath      string       `json:"bound_path,omitempty"`
}

// stateFilePath returns the path to the state file, creating the directory if needed.
// State lives under ~/.ai/automata/profiles/<profile-slug>/state.json.
func stateFilePath(profile string) (string, error) {
	if err := paths.EnsureProfileDir(profile); err != nil {
		return "", fmt.Errorf("cannot create profile dir: %w", err)
	}
	return paths.StatePath(profile), nil
}

var writeStateAtomic = atomicfile.Write

// SaveState serializes the tree and atomically replaces the unified profile
// state path. The temporary file lives beside state.json so Rename is atomic.
func (t *Tree) SaveState() error {
	if t.stateLoadErr != nil {
		return fmt.Errorf("tree state is read-only until a valid snapshot is loaded: %w", t.stateLoadErr)
	}
	if t.saveStateOverride != nil {
		return t.saveStateOverride()
	}
	t.saveMu.Lock()
	defer t.saveMu.Unlock()

	data, err := json.MarshalIndent(t.toState(), "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	path, err := stateFilePath(t.Profile)
	if err != nil {
		return err
	}

	if err := writeStateAtomic(path, data, 0o644); err != nil {
		if atomicfile.IsCommitted(err) {
			return &CommittedStateError{Path: path, Err: err}
		}
		return fmt.Errorf("write state %s: %w", path, err)
	}
	return nil
}

// LoadState reads and validates a complete profile snapshot before replacing
// any in-memory tree state. A one-time legacy migration remains resumable.
func (t *Tree) LoadState() error {
	if err := migrateLegacyData(t.Profile); err != nil {
		return t.rejectStateLoad(fmt.Errorf("migrate legacy state: %w", err))
	}

	path, err := stateFilePath(t.Profile)
	if err != nil {
		return t.rejectStateLoad(fmt.Errorf("resolve state path: %w", err))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			t.stateLoadErr = nil
			return nil
		}
		return t.rejectStateLoad(fmt.Errorf("read state %s: %w", path, err))
	}

	var state TreeState
	if err := json.Unmarshal(data, &state); err != nil {
		return t.rejectStateLoad(fmt.Errorf("decode state %s: %w", path, err))
	}
	if err := validateTreeState(&state); err != nil {
		return t.rejectStateLoad(fmt.Errorf("validate state %s: %w", path, err))
	}

	root := fromStateItems(state.Items)
	t.SetProfile(t.Profile)
	validSessions := make(map[string]struct{})
	var collectSessions func([]*Item)
	collectSessions = func(items []*Item) {
		for _, item := range items {
			if item == nil {
				continue
			}
			if item.IsFolder {
				collectSessions(item.Children)
				continue
			}
			validSessions[t.sessionKey(item)] = struct{}{}
		}
	}
	collectSessions(root)
	active := make(map[string]struct{}, len(state.ActiveSessions))
	var discardedActive []string
	for _, id := range state.ActiveSessions {
		if id == "" {
			continue
		}
		if _, exists := validSessions[id]; !exists {
			discardedActive = append(discardedActive, id)
			continue
		}
		active[id] = struct{}{}
	}
	t.root = root
	t.treeWidth = state.TreeWidth
	t.planWidth = state.PlanWidth
	t.activeSessions = active
	if state.Theme != "" {
		if _, ok := apptheme.ByID(state.Theme); ok {
			t.Theme = state.Theme
		}
	}
	t.rebuildFlat()

	t.stateLoadErr = nil
	t.lastActionError = nil
	if len(discardedActive) > 0 {
		t.recordActionError(fmt.Errorf("discarded active session IDs not present in Tree: %s", strings.Join(discardedActive, ", ")))
	}
	return nil
}

func (t *Tree) rejectStateLoad(err error) error {
	t.stateLoadErr = err
	return err
}

func validateTreeState(state *TreeState) error {
	if state == nil {
		return errors.New("state is required")
	}
	if state.Version < 0 || state.Version > 2 {
		return fmt.Errorf("unsupported state version %d", state.Version)
	}
	return validateStateItems(state.Items, "items")
}

func validateStateItems(items []*StateItem, path string) error {
	seen := make(map[string]string, len(items))
	for index, item := range items {
		itemPath := fmt.Sprintf("%s[%d]", path, index)
		if item == nil {
			return fmt.Errorf("%s is null", itemPath)
		}
		name := strings.TrimSpace(item.Name)
		key := canonicalSiblingKey(name)
		if key == "" {
			return fmt.Errorf("%s has an empty canonical name", itemPath)
		}
		if previous, exists := seen[key]; exists {
			return fmt.Errorf("%s duplicates sibling identity %q from %s", itemPath, key, previous)
		}
		seen[key] = itemPath
		if item.IsFolder && item.IsTerminal {
			return fmt.Errorf("%s cannot be both folder and terminal", itemPath)
		}
		if !item.IsFolder && (len(item.Items) != 0 || item.Archived || item.BoundPath != "") {
			return fmt.Errorf("%s has folder-only fields on a leaf", itemPath)
		}
		if item.IsFolder {
			if err := validateStateItems(item.Items, itemPath+".items"); err != nil {
				return err
			}
		}
	}
	return nil
}

// ClearState removes the state file.
func (t *Tree) ClearState() error {
	path, err := stateFilePath(t.Profile)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	t.stateLoadErr = nil
	return nil
}

// toState converts the root items to a serializable state.
func (t *Tree) toState() *TreeState {
	return &TreeState{
		Version:        2,
		Items:          toStateItems(t.root),
		TreeWidth:      t.treeWidth,
		PlanWidth:      t.planWidth,
		Theme:          apptheme.Resolve(t.Theme).ID,
		ActiveSessions: t.ActiveSessionIDs(),
	}
}

func toStateItems(items []*Item) []*StateItem {
	if items == nil {
		return nil
	}
	out := make([]*StateItem, len(items))
	for i, item := range items {
		si := &StateItem{
			Name:       item.Name,
			IsFolder:   item.IsFolder,
			IsTerminal: item.IsTerminal,
			Archived:   item.Archived,
			Expanded:   item.Expanded,
			BoundPath:  item.BoundPath,
		}
		if item.IsFolder {
			si.Items = toStateItems(item.Children)
		} else {
			si.CWD = item.CWD
			si.CommandHistory = item.CommandHistory
		}
		out[i] = si
	}
	return out
}

func fromStateItems(items []*StateItem) []*Item {
	if items == nil {
		return nil
	}
	out := make([]*Item, len(items))
	for i, si := range items {
		item := &Item{
			Name:       strings.TrimSpace(si.Name),
			IsFolder:   si.IsFolder,
			IsTerminal: si.IsTerminal,
			Archived:   si.Archived,
			Expanded:   si.Expanded,
			BoundPath:  si.BoundPath,
			ID:         generateID(),
		}
		item.refreshBoundStale()
		if si.IsFolder {
			children := fromStateItems(si.Items)
			for _, child := range children {
				child.parent = item
			}
			item.Children = children
		} else {
			item.CWD = si.CWD
			item.CommandHistory = si.CommandHistory
		}
		out[i] = item
	}
	return out
}
