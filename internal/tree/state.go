package tree

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/HumanHorizon/automata/internal/paths"
	apptheme "github.com/HumanHorizon/automata/internal/theme"
)

const legacyStateFileName = ".automata/state.json"

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

// SaveState serializes the tree and writes it to the unified profile state path.
var (
	createStateTemp = os.CreateTemp
	writeStateTemp  = func(file *os.File, data []byte) error {
		_, err := file.Write(data)
		return err
	}
	syncStateTemp  = func(file *os.File) error { return file.Sync() }
	closeStateTemp = func(file *os.File) error { return file.Close() }
	renameState    = os.Rename
	syncStateDir   = func(path string) error {
		dir, err := os.Open(path)
		if err != nil {
			return err
		}
		defer dir.Close()
		return dir.Sync()
	}
)

// SaveState serializes the tree and atomically replaces the unified profile
// state path. The temporary file lives beside state.json so Rename is atomic.
func (t *Tree) SaveState() error {
	t.saveMu.Lock()
	defer t.saveMu.Unlock()

	state := t.toState()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	path, err := stateFilePath(t.Profile)
	if err != nil {
		return err
	}

	tmp, err := createStateTemp(filepath.Dir(path), ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary state: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temporary state: %w", err)
	}
	if err := writeStateTemp(tmp, data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary state: %w", err)
	}
	if err := syncStateTemp(tmp); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temporary state: %w", err)
	}
	if err := closeStateTemp(tmp); err != nil {
		return fmt.Errorf("close temporary state: %w", err)
	}
	if err := renameState(tmpPath, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	if err := syncStateDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sync state directory: %w", err)
	}
	return nil
}

// LoadState reads the profile state file, deserializes it, and replaces the tree root.
// If the file doesn't exist or is invalid, the tree remains empty.
// A one-time migration from ~/.automata is performed when the new profile dir is empty
// but legacy data exists.
func (t *Tree) LoadState() error {
	if err := migrateLegacyData(t.Profile); err != nil {
		// Migration errors are non-fatal; log and continue with empty/fresh state.
		fmt.Fprintf(os.Stderr, "automata: legacy migration warning: %v\n", err)
	}

	path, err := stateFilePath(t.Profile)
	if err != nil {
		return nil // can't even determine path — continue with empty tree
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no state file yet — empty tree
		}
		return nil // can't read — continue with empty tree
	}
	var state TreeState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil // corrupted file — continue with empty tree
	}
	t.root = fromStateItems(state.Items)
	t.treeWidth = state.TreeWidth
	t.planWidth = state.PlanWidth
	if state.Theme != "" {
		if _, ok := apptheme.ByID(state.Theme); ok {
			t.Theme = state.Theme
		}
	}
	if len(state.ActiveSessions) > 0 {
		t.activeSessions = make(map[string]struct{}, len(state.ActiveSessions))
		for _, id := range state.ActiveSessions {
			t.activeSessions[id] = struct{}{}
		}
	}
	t.rebuildFlat()
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
			Name:       si.Name,
			IsFolder:   si.IsFolder,
			IsTerminal: si.IsTerminal,
			Archived:   si.Archived,
			Expanded:   si.Expanded,
			BoundPath:  si.BoundPath,
			ID:         generateID(),
		}
		item.refreshBoundStale()
		if si.IsFolder && len(si.Items) > 0 {
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
