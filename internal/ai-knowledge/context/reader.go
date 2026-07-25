package context

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

// profileFromSessionID extracts the profile slug from the leading `profile__`
// prefix that Automata bakes into session IDs. Returns "" if the session ID has
// no profile prefix.
func profileFromSessionID(sessionID string) string {
	const sep = "__"
	idx := strings.Index(sessionID, sep)
	if idx <= 0 {
		return ""
	}
	return slugify(sessionID[:idx])
}

// profileSlug returns the profile directory name. Empty profile maps to "default".
func profileSlug() string {
	profile := os.Getenv("AI_PROFILE")
	if profile == "" {
		return "default"
	}
	return slugify(profile)
}

func sessionDir(sessionID string) string {
	profile := profileFromSessionID(sessionID)
	if profile == "" {
		profile = profileSlug()
	}
	return filepath.Join(dataHome(), "profiles", profile, "sessions", sessionID)
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

type IntentionState struct {
	Status string `json:"status"` // idle, expert, executors, validator, done
}

type Settings struct {
	Domain       string         `json:"domain"`
	Modes        []string       `json:"modes,omitempty"`
	Intention    IntentionState `json:"intention"`
	On           bool           `json:"on"`
	Dual         bool           `json:"dual,omitempty"`
	Firm         bool           `json:"firm,omitempty"`
	AutoContinue bool           `json:"autoContinue"`
}

type PlanStep struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

type Plan struct {
	Name  string     `json:"name"`
	Steps []PlanStep `json:"steps"`
}

// Status — flexible, supports both ai-knowledge and Zed formats
type Status struct {
	Text        string `json:"text,omitempty"`
	Type        string `json:"type,omitempty"`
	Action      string `json:"action,omitempty"`
	Description string `json:"description,omitempty"`
	Reason      string `json:"reason,omitempty"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
	Cwd         string `json:"cwd,omitempty"`
	Command     string `json:"command,omitempty"`
}

func (s *Status) DisplayText() string {
	if s.Text != "" {
		return s.Text
	}
	if s.Reason != "" {
		return s.Reason
	}
	if s.Description != "" {
		return s.Description
	}
	if s.Action != "" {
		return s.Action
	}
	return ""
}

func (s *Status) DisplayType() string {
	if s.Type != "" {
		return s.Type
	}
	return "info"
}

type Data struct {
	Plans    map[string][]PlanStep
	Status   *Status
	Settings *Settings
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return json.Unmarshal(data, v)
}

func Read(sessionID string) (*Data, error) {
	data := &Data{
		Plans: make(map[string][]PlanStep),
	}

	ctxPath := sessionDir(sessionID)

	// Read plans — support both old ([]string) and new ([]PlanStep) formats
	var rawPlans []map[string]interface{}
	if err := readJSON(filepath.Join(ctxPath, "plans.json"), &rawPlans); err == nil {
		for _, rp := range rawPlans {
			name, _ := rp["name"].(string)
			if name == "" {
				name = "general"
			}
			var steps []PlanStep
			if rawSteps, ok := rp["steps"]; ok {
				if stepArr, ok := rawSteps.([]interface{}); ok {
					for _, s := range stepArr {
						if stepStr, ok := s.(string); ok {
							// Old format: string
							steps = append(steps, PlanStep{Text: stepStr, Done: false})
						} else if stepMap, ok := s.(map[string]interface{}); ok {
							// New format: {text, done}
							text, _ := stepMap["text"].(string)
							done, _ := stepMap["done"].(bool)
							steps = append(steps, PlanStep{Text: text, Done: done})
						}
					}
				}
			}
			data.Plans[name] = steps
		}
	}

	// Read status — accept any non-empty status
	var status Status
	if err := readJSON(filepath.Join(ctxPath, "status.json"), &status); err == nil {
		if status.DisplayText() != "" || status.Action != "" || status.Command != "" {
			data.Status = &status
		}
	}

	// Read settings
	settingsPath := filepath.Join(ctxPath, "settings.json")
	var settings Settings
	if err := readJSON(settingsPath, &settings); err == nil {
		data.Settings = &settings
	}

	return data, nil
}

// CachedReader caches session context (plans/status/settings) by the max mtime
// of the relevant files. Safe for concurrent use (single reader locks).
type CachedReader struct {
	mu    sync.Mutex
	cache map[string]cachedCtxEntry
}

type cachedCtxEntry struct {
	data     *Data
	maxMtime time.Time
}

// NewCachedReader creates a context reader with mtime caching.
func NewCachedReader() *CachedReader {
	return &CachedReader{cache: make(map[string]cachedCtxEntry)}
}

// Read returns the context data for a session, cached by the max mtime of
// plans.json, status.json and settings.json.
func (r *CachedReader) Read(sessionID string) (*Data, error) {
	if sessionID == "" {
		return &Data{Plans: map[string][]PlanStep{}}, nil
	}
	dir := sessionDir(sessionID)
	// Compute max mtime of the three files we care about.
	var maxMtime time.Time
	for _, name := range []string{"plans.json", "status.json", "settings.json"} {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		if fi.ModTime().After(maxMtime) {
			maxMtime = fi.ModTime()
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if entry, ok := r.cache[sessionID]; ok && entry.maxMtime.Equal(maxMtime) && !maxMtime.IsZero() {
		return entry.data, nil
	}

	data, err := Read(sessionID)
	if err != nil {
		return data, err
	}
	r.cache[sessionID] = cachedCtxEntry{data: data, maxMtime: maxMtime}
	return data, nil
}
