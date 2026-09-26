// Package status reads just-pi session status.json files for the tree-badge
// indicator. Each ai-knowledge session writes a status.json under
// ~/.ai/automata/profiles/<profile>/sessions/<sessionID>/.
package status

import (
	"encoding/json"
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

func (r *CachedReader) Read(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	path := statusPath(r.profile, sessionID)

	// Check mtime cache
	fi, err := os.Stat(path)
	if err != nil {
		// File doesn't exist or can't be read — cache empty result
		r.cache.Add(sessionID, cachedEntry{value: "", mtime: time.Time{}})
		return ""
	}
	if entry, ok := r.cache.Get(sessionID); ok && entry.info != nil &&
		entry.mtime.Equal(fi.ModTime()) && entry.size == fi.Size() && os.SameFile(entry.info, fi) {
		return entry.value
	}

	// Read file
	data, err := os.ReadFile(path)
	if err != nil {
		r.cache.Add(sessionID, cachedEntry{value: "", mtime: fi.ModTime(), size: fi.Size(), info: fi})
		return ""
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		r.cache.Add(sessionID, cachedEntry{value: "", mtime: fi.ModTime(), size: fi.Size(), info: fi})
		return ""
	}
	r.cache.Add(sessionID, cachedEntry{value: rec.Action, mtime: fi.ModTime(), size: fi.Size(), info: fi})
	return rec.Action
}

// Read is a convenience function that creates a one-shot reader without caching.
// Use CachedReader for repeated reads.
func Read(profile, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	path := statusPath(profile, sessionID)
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		return ""
	}
	return rec.Action
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
		return "W" // also W — distinguishable by full word in tree
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
	// Fallback for unknown actions so a typo in status.json still surfaces.
	return "•"
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
