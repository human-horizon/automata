package notesactualize

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadSettingsReadsOnlyEnabledModelsAndDefaults(t *testing.T) {
	dir := t.TempDir()
	content := `{"enabledModels":["anthropic/model-a","openai/model-b","anthropic/model-a"],"defaultModel":"openai/model-b","defaultThinkingLevel":"high","apiKey":"must-not-be-exposed"}`
	if err := os.WriteFile(filepath.Join(dir, settingsFileName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LoadSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"anthropic/model-a", "openai/model-b"}; !reflect.DeepEqual(got.Models, want) {
		t.Fatalf("models = %#v, want %#v", got.Models, want)
	}
	if got.DefaultModel != "openai/model-b" || got.DefaultThinkingLevel != "high" {
		t.Fatalf("unexpected defaults: %#v", got)
	}
	if got.ValidateOptions(RunOptions{Model: "anthropic/model-a", ThinkingLevel: "max"}) != nil {
		t.Fatal("expected enabled model and supported thinking level to validate")
	}
	if err := got.ValidateOptions(RunOptions{Model: "not-enabled", ThinkingLevel: "high"}); err == nil {
		t.Fatal("expected disabled model to be rejected")
	}
	if err := got.ValidateOptions(RunOptions{Model: "anthropic/model-a", ThinkingLevel: "unknown"}); err == nil {
		t.Fatal("expected unknown thinking level to be rejected")
	}
}

func TestLoadSettingsFallsBackToFirstEnabledModel(t *testing.T) {
	dir := t.TempDir()
	content := `{"enabledModels":["provider/first","provider/second"],"defaultModel":"provider/not-enabled","defaultThinkingLevel":"low"}`
	if err := os.WriteFile(filepath.Join(dir, settingsFileName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultModel != "provider/first" {
		t.Fatalf("default model = %q, want first enabled model", got.DefaultModel)
	}
}

func TestLoadSettingsRejectsInvalidSources(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "empty model list", content: `{"enabledModels":[],"defaultThinkingLevel":"low"}`},
		{name: "empty model id", content: `{"enabledModels":["provider/model", " "],"defaultThinkingLevel":"low"}`},
		{name: "bad thinking default", content: `{"enabledModels":["provider/model"],"defaultThinkingLevel":"ultra"}`},
		{name: "malformed JSON", content: `{"enabledModels":`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, settingsFileName), []byte(test.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadSettings(dir); err == nil {
				t.Fatal("expected invalid settings to fail closed")
			}
		})
	}
}
