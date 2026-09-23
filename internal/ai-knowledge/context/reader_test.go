package context

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HumanHorizon/automata/internal/paths"
)

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
