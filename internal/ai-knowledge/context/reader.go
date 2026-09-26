package context

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/HumanHorizon/automata/internal/paths"
)

func sessionDirForProfile(profile, sessionID string) string {
	return paths.SessionDir(profile, sessionID)
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
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func readForProfile(profile, sessionID string) (*Data, error) {
	data := &Data{
		Plans: make(map[string][]PlanStep),
	}

	ctxPath := sessionDirForProfile(profile, sessionID)

	var failures []error

	// Read plans — support both old string steps and new {text, done} steps.
	plansPath := filepath.Join(ctxPath, "plans.json")
	var rawPlans []json.RawMessage
	planContents, plansErr := os.ReadFile(plansPath)
	if os.IsNotExist(plansErr) {
		plansErr = nil
	}
	if plansErr == nil && planContents != nil {
		if !strings.HasPrefix(strings.TrimSpace(string(planContents)), "[") {
			plansErr = errors.New("plans must be a JSON array")
		} else if err := json.Unmarshal(planContents, &rawPlans); err != nil {
			plansErr = fmt.Errorf("decode %s: %w", plansPath, err)
		}
	}
	if plansErr != nil {
		failures = append(failures, fmt.Errorf("read %s: %w", plansPath, plansErr))
	} else {
		for planIndex, rawPlanContents := range rawPlans {
			var rawPlan map[string]interface{}
			if err := json.Unmarshal(rawPlanContents, &rawPlan); err != nil || rawPlan == nil {
				if err == nil {
					err = errors.New("plan must be an object")
				}
				failures = append(failures, fmt.Errorf("decode %s plan %d: %w", plansPath, planIndex, err))
				continue
			}
			name, nameIsString := rawPlan["name"].(string)
			if _, exists := rawPlan["name"]; exists && !nameIsString {
				failures = append(failures, fmt.Errorf("decode %s plan %d: name must be a string", plansPath, planIndex))
			}
			if name == "" {
				name = "general"
			}
			var steps []PlanStep
			if rawSteps, exists := rawPlan["steps"]; exists {
				stepArray, isArray := rawSteps.([]interface{})
				if !isArray {
					failures = append(failures, fmt.Errorf("decode %s plan %d: steps must be an array", plansPath, planIndex))
				} else {
					for stepIndex, rawStep := range stepArray {
						if stepText, ok := rawStep.(string); ok {
							steps = append(steps, PlanStep{Text: stepText})
							continue
						}
						stepMap, isObject := rawStep.(map[string]interface{})
						if !isObject {
							failures = append(failures, fmt.Errorf("decode %s plan %d step %d: expected a string or object", plansPath, planIndex, stepIndex))
							continue
						}
						stepText, hasText := stepMap["text"].(string)
						if !hasText {
							failures = append(failures, fmt.Errorf("decode %s plan %d step %d: text must be a string", plansPath, planIndex, stepIndex))
							continue
						}
						step := PlanStep{Text: stepText}
						if rawDone, exists := stepMap["done"]; exists {
							if done, ok := rawDone.(bool); ok {
								step.Done = done
							} else {
								failures = append(failures, fmt.Errorf("decode %s plan %d step %d: done must be a boolean", plansPath, planIndex, stepIndex))
							}
						}
						steps = append(steps, step)
					}
				}
			}
			data.Plans[name] = append(data.Plans[name], steps...)
		}
	}

	// Read status — accept any non-empty status.
	var status Status
	statusPath := filepath.Join(ctxPath, "status.json")
	if err := readJSON(statusPath, &status); err != nil {
		failures = append(failures, err)
	} else if status.DisplayText() != "" || status.Action != "" || status.Command != "" {
		data.Status = &status
	}

	settingsPath := filepath.Join(ctxPath, "settings.json")
	var settings Settings
	if err := readJSON(settingsPath, &settings); err != nil {
		failures = append(failures, err)
	} else {
		data.Settings = &settings
	}

	return data, errors.Join(failures...)
}

// Read resolves legacy sessions through existing profile directories, then
// falls back to the session prefix, AI_PROFILE and canonical default.
func Read(sessionID string) (*Data, error) {
	profile, err := paths.ResolveLegacySessionProfile(sessionID, paths.LegacySessionProfileReadOnly)
	if err != nil {
		return nil, err
	}
	return readForProfile(profile, sessionID)
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
	data      *Data
	signature string
}

var contextFiles = []string{"plans.json", "status.json", "settings.json"}

// NewCachedReader creates a context reader with file-signature caching.
func NewCachedReader() *CachedReader {
	return &CachedReader{cache: make(map[string]cachedCtxEntry)}
}

func contextCacheKey(profile, sessionID string) string {
	return sessionDirForProfile(profile, sessionID)
}

func contextFileSignature(dir string) string {
	var signature strings.Builder
	for _, name := range contextFiles {
		signature.WriteString(name)
		signature.WriteByte('=')
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			if os.IsNotExist(err) {
				signature.WriteString("missing")
			} else {
				signature.WriteString("error:")
				signature.WriteString(err.Error())
			}
			signature.WriteByte(';')
			continue
		}
		signature.WriteString(strconv.FormatInt(info.Size(), 10))
		signature.WriteByte(':')
		signature.WriteString(strconv.FormatInt(info.ModTime().UnixNano(), 10))
		signature.WriteByte(';')
	}
	return signature.String()
}

// Invalidate removes a session from the cache. It is useful when a watcher
// observes an atomic replacement whose timestamp has not advanced.
func (r *CachedReader) Invalidate(profile, sessionID string) {
	if sessionID == "" {
		return
	}
	r.mu.Lock()
	delete(r.cache, contextCacheKey(profile, sessionID))
	r.mu.Unlock()
}

// Read returns the context data for a session, cached by the complete
// existence/size/mtime signature of plans.json, status.json and settings.json.
func (r *CachedReader) Read(sessionID string) (*Data, error) {
	profile, err := paths.ResolveLegacySessionProfile(sessionID, paths.LegacySessionProfileReadOnly)
	if err != nil {
		return nil, err
	}
	return r.ReadForProfile(profile, sessionID)
}

// ReadForProfile reads and caches context data under an explicit profile.
func (r *CachedReader) ReadForProfile(profile, sessionID string) (*Data, error) {
	if sessionID == "" {
		return &Data{Plans: map[string][]PlanStep{}}, nil
	}
	dir := sessionDirForProfile(profile, sessionID)
	cacheKey := contextCacheKey(profile, sessionID)
	signature := contextFileSignature(dir)

	r.mu.Lock()
	defer r.mu.Unlock()

	if entry, ok := r.cache[cacheKey]; ok && entry.signature == signature {
		return entry.data, nil
	}

	data, err := readForProfile(profile, sessionID)
	if err != nil {
		return data, err
	}
	r.cache[cacheKey] = cachedCtxEntry{data: data, signature: signature}
	return data, nil
}
