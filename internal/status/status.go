// Package status reads just-pi session status.json files for the tree-badge
// indicator. Each ai-knowledge session writes a status.json under
// ~/.ai/automata/profiles/<profile>/sessions/<sessionID>/.
package status

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/HumanHorizon/automata/internal/cache"
	"github.com/HumanHorizon/automata/internal/paths"
)

// Record mirrors the fields we care about from status.json. Other fields are
// ignored so we don't break when ai-knowledge adds new ones.
type Record struct {
	Action string `json:"action"`
}

// CachedReader caches status reads while file identity, size and mtime remain unchanged.
// Safe for concurrent use.
type CachedReader struct {
	mu      sync.Mutex
	profile string
	cache   *cache.LRU[string, cachedEntry]
}

type cachedEntry struct {
	value string
	mtime time.Time
	size  int64
	info  os.FileInfo
}

func statusPath(profile, sessionID string) string {
	return filepath.Join(paths.SessionDir(profile, sessionID), "status.json")
}

const statusCacheCapacity = 1024

// NewCachedReader creates a reader that caches by file identity, size and mtime.
func NewCachedReader(profile string) *CachedReader {
	return &CachedReader{
		profile: profile,
		cache:   cache.NewLRU[string, cachedEntry](statusCacheCapacity),
	}
}

// Read returns the "action" field of status.json for a session, or "" if the
// file is missing or unreadable. Uses file metadata to avoid re-reading unchanged files.
// Invalidate forces the next Read for a session to inspect status.json again.
func (r *CachedReader) Invalidate(sessionID string) {
	if r == nil || sessionID == "" {
		return
	}
	r.mu.Lock()
	r.cache.Delete(sessionID)
	r.mu.Unlock()
}

func (r *CachedReader) Read(sessionID string) (string, error) {
	if sessionID == "" {
		return "", nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	path := statusPath(r.profile, sessionID)
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("stat status %s: %w", path, err)
	}
	if entry, ok := r.cache.Get(sessionID); ok && entry.info != nil &&
		entry.mtime.Equal(fi.ModTime()) && entry.size == fi.Size() && os.SameFile(entry.info, fi) {
		return entry.value, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read status %s: %w", path, err)
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		return "", fmt.Errorf("decode status %s: %w", path, err)
	}
	r.cache.Add(sessionID, cachedEntry{value: rec.Action, mtime: fi.ModTime(), size: fi.Size(), info: fi})
	return rec.Action, nil
}

// Read returns the action from one status file without caching.
func Read(profile, sessionID string) (string, error) {
	if sessionID == "" {
		return "", nil
	}
	path := statusPath(profile, sessionID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read status %s: %w", path, err)
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		return "", fmt.Errorf("decode status %s: %w", path, err)
	}
	return rec.Action, nil
}

// Emoji returns the single-glyph indicator for a status action, or "" when
// the action is empty / unknown. The mapping mirrors pkg/ui/render.go so the
// tree badge always agrees with the Knowledge panel.
//
// "active" is intentionally not a recognized substatus: a session with no
// recorded substatus should display as idle, not as "A".
func Emoji(action string) string {
	switch action {
	case "thinking":
		return "~"
	case "read":
		return "R"
	case "write":
		return "W"
	case "grep":
		return "G"
	case "find":
		return "F"
	case "analyze":
		return "A"
	case "wait":
		return "w"
	case "job":
		return "J"
	case "run":
		return ">"
	case "idle":
		return ""
	case "stop":
		return "X"
	// "active" and "status" are known actions with no dedicated glyph and
	// no Word mapping — they map to empty so the Tree falls back to
	// "○ idle" and the right panel can still render the canonical word
	// (e.g. "● active") through actionIcon without a competing glyph.
	case "active", "status":
		return ""
	}
	if action == "" {
		return ""
	}
	// Fallback for unknown actions so a typo in status.json stays visible.
	return "?"
}

// Word returns the lowercase canonical substatus name for the tree status
// column, or "" if the action is empty / unknown. Use this when the badge
// should read like a sentence ("● read") rather than a glyph ("R").
//
// "active" maps to "" — the spec says a session that is merely running
// without a recorded substatus must not be labeled "active".
func Word(action string) string {
	switch action {
	case "thinking", "read", "write", "find", "grep", "analyze", "wait", "job", "run":
		return action
	case "idle", "stop", "active":
		return ""
	}
	return ""
}
