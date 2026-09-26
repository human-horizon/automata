package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/HumanHorizon/automata/internal/atomicfile"
	"github.com/HumanHorizon/automata/internal/paths"
)

// effectiveProfile keeps memory reads and writes in the explicit profile
// scope. paths.DomainDir maps an empty profile to the canonical default.
func effectiveProfile(profile string) string {
	return profile
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

// CachedReader caches notes reads by file signature so we only re-read when the file changes.
type CachedReader struct {
	mu    sync.Mutex
	cache map[string]cachedNotesEntry
}

type cachedNotesEntry struct {
	data      *Data
	signature string
}

// NewCachedReader creates a notes reader with file-signature caching.
func NewCachedReader() *CachedReader {
	return &CachedReader{cache: make(map[string]cachedNotesEntry)}
}

// Invalidate removes a domain from the cache so a subsequent read sees
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

	if err := atomicfile.Write(filepath.Join(domainDir, "notes.json"), encoded, 0o644); err != nil {
		return fmt.Errorf("write notes file: %w", err)
	}
	return nil
}

func emptyData() *Data {
	return &Data{Notes: []NoteSummary{}}
}

func notesSignature(info os.FileInfo) string {
	return fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
}

func readNotesFile(path string) (*Data, string, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return emptyData(), "missing", nil
		}
		return nil, "", fmt.Errorf("stat notes file %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, "", fmt.Errorf("notes path %s is not a regular file", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("read notes file %s: %w", path, err)
	}
	var notes []NoteSummary
	if err := json.Unmarshal(raw, &notes); err != nil {
		return nil, "", fmt.Errorf("decode notes file %s: %w", path, err)
	}
	return &Data{Notes: normalizeNotes(notes)}, notesSignature(info), nil
}

// Read returns notes for a domain, using file signatures to skip unchanged files.
func (r *CachedReader) Read(profile, domain string) (*Data, error) {
	if domain == "" {
		return emptyData(), nil
	}
	path := filepath.Join(paths.DomainDir(effectiveProfile(profile), domain), "notes.json")

	r.mu.Lock()
	defer r.mu.Unlock()

	data, signature, err := readNotesFile(path)
	if err != nil {
		return nil, err
	}
	if entry, ok := r.cache[path]; ok && entry.signature == signature {
		return entry.data, nil
	}
	r.cache[path] = cachedNotesEntry{data: data, signature: signature}
	return data, nil
}

func Read(profile, domain string) (*Data, error) {
	if domain == "" {
		return emptyData(), nil
	}

	notesPath := filepath.Join(paths.DomainDir(effectiveProfile(profile), domain), "notes.json")
	data, _, err := readNotesFile(notesPath)
	return data, err
}
