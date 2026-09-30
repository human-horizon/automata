package status

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/HumanHorizon/automata/internal/paths"
)

func TestEmoji(t *testing.T) {
	cases := map[string]string{
		"thinking": "~",
		"read":     "R",
		"write":    "W",
		"grep":     "G",
		"find":     "F",
		"analyze":  "A",
		"wait":     "w",
		"job":      "J",
		"run":      ">",
		"idle":     "",
		"stop":     "X",
		// "active" and "status" are known actions without a dedicated
		// glyph — they must produce "" so the Tree falls back to "○ idle"
		// and the right panel can still render the canonical word through
		// actionIcon.
		"active": "",
		"status": "",
		"":       "",
		// Anything we don't recognise remains visibly distinct from idle.
		"weird": "?",
	}
	for action, want := range cases {
		if got := Emoji(action); got != want {
			t.Errorf("Emoji(%q) = %q, want %q", action, got, want)
		}
	}
}

func TestWord(t *testing.T) {
	cases := map[string]string{
		"thinking": "thinking",
		"read":     "read",
		"write":    "write",
		"find":     "find",
		"grep":     "grep",
		"analyze":  "analyze",
		"wait":     "wait",
		"job":      "job",
		"run":      "run",
		"idle":     "",
		"stop":     "",
		"active":   "",
		"":         "",
		"weird":    "",
	}
	for action, want := range cases {
		if got := Word(action); got != want {
			t.Errorf("Word(%q) = %q, want %q", action, got, want)
		}
	}
}

func TestReadUsesCanonicalSessionPaths(t *testing.T) {
	dataHome := t.TempDir()
	home := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	t.Setenv("HOME", home)

	tests := []struct {
		name    string
		profile string
		session string
		action  string
	}{
		{
			name:    "default profile",
			profile: "",
			session: "default-session",
			action:  "read",
		},
		{
			name:    "unicode profile slug",
			profile: "Проект Ω",
			session: "unicode-session",
			action:  "write",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := paths.SessionDir(test.profile, test.session)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("MkdirAll canonical session dir: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, "status.json"),
				[]byte(`{"action":"`+test.action+`"}`), 0o644); err != nil {
				t.Fatalf("WriteFile canonical status: %v", err)
			}

			if test.profile == "" {
				legacyDir := filepath.Join(dataHome, "sessions", test.session)
				if err := os.MkdirAll(legacyDir, 0o755); err != nil {
					t.Fatalf("MkdirAll legacy session dir: %v", err)
				}
				if err := os.WriteFile(filepath.Join(legacyDir, "status.json"),
					[]byte(`{"action":"legacy"}`), 0o644); err != nil {
					t.Fatalf("WriteFile legacy status: %v", err)
				}
			}

			got, err := Read(test.profile, test.session)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.action {
				t.Fatalf("Read = %q, want %q", got, test.action)
			}
			reader := NewCachedReader(test.profile)
			got, err = reader.Read(test.session)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.action {
				t.Fatalf("CachedReader.Read = %q, want %q", got, test.action)
			}
		})
	}

	homeStatus := filepath.Join(home, ".ai", "automata", "profiles", "default", "sessions", "default-session", "status.json")
	if _, err := os.Stat(homeStatus); !os.IsNotExist(err) {
		t.Fatalf("test must not write status under HOME, stat err: %v", err)
	}
}

