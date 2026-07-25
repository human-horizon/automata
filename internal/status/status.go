// Package status reads just-pi session status.json files for the tree-badge
// indicator. Each ai-knowledge session writes a status.json under
// ~/.ai/automata/profiles/<profile>/sessions/<sessionID>/.
package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Record mirrors the fields we care about from status.json. Other fields are
// ignored so we don't break when ai-knowledge adds new ones.
type Record struct {
	Action string `json:"action"`
}

// CachedReader caches status reads by mtime so we only re-read when files change.
// Safe for concurrent use from a single goroutine (bubbletea main loop).
type CachedReader struct {
	profile string
	cache   map[string]cachedEntry
}

type cachedEntry struct {
	value  string
	mtime  time.Time
}

// NewCachedReader creates a reader that caches by mtime.
func NewCachedReader(profile string) *CachedReader {
	return &CachedReader{
		profile: profile,
		cache:   make(map[string]cachedEntry),
	}
}

// Read returns the "action" field of status.json for a session, or "" if the
// file is missing or unreadable. Uses mtime cache to avoid re-reading unchanged files.
func (r *CachedReader) Read(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	base := dataHome()
	if r.profile != "" {
		base = filepath.Join(base, "profiles", slugify(r.profile))
	}
	path := filepath.Join(base, "sessions", sessionID, "status.json")

	// Check mtime cache
	fi, err := os.Stat(path)
	if err != nil {
		// File doesn't exist or can't be read — cache empty result
		r.cache[sessionID] = cachedEntry{value: "", mtime: time.Time{}}
		return ""
	}
	if entry, ok := r.cache[sessionID]; ok && entry.mtime.Equal(fi.ModTime()) {
		return entry.value
	}

	// Read file
	data, err := os.ReadFile(path)
	if err != nil {
		r.cache[sessionID] = cachedEntry{value: "", mtime: fi.ModTime()}
		return ""
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		r.cache[sessionID] = cachedEntry{value: "", mtime: fi.ModTime()}
		return ""
	}
	r.cache[sessionID] = cachedEntry{value: rec.Action, mtime: fi.ModTime()}
	return rec.Action
}

// Read is a convenience function that creates a one-shot reader without caching.
// Use CachedReader for repeated reads.
func Read(profile, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	base := dataHome()
	if profile != "" {
		base = filepath.Join(base, "profiles", slugify(profile))
	}
	path := filepath.Join(base, "sessions", sessionID, "status.json")
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

func dataHome() string {
	if v := os.Getenv("AI_DATA_HOME"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return "/Users/a"
	}
	return filepath.Join(home, ".ai", "automata")
}

func slugify(s string) string {
	out := make([]byte, 0, len(s))
	prevDash := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
			out = append(out, c)
			prevDash = false
		case c >= 'A' && c <= 'Z':
			out = append(out, c+32)
			prevDash = false
		case c >= '0' && c <= '9':
			out = append(out, c)
			prevDash = false
		default:
			if !prevDash && len(out) > 0 {
				out = append(out, '-')
				prevDash = true
			}
		}
	}
	// Trim trailing dash.
	for len(out) > 0 && out[len(out)-1] == '-' {
		out = out[:len(out)-1]
	}
	return string(out)
}