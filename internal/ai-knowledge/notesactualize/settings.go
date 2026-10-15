package notesactualize

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const settingsFileName = "settings.json"

var thinkingLevels = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

// RunOptions selects the model and reasoning level for one explicit run.
type RunOptions struct {
	Model         string
	ThinkingLevel string
}

// Settings contains only model-selection fields read from the Pi settings file.
type Settings struct {
	Models               []string
	DefaultModel         string
	ThinkingLevels       []string
	DefaultThinkingLevel string
}

type piSettings struct {
	EnabledModels        []string `json:"enabledModels"`
	DefaultModel         string   `json:"defaultModel"`
	DefaultThinkingLevel string   `json:"defaultThinkingLevel"`
}

// LoadSettings reads the model allowlist and defaults from one agent directory.
func LoadSettings(agentDir string) (Settings, error) {
	if strings.TrimSpace(agentDir) == "" {
		return Settings{}, errors.New("pi agent directory is not configured")
	}
	path := filepath.Join(agentDir, settingsFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return Settings{}, fmt.Errorf("read Pi model settings: %w", err)
	}
	var raw piSettings
	if err := json.Unmarshal(data, &raw); err != nil {
		return Settings{}, fmt.Errorf("decode Pi model settings: %w", err)
	}
	models := make([]string, 0, len(raw.EnabledModels))
	seen := make(map[string]struct{}, len(raw.EnabledModels))
	for _, model := range raw.EnabledModels {
		model = strings.TrimSpace(model)
		if model == "" {
			return Settings{}, errors.New("pi enabledModels contains an empty model")
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		models = append(models, model)
	}
	if len(models) == 0 {
		return Settings{}, errors.New("pi enabledModels is empty")
	}
	defaultModel := strings.TrimSpace(raw.DefaultModel)
	if _, ok := seen[defaultModel]; !ok {
		defaultModel = models[0]
	}
	defaultThinking := strings.TrimSpace(raw.DefaultThinkingLevel)
	if !validThinkingLevel(defaultThinking) {
		return Settings{}, fmt.Errorf("unsupported defaultThinkingLevel %q", defaultThinking)
	}
	return Settings{
		Models:               models,
		DefaultModel:         defaultModel,
		ThinkingLevels:       append([]string(nil), thinkingLevels...),
		DefaultThinkingLevel: defaultThinking,
	}, nil
}

// ValidateOptions rejects values that are not present in the current settings.
func (s Settings) ValidateOptions(options RunOptions) error {
	modelFound := false
	for _, model := range s.Models {
		if model == options.Model {
			modelFound = true
			break
		}
	}
	if !modelFound {
		return fmt.Errorf("model %q is not enabled in Pi settings", options.Model)
	}
	if !validThinkingLevel(options.ThinkingLevel) {
		return fmt.Errorf("unsupported thinking level %q", options.ThinkingLevel)
	}
	return nil
}

func validThinkingLevel(level string) bool {
	for _, candidate := range thinkingLevels {
		if candidate == level {
			return true
		}
	}
	return false
}
