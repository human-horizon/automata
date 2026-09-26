package context

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HumanHorizon/automata/internal/paths"
)

func TestReadForProfileReturnsValidPartialDataAndDiagnostics(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const sessionID = "partial-context__chat"
	dir := paths.SessionDir("", sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	plans := `[{"name":"valid","steps":["legacy",{"text":"structured","done":true},42]},42,{"name":"other","steps":[{"text":"retained","done":false}]}]`
	if err := os.WriteFile(filepath.Join(dir, "plans.json"), []byte(plans), 0o644); err != nil {
		t.Fatal(err)
	}
	statusPath := filepath.Join(dir, "status.json")
	if err := os.WriteFile(statusPath, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"autoContinue":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	data, err := ReadForProfile("", sessionID)
	if err == nil {
		t.Fatal("corrupt context entries returned no diagnostic")
	}
	if data == nil {
		t.Fatal("partial context data is nil")
	}
	if got := data.Plans["valid"]; len(got) != 2 || got[0].Text != "legacy" || got[1].Text != "structured" || !got[1].Done {
		t.Fatalf("valid plan steps were not retained: %#v", got)
	}
	if got := data.Plans["other"]; len(got) != 1 || got[0].Text != "retained" {
		t.Fatalf("valid plan after corrupt entries was not retained: %#v", got)
	}
	if data.Settings == nil || !data.Settings.AutoContinue {
		t.Fatalf("valid settings were not retained: %#v", data.Settings)
	}
	if data.Status != nil {
		t.Fatalf("corrupt status unexpectedly decoded: %#v", data.Status)
	}
	for _, expected := range []string{"plans.json plan 0 step 2", "plans.json plan 1", statusPath} {
		if !strings.Contains(err.Error(), expected) {
			t.Fatalf("aggregate diagnostic %q does not mention %q", err, expected)
		}
	}
}

func TestCachedReaderNoticesSizeChangeWhenMaxMtimeIsUnchanged(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	const sessionID = "cache-size"
	dir := paths.SessionDir("", sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	plansPath := filepath.Join(dir, "plans.json")
	statusPath := filepath.Join(dir, "status.json")
	if err := os.WriteFile(plansPath, []byte(`[{"name":"main","steps":[]}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statusPath, []byte(`{"text":"old"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	maxMtime := time.Unix(200, 0)
	oldMtime := time.Unix(100, 0)
	if err := os.Chtimes(plansPath, maxMtime, maxMtime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(statusPath, oldMtime, oldMtime); err != nil {
		t.Fatal(err)
	}

	reader := NewCachedReader()
	first, err := reader.ReadForProfile("", sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status == nil || first.Status.Text != "old" {
		t.Fatalf("first status = %#v", first.Status)
	}
	if err := os.WriteFile(statusPath, []byte(`{"text":"new status"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(statusPath, oldMtime, oldMtime); err != nil {
		t.Fatal(err)
	}
	second, err := reader.ReadForProfile("", sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if second.Status == nil || second.Status.Text != "new status" {
		t.Fatalf("size change was not detected: %#v", second.Status)
	}
}

func TestCachedReaderNoticesFileDeletion(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	const sessionID = "cache-delete"
	dir := paths.SessionDir("", sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	plansPath := filepath.Join(dir, "plans.json")
	statusPath := filepath.Join(dir, "status.json")
	if err := os.WriteFile(plansPath, []byte(`[{"name":"main","steps":[]}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statusPath, []byte(`{"text":"present"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	maxMtime := time.Unix(200, 0)
	if err := os.Chtimes(plansPath, maxMtime, maxMtime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(statusPath, time.Unix(100, 0), time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}

	reader := NewCachedReader()
	first, err := reader.ReadForProfile("", sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status == nil {
		t.Fatal("expected initial status")
	}
	if err := os.Remove(statusPath); err != nil {
		t.Fatal(err)
	}
	second, err := reader.ReadForProfile("", sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != nil {
		t.Fatalf("deleted status remained cached: %#v", second.Status)
	}
}

func TestCachedReaderInvalidateReloadsUnchangedSignature(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	const sessionID = "cache-invalidate"
	dir := paths.SessionDir("", sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	statusPath := filepath.Join(dir, "status.json")
	mtime := time.Unix(100, 0)
	if err := os.WriteFile(statusPath, []byte(`{"text":"one"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(statusPath, mtime, mtime); err != nil {
		t.Fatal(err)
	}

	reader := NewCachedReader()
	first, err := reader.ReadForProfile("", sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status == nil || first.Status.Text != "one" {
		t.Fatalf("first status = %#v", first.Status)
	}
	if err := os.WriteFile(statusPath, []byte(`{"text":"two"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(statusPath, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	cached, err := reader.ReadForProfile("", sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if cached.Status == nil || cached.Status.Text != "one" {
		t.Fatalf("expected unchanged signature to use cache, got %#v", cached.Status)
	}
	reader.Invalidate("", sessionID)
	updated, err := reader.ReadForProfile("", sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status == nil || updated.Status.Text != "two" {
		t.Fatalf("invalidation did not reload status: %#v", updated.Status)
	}
}

func TestReadForProfileEmptyUsesDefault(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	t.Setenv("AI_PROFILE", "wrong-profile")
	const sessionID = "default-chat"

	defaultDir := paths.SessionDir("", sessionID)
	wrongDir := paths.SessionDir("wrong-profile", sessionID)
	for _, dir := range []string{defaultDir, wrongDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(defaultDir, "status.json"), []byte(`{"text":"default"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wrongDir, "status.json"), []byte(`{"text":"wrong"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	data, err := ReadForProfile("", sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if data.Status == nil || data.Status.Text != "default" {
		t.Fatalf("empty explicit profile status = %#v, want default", data.Status)
	}
}

func TestLegacyReadResolvesProfilePrefixEnvironmentAndDefault(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	t.Setenv("AI_PROFILE", "wrong-profile")

	writeStatus := func(profile, sessionID, text string) {
		t.Helper()
		dir := paths.SessionDir(profile, sessionID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "status.json"), []byte(`{"text":"`+text+`"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeStatus("encoded-profile", "encoded-profile__chat", "encoded")
	writeStatus("wrong-profile", "encoded-profile__chat", "wrong")
	writeStatus("wrong-profile", "legacy-chat", "environment")
	writeStatus("", "default-chat", "default")

	for _, test := range []struct {
		sessionID string
		want      string
	}{
		{sessionID: "encoded-profile__chat", want: "encoded"},
		{sessionID: "legacy-chat", want: "environment"},
	} {
		data, err := Read(test.sessionID)
		if err != nil {
			t.Fatal(err)
		}
		if data.Status == nil || data.Status.Text != test.want {
			t.Fatalf("Read(%q) = %#v, want %q", test.sessionID, data.Status, test.want)
		}
		cached, err := NewCachedReader().Read(test.sessionID)
		if err != nil {
			t.Fatal(err)
		}
		if cached.Status == nil || cached.Status.Text != test.want {
			t.Fatalf("CachedReader.Read(%q) = %#v, want %q", test.sessionID, cached.Status, test.want)
		}
	}

	t.Setenv("AI_PROFILE", "")
	data, err := Read("default-chat")
	if err != nil {
		t.Fatal(err)
	}
	if data.Status == nil || data.Status.Text != "default" {
		t.Fatalf("Read with unset profile = %#v, want canonical default", data.Status)
	}
	cached, err := NewCachedReader().Read("default-chat")
	if err != nil {
		t.Fatal(err)
	}
	if cached.Status == nil || cached.Status.Text != "default" {
		t.Fatalf("CachedReader.Read with unset profile = %#v, want canonical default", cached.Status)
	}
}

func TestLegacyReadResolvesDefaultFamiliarSessionID(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	t.Setenv("AI_PROFILE", "")
	const sessionID = "chat__expert"
	dir := paths.SessionDir("", sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "status.json"), []byte(`{"text":"default familiar"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	for name, read := range map[string]func(string) (*Data, error){
		"direct": Read,
		"cached": NewCachedReader().Read,
	} {
		data, err := read(sessionID)
		if err != nil {
			t.Fatalf("%s read: %v", name, err)
		}
		if data.Status == nil || data.Status.Text != "default familiar" {
			t.Fatalf("%s status = %#v, want default familiar session", name, data.Status)
		}
	}
}

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
	if legacy.Status == nil || legacy.Status.Text != "explicit" {
		t.Fatalf("legacy reader did not honor session profile prefix: %#v", legacy.Status)
	}
	cachedLegacy, err := NewCachedReader().Read(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if cachedLegacy.Status == nil || cachedLegacy.Status.Text != "explicit" {
		t.Fatalf("cached legacy reader did not honor session profile prefix: %#v", cachedLegacy.Status)
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