func TestCachedReaderNoticesSameMtimeReplacement(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const sessionID = "same-mtime-replacement"
	dir := paths.SessionDir("", sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	statusPath := filepath.Join(dir, "status.json")
	temporaryPath := filepath.Join(dir, "replacement.json")
	mtime := time.Unix(1_700_000_000, 0)
	oldContents := []byte(`{"action":"old"}`)
	newContents := []byte(`{"action":"new"}`)
	if len(oldContents) != len(newContents) {
		t.Fatal("replacement fixtures must have equal sizes")
	}
	if err := os.WriteFile(statusPath, oldContents, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(statusPath, mtime, mtime); err != nil {
		t.Fatal(err)
	}

	reader := NewCachedReader("")
	got, err := reader.Read(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got != "old" {
		t.Fatalf("initial status = %q, want old", got)
	}
	oldInfo, err := os.Stat(statusPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(temporaryPath, newContents, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(temporaryPath, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	replacementInfo, err := os.Stat(temporaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if oldInfo.Size() != replacementInfo.Size() || !oldInfo.ModTime().Equal(replacementInfo.ModTime()) {
		t.Fatal("replacement fixture must preserve file size and mtime")
	}
	if os.SameFile(oldInfo, replacementInfo) {
		t.Fatal("replacement fixture unexpectedly refers to the same file")
	}
	if err := os.Rename(temporaryPath, statusPath); err != nil {
		t.Fatal(err)
	}

	got, err = reader.Read(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got != "new" {
		t.Fatalf("status after same-mtime replacement = %q, want new", got)
	}
}

func TestCachedReaderInvalidateForcesRefresh(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const sessionID = "invalidate-status"
	dir := paths.SessionDir("", sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	statusPath := filepath.Join(dir, "status.json")
	mtime := time.Unix(1_700_000_000, 0)
	if err := os.WriteFile(statusPath, []byte(`{"action":"old"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(statusPath, mtime, mtime); err != nil {
		t.Fatal(err)
	}

	reader := NewCachedReader("")
	got, err := reader.Read(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got != "old" {
		t.Fatalf("initial status = %q, want old", got)
	}
	if err := os.WriteFile(statusPath, []byte(`{"action":"new"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(statusPath, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	got, err = reader.Read(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got != "old" {
		t.Fatalf("cached same-signature status = %q, want stale old before invalidation", got)
	}

	reader.Invalidate(sessionID)
	got, err = reader.Read(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got != "new" {
		t.Fatalf("invalidated status = %q, want new", got)
	}
}

func TestCachedReaderBoundsSessionEntries(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	reader := NewCachedReader("")
	for index := range statusCacheCapacity + 100 {
		if _, err := reader.Read(fmt.Sprintf("status-session-%d", index)); err != nil {
			t.Fatal(err)
		}
	}
	if got := reader.cache.Len(); got > statusCacheCapacity {
		t.Fatalf("status cache entries = %d, exceeds capacity %d", got, statusCacheCapacity)
	}
}

func TestReadStatusRecordRetriesTransientTruncation(t *testing.T) {
	reads := 0
	delays := 0
	record, err := readStatusRecord("status.json", func(string) ([]byte, error) {
		reads++
		if reads == 1 {
			return []byte(`{"action":"`), nil
		}
		return []byte(`{"action":"read"}`), nil
	}, func(delay time.Duration) {
		delays++
		if delay != statusReadRetryDelay {
			t.Errorf("retry delay = %s, want %s", delay, statusReadRetryDelay)
		}
	})
	if err != nil {
		t.Fatalf("readStatusRecord returned error: %v", err)
	}
	if record.Action != "read" || reads != 2 || delays != 1 {
		t.Fatalf("record=%+v reads=%d delays=%d", record, reads, delays)
	}
}

func TestReadStatusRecordKeepsPersistentCorruptionVisible(t *testing.T) {
	reads := 0
	delays := 0
	_, err := readStatusRecord("status.json", func(string) ([]byte, error) {
		reads++
		return []byte("{"), nil
	}, func(time.Duration) { delays++ })
	if err == nil {
		t.Fatal("persistent malformed status returned success")
	}
	if reads != statusReadAttempts || delays != statusReadAttempts-1 {
		t.Fatalf("reads=%d delays=%d, want %d reads and %d delays", reads, delays, statusReadAttempts, statusReadAttempts-1)
	}
}

func TestCachedReaderIOOutsideMutexAndInvalidationWins(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const sessionID = "io-outside-mutex"
	dir := paths.SessionDir("", sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	statusPath := filepath.Join(dir, "status.json")
	if err := os.WriteFile(statusPath, []byte(`{"action":"read"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	reader := NewCachedReader("")
	originalRead := reader.readFile
	started := make(chan struct{}, 1)
	release := make(chan struct{}, 1)
	firstRead := true
	readCalls := 0
	reader.readFile = func(path string) ([]byte, error) {
		readCalls++
		if firstRead {
			firstRead = false
			started <- struct{}{}
			<-release
		}
		return originalRead(path)
	}

	readDone := make(chan error, 1)
	go func() {
		_, err := reader.Read(sessionID)
		readDone <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("reader did not reach injected file read")
	}

	invalidated := make(chan struct{})
	go func() {
		reader.Invalidate(sessionID)
		close(invalidated)
	}()
	select {
	case <-invalidated:
	case <-time.After(time.Second):
		release <- struct{}{}
		t.Fatal("Invalidate blocked behind status file read")
	}
	release <- struct{}{}
	select {
	case err := <-readDone:
		if err != nil {
			t.Fatalf("Read returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("reader did not finish after injected file read was released")
	}
	if _, cached := reader.cache.Get(sessionID); cached {
		t.Fatal("read repopulated cache after concurrent invalidation")
	}
	if _, err := reader.Read(sessionID); err != nil {
		t.Fatal(err)
	}
	if readCalls != 2 {
		t.Fatalf("status file read calls = %d, want 2 after invalidation", readCalls)
	}
}

func TestCachedReaderConcurrentReadAndInvalidate(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const sessionID = "concurrent-status"
	dir := paths.SessionDir("", sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "status.json"), []byte(`{"action":"read"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	reader := NewCachedReader("")
	var wait sync.WaitGroup
	for worker := range 16 {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for iteration := range 100 {
				if (worker+iteration)%2 == 0 {
					reader.Invalidate(sessionID)
				}
				_, _ = reader.Read(sessionID)
			}
		}(worker)
	}
	wait.Wait()
}

func TestReadMissingFile(t *testing.T) {
	got, err := Read("nope", "nosession")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("expected empty for missing file, got %q", got)
	}
}

func TestReadEmptySessionID(t *testing.T) {
	got, err := Read("profile", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("expected empty for empty sessionID, got %q", got)
	}
}

func TestReadActionFromFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AI_DATA_HOME", home)

	profile := "AI Dev"
	sid := "deadbeef"
	dir := filepath.Join(home, "profiles", "ai-dev", "sessions", sid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "status.json"), []byte(`{"action":"read"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Read(profile, sid)
	if err != nil {
		t.Fatal(err)
	}
	if got != "read" {
		t.Errorf("Read = %q, want read", got)
	}
}

func TestCachedReaderReportsBadJSON(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const sessionID = "cached-broken"
	dir := paths.SessionDir("", sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "status.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	reader := NewCachedReader("")
	if _, err := reader.Read(sessionID); err == nil {
		t.Fatal("cached reader silently treated bad JSON as empty status")
	}
	if got := reader.cache.Len(); got != 0 {
		t.Fatalf("bad status was cached as a normal value: cache entries=%d", got)
	}
}

func TestReadBadJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AI_DATA_HOME", home)

	sid := "broken"
	dir := filepath.Join(home, "profiles", "default", "sessions", sid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "status.json"), []byte(`not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read("default", sid); err == nil {
		t.Fatal("bad JSON was silently treated as empty status")
	}
}
