package context

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HumanHorizon/automata/internal/cache"
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
	cached := NewCachedReader()
	cachedData, err := cached.ReadForProfile(profile, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if cachedData.Status == nil || cachedData.Status.Text != "explicit" {
		t.Fatalf("cached explicit profile status = %#v", cachedData.Status)
	}
}

func TestCachedReaderBoundsEntriesAndSkipsOversizedSource(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	reader := NewCachedReader()
	for index := range 500 {
		sessionID := fmt.Sprintf("bounded-context__session-%d", index)
		if _, err := reader.ReadForProfile("bounded-context", sessionID); err != nil {
			t.Fatalf("read session %d: %v", index, err)
		}
	}
	if got := reader.cache.Len(); got > cache.ReaderCacheCapacity {
		t.Fatalf("cache entries = %d, exceeds capacity %d", got, cache.ReaderCacheCapacity)
	}

	const sessionID = "oversized-context__session"
	dir := paths.SessionDir("oversized-context", sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	largePlans := []byte("[]" + strings.Repeat(" ", int(cache.MaxSourceMetadataBytes)+1))
	if err := os.WriteFile(filepath.Join(dir, "plans.json"), largePlans, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadForProfile("oversized-context", sessionID); err != nil {
		t.Fatalf("read oversized source: %v", err)
	}
	if _, cached := reader.cache.Get(contextCacheKey("oversized-context", sessionID)); cached {
		t.Fatal("oversized context source was cached")
	}
}
