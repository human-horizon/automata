package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// dataHome returns the base directory for ai-knowledge data.
// Data is stored under ~/.ai/automata/profiles/<profile> so that ai-knowledge
// integrates with Automata.
func dataHome() string {
	if v := os.Getenv("AI_DATA_HOME"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "/Users/a"
	}
	return filepath.Join(home, ".ai", "automata")
}

func homeDir() string {
	return dataHome()
}

// profileSlug returns the profile directory name. Empty profile maps to "default".
func profileSlug() string {
	profile := os.Getenv("AI_PROFILE")
	if profile == "" {
		return "default"
	}
	return slugify(profile)
}

func domainDir(profile, domain string) string {
	if profile == "" {
		profile = profileSlug()
	}
	return filepath.Join(dataHome(), "profiles", profile, "domains", domain)
}

// slugify is a minimal slug used only for profile directory names.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			if b.Len() > 0 && b.String()[b.Len()-1] != '-' {
				b.WriteRune('-')
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

type NoteSummary struct {
	Title string   `json:"title"`
	Notes []string `json:"notes"`
}

type Data struct {
	Notes []NoteSummary
}

// CachedReader caches notes reads by mtime so we only re-read when the file changes.
type CachedReader struct {
	mu    sync.Mutex
	cache map[string]cachedNotesEntry
}

type cachedNotesEntry struct {
	data  *Data
	mtime time.Time
}

// NewCachedReader creates a notes reader with mtime caching.
func NewCachedReader() *CachedReader {
	return &CachedReader{cache: make(map[string]cachedNotesEntry)}
}

// Read returns notes for a domain, using mtime cache to skip unchanged files.
func (r *CachedReader) Read(profile, domain string) (*Data, error) {
	if domain == "" {
		return &Data{Notes: []NoteSummary{}}, nil
	}
	path := filepath.Join(domainDir(profile, domain), "notes.json")

	r.mu.Lock()
	defer r.mu.Unlock()

	// Check mtime cache
	fi, err := os.Stat(path)
	if err != nil {
		// File missing — cache empty result
		empty := &Data{Notes: []NoteSummary{}}
		r.cache[path] = cachedNotesEntry{data: empty, mtime: time.Time{}}
		return empty, nil
	}
	if entry, ok := r.cache[path]; ok && entry.mtime.Equal(fi.ModTime()) {
		return entry.data, nil
	}

	// Read file
	raw, err := os.ReadFile(path)
	if err != nil {
		empty := &Data{Notes: []NoteSummary{}}
		r.cache[path] = cachedNotesEntry{data: empty, mtime: fi.ModTime()}
		return empty, nil
	}
	var notes []NoteSummary
	_ = json.Unmarshal(raw, &notes)
	data := &Data{Notes: notes}
	r.cache[path] = cachedNotesEntry{data: data, mtime: fi.ModTime()}
	return data, nil
}

func Read(profile, domain string) (*Data, error) {
	if domain == "" {
		return &Data{
			Notes: []NoteSummary{},
		}, nil
	}

	notesPath := filepath.Join(domainDir(profile, domain), "notes.json")

	if raw, err := os.ReadFile(notesPath); err == nil {
		data := &Data{
			Notes: []NoteSummary{},
		}
		_ = json.Unmarshal(raw, &data.Notes)
		return data, nil
	}

	return &Data{
		Notes: []NoteSummary{},
	}, nil
}
