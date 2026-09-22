package context

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/HumanHorizon/automata/internal/paths"
)

// profileFromSessionID extracts the already canonical profile slug from the
// leading `profile__` prefix baked into session IDs.
func profileFromSessionID(sessionID string) string {
	const sep = "__"
	idx := strings.Index(sessionID, sep)
	if idx <= 0 {
		return ""
	}
	return sessionID[:idx]
}

func sessionDirForProfile(profile, sessionID string) string {
	if profile == "" {
		profile = os.Getenv("AI_PROFILE")
	}
	return paths.SessionDir(profile, sessionID)
}

func sessionDir(sessionID string) string {
	return sessionDirForProfile(profileFromSessionID(sessionID), sessionID)
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

func readForProfile(profile, sessionID string) (*Data, error) {
	data := &Data{
		Plans: make(map[string][]PlanStep),
	}

	ctxPath := sessionDirForProfile(profile, sessionID)

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

// Read returns context data using the profile encoded in the session ID or
// AI_PROFILE for legacy unprefixed IDs.
func Read(sessionID string) (*Data, error) {
	return readForProfile("", sessionID)
}

// ReadForProfile returns context data using the explicit canonical profile.
func ReadForProfile(profile, sessionID string) (*Data, error) {
	return readForProfile(profile, sessionID)
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
	return r.ReadForProfile("", sessionID)
}

// ReadForProfile reads and caches context data under an explicit profile.
func (r *CachedReader) ReadForProfile(profile, sessionID string) (*Data, error) {
	if sessionID == "" {
		return &Data{Plans: map[string][]PlanStep{}}, nil
	}
	dir := sessionDirForProfile(profile, sessionID)
	cacheKey := filepath.Join(dir, "plans.json")
	// Compute max mtime of the three files we care about.
	var maxMtime time.Time
	for _, name := range []string{"plans.json", "status.json", "settings.json"} {
		filePath := filepath.Join(dir, name)
		if name != "plans.json" {
			cacheKey += "|" + filePath
		}
		fi, err := os.Stat(filePath)
		if err != nil {
			continue
		}
		if fi.ModTime().After(maxMtime) {
			maxMtime = fi.ModTime()
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if entry, ok := r.cache[cacheKey]; ok && entry.maxMtime.Equal(maxMtime) && !maxMtime.IsZero() {
		return entry.data, nil
	}

	data, err := readForProfile(profile, sessionID)
	if err != nil {
		return data, err
	}
	r.cache[cacheKey] = cachedCtxEntry{data: data, maxMtime: maxMtime}
	return data, nil
}
