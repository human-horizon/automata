package context

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/HumanHorizon/automata/internal/paths"
)

func TestReadForProfileUsesExplicitCanonicalSessionDir(t *testing.T) {
	t.Setenv("AI_PROFILE", "wrong-profile")
	profile := "Explicit Profile"
	sessionID := "explicit-profile__chat"
	dir := paths.SessionDir(profile, sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "status.json"), []byte(`{"text":"explicit"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	data, err := ReadForProfile(profile, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if data.Status == nil || data.Status.Text != "explicit" {
		t.Fatalf("explicit profile status = %#v", data.Status)
	}
	legacy, err := Read(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Status != nil {
		t.Fatalf("legacy reader unexpectedly read explicit profile: %#v", legacy.Status)
	}

	cached := NewCachedReader()
	cachedData, err := cached.ReadForProfile(profile, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if cachedData.Status == nil || cachedData.Status.Text != "explicit" {
		t.Fatalf("cached explicit profile status = %#v", cachedData.Status)
	}
}
