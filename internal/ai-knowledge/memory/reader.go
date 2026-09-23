package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/HumanHorizon/automata/internal/paths"
)

// effectiveProfile preserves explicit profile precedence and uses AI_PROFILE
// only when the caller leaves the profile empty. paths.DomainDir applies the
// canonical slug and default profile mapping.
func effectiveProfile(profile string) string {
	if profile != "" {
		return profile
	}
	return os.Getenv("AI_PROFILE")
}

type NoteSection struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

type NoteSummary struct {
	Title    string        `json:"title"`
	Notes    []string      `json:"notes,omitempty"`
	Sections []NoteSection `json:"sections,omitempty"`
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

// Invalidate removes a domain from the mtime cache so a subsequent read sees
// an immediately replaced notes file even when filesystem timestamps are coarse.
func (r *CachedReader) Invalidate(profile, domain string) {
	if domain == "" {
		return
	}

	path := filepath.Join(paths.DomainDir(effectiveProfile(profile), domain), "notes.json")
	r.mu.Lock()
	delete(r.cache, path)
	r.mu.Unlock()
}

func normalizeNotes(notes []NoteSummary) []NoteSummary {
	for i := range notes {
		if len(notes[i].Sections) > 0 || len(notes[i].Notes) == 0 {
			continue
		}

		legacyLines := make([]string, 0, len(notes[i].Notes))
		for _, line := range notes[i].Notes {
			legacyLines = append(legacyLines, "- "+line)
		}
		notes[i].Sections = []NoteSection{{Content: strings.Join(legacyLines, "\n")}}
	}
	return notes
}

// Write stores notes as formatted JSON and atomically replaces notes.json.
func Write(profile, domain string, notes []NoteSummary) error {
	if domain == "" {
		return fmt.Errorf("cannot write notes without a domain")
	}

	domainDir := paths.DomainDir(effectiveProfile(profile), domain)
	if err := os.MkdirAll(domainDir, 0o755); err != nil {
		return fmt.Errorf("create notes directory: %w", err)
	}

	if notes == nil {
		notes = make([]NoteSummary, 0)
	}
	encoded, err := json.MarshalIndent(notes, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal notes: %w", err)
	}
	encoded = append(encoded, '\n')

	temporary, err := os.CreateTemp(domainDir, ".notes-*.json")
	if err != nil {
		return fmt.Errorf("create temporary notes file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set temporary notes permissions: %w", err)
	}
	if _, err := temporary.Write(encoded); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary notes file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync temporary notes file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary notes file: %w", err)
	}

	if err := os.Rename(temporaryPath, filepath.Join(domainDir, "notes.json")); err != nil {
		return fmt.Errorf("replace notes file: %w", err)
	}
	return nil
}

// Read returns notes for a domain, using mtime cache to skip unchanged files.
func (r *CachedReader) Read(profile, domain string) (*Data, error) {
	if domain == "" {
		return &Data{Notes: []NoteSummary{}}, nil
	}
	path := filepath.Join(paths.DomainDir(effectiveProfile(profile), domain), "notes.json")

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
	data := &Data{Notes: normalizeNotes(notes)}
	r.cache[path] = cachedNotesEntry{data: data, mtime: fi.ModTime()}
	return data, nil
}

func Read(profile, domain string) (*Data, error) {
	if domain == "" {
		return &Data{
			Notes: []NoteSummary{},
		}, nil
	}

	notesPath := filepath.Join(paths.DomainDir(effectiveProfile(profile), domain), "notes.json")

	if raw, err := os.ReadFile(notesPath); err == nil {
		data := &Data{
			Notes: []NoteSummary{},
		}
		_ = json.Unmarshal(raw, &data.Notes)
		data.Notes = normalizeNotes(data.Notes)
		return data, nil
	}

	return &Data{
		Notes: []NoteSummary{},
	}, nil
}
