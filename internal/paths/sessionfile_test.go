package paths

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFindSessionJSONLFallsBackWhenCWDChanged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	const sessionID = "humanhorizon__human-horizon.automata.ai-2"
	createdCWD := "/Users/a/Space"
	currentCWD := "/Users/a/Space/Projects/HumanHorizon/automata"
	want := writeSessionFixture(t, home, createdCWD, sessionID, time.Now())

	t.Logf("Дано: сессия AI#2 создана в %s", createdCWD)
	t.Logf("Когда: Clear ищет её из %s", currentCWD)

	got := FindSessionJSONL(sessionID, currentCWD, "")

	if got != want {
		t.Fatalf("Тогда: должен быть найден исходный JSONL\nожидалось: %s\nполучено:   %s", want, got)
	}
}

func TestFindSessionJSONLPrefersCurrentCWD(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	const sessionID = "humanhorizon__human-horizon.automata.ai-2"
	currentCWD := "/current"
	otherCWD := "/other"
	want := writeSessionFixture(t, home, currentCWD, sessionID, time.Now().Add(-time.Hour))
	writeSessionFixture(t, home, otherCWD, sessionID, time.Now())

	got := FindSessionJSONL(sessionID, currentCWD, "")

	if got != want {
		t.Fatalf("current cwd must have priority\nexpected: %s\nactual:   %s", want, got)
	}
}

func TestFindSessionJSONLMatchesExactSessionID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	const sessionID = "humanhorizon__human-horizon.automata.ai-2"
	writeSessionFixture(t, home, "/current", sessionID+"0", time.Now())
	want := writeSessionFixture(t, home, "/created", sessionID, time.Now().Add(-time.Hour))

	got := FindSessionJSONL(sessionID, "/current", "")

	if got != want {
		t.Fatalf("similar session id must not match\nexpected: %s\nactual:   %s", want, got)
	}
}

func TestFindSessionJSONLFallbackChoosesNewestMatch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	const sessionID = "humanhorizon__human-horizon.automata.ai-2"
	writeSessionFixture(t, home, "/older", sessionID, time.Now().Add(-time.Hour))
	want := writeSessionFixture(t, home, "/newer", sessionID, time.Now())

	got := FindSessionJSONL(sessionID, "/missing", "")

	if got != want {
		t.Fatalf("fallback must choose newest matching JSONL\nexpected: %s\nactual:   %s", want, got)
	}
}

func TestDeleteSessionJSONLFallsBackAndDeletesOnlyExactMatch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	const sessionID = "humanhorizon__human-horizon.automata.ai-2"
	target := writeSessionFixture(t, home, "/created", sessionID, time.Now())
	similar := writeSessionFixture(t, home, "/current", sessionID+"0", time.Now())

	t.Log("Дано: AI#2 создана в другом каталоге, а похожая сессия находится в текущем")
	agentDir := filepath.Join(home, ".ai", "just", "pi")
	deleted, err := DeleteSessionJSONL(sessionID, "/current", agentDir)
	if err != nil {
		t.Fatalf("Когда: Clear удаляет историю AI#2: %v", err)
	}

	if deleted != target {
		t.Fatalf("Тогда: должна быть удалена точная сессия\nожидалось: %s\nполучено:   %s", target, deleted)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target JSONL still exists or cannot be checked: %v", err)
	}
	if _, err := os.Stat(similar); err != nil {
		t.Fatalf("similar session must remain: %v", err)
	}
}

// TestSessionRootsDiscoversAllAgents verifies SessionRoots picks up every
// ~/.ai/<agent>/pi/sessions/ directory that exists on the machine, not
// only the default just one. Without this, Clear on a non-just agent
// (e.g. --pi getic) cannot find its JSONL and silently fails.
func TestSessionRootsDiscoversAllAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	for _, agent := range []string{"just", "getic", "synth"} {
		if err := os.MkdirAll(filepath.Join(home, ".ai", agent, "pi", "sessions"), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", agent, err)
		}
	}

	roots := SessionRoots()
	want := map[string]bool{
		filepath.Join(home, ".ai", "just", "pi", "sessions"):  false,
		filepath.Join(home, ".ai", "getic", "pi", "sessions"): false,
		filepath.Join(home, ".ai", "synth", "pi", "sessions"): false,
	}
	for _, r := range roots {
		if _, ok := want[r]; ok {
			want[r] = true
		}
	}
	for path, seen := range want {
		if !seen {
			t.Errorf("expected %q in SessionRoots(), got %v", path, roots)
		}
	}
}

// TestFindSessionJSONLLocatesAcrossAgents confirms that a session created
// under a non-just pi agent (--pi getic) is discoverable by Clear even
// when called with an arbitrary cwd. Pre-fix, SessionRoots only knew
// about ~/.ai/just/pi/sessions/ and the lookup returned "".
func TestFindSessionJSONLLocatesAcrossAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := os.MkdirAll(filepath.Join(home, ".ai", "getic", "pi", "sessions"), 0o755); err != nil {
		t.Fatalf("mkdir getic: %v", err)
	}

	const sessionID = "getic__shop.sym-8285.ai-2"
	cwd := "/Users/a/Space/Projects/Getic"
	dir := filepath.Join(home, ".ai", "getic", "pi", "sessions", EncodeCwdDir(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("%d_%s.jsonl", time.Now().UnixNano(), sessionID))
	header := fmt.Sprintf("{\"type\":\"session\",\"version\":3,\"id\":%q,\"cwd\":%q}\n", sessionID, cwd)
	if err := os.WriteFile(path, []byte(header), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got := FindSessionJSONL(sessionID, cwd, "")
	if got != path {
		t.Fatalf("expected to find JSONL in getic agent\nwant: %s\ngot:  %s", path, got)
	}
}

// TestFindSessionJSONLRespectsAgentFilter confirms the agent filter cuts
// off other agents' sessions. The legacy behaviour (no agent filter) still
// finds them; the new safe path used by Clear does not.
func TestFindSessionJSONLRespectsAgentFilter(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	const sessionID = "humanhorizon__human-horizon.automata.ai-2"
	justPath := writeSessionFixture(t, home, "/anywhere", sessionID, time.Now())

	// Safe path: ask for getic, hit a session that lives in just — must miss.
	if got := FindSessionJSONL(sessionID, "/anywhere", filepath.Join(home, ".ai", "getic", "pi")); got != "" {
		t.Fatalf("agent filter must not pick up just session\nexpected empty\nactual:   %s", got)
	}

	// Legacy path: empty agentDir falls back to dynamic discovery and finds it.
	if got := FindSessionJSONL(sessionID, "/anywhere", ""); got != justPath {
		t.Fatalf("legacy fallback should still find it\nexpected: %s\nactual:   %s", justPath, got)
	}

	// Same-agent filter does find it.
	if got := FindSessionJSONL(sessionID, "/anywhere", filepath.Join(home, ".ai", "just", "pi")); got != justPath {
		t.Fatalf("same-agent filter must find it\nexpected: %s\nactual:   %s", justPath, got)
	}
}

// TestDeleteSessionJSONLRefusesCrossAgent is the safety net: a Clear in
// getic must NOT delete a JSONL that lives in just, even with a matching
// sessionID. We also assert the file is still present afterwards.
func TestDeleteSessionJSONLRefusesCrossAgent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	const sessionID = "shared__id"
	justPath := writeSessionFixture(t, home, "/anywhere", sessionID, time.Now())

	geticAgent := filepath.Join(home, ".ai", "getic", "pi")
	deleted, err := DeleteSessionJSONL(sessionID, "/anywhere", geticAgent)
	if err == nil {
		t.Fatalf("expected error for cross-agent delete, got success: %s", deleted)
	}
	if deleted != "" {
		t.Fatalf("expected empty path on error, got %q", deleted)
	}
	if _, err := os.Stat(justPath); err != nil {
		t.Fatalf("just JSONL must remain untouched, stat err: %v", err)
	}
}

// TestDeleteSessionJSONLRefusesEmptyAgentDir makes the safe-by-default
// guarantee explicit: empty agentDir is never silently accepted.
func TestDeleteSessionJSONLRefusesEmptyAgentDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	const sessionID = "any__id"
	writeSessionFixture(t, home, "/anywhere", sessionID, time.Now())

	deleted, err := DeleteSessionJSONL(sessionID, "/anywhere", "")
	if err == nil {
		t.Fatalf("expected error for empty agentDir, got success: %s", deleted)
	}
	if deleted != "" {
		t.Fatalf("expected empty path on error, got %q", deleted)
	}
}

func TestFamiliarsJSONLPathUsesCanonicalDefaultProfile(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const sessionID = "chat"
	got := FamiliarsJSONLPath("", sessionID)
	want := filepath.Join(SessionDir("", sessionID), "familiars.json")
	if got != want {
		t.Fatalf("default familiars path = %q, want %q", got, want)
	}
}

func TestRemoveFamiliarUpdatesFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	const sessionID = "owner__sid"
	const keepSID = "familiar__keep"
	const dropSID = "familiar__drop"

	path := FamiliarsJSONLPath("test", sessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := fmt.Sprintf(`[
  {"id":"keep","sessionId":%q,"created":"2026-01-01"},
  {"id":"drop","sessionId":%q,"created":"2026-01-02"}
]`, keepSID, dropSID)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if err := RemoveFamiliar("test", sessionID, dropSID); err != nil {
		t.Fatalf("RemoveFamiliar: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after remove: %v", err)
	}
	var entries []FamiliarEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("parse after remove: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry left, got %d: %s", len(entries), data)
	}
	if entries[0].SessionID != keepSID {
		t.Errorf("kept entry mismatch: want %q got %q", keepSID, entries[0].SessionID)
	}
}

func TestRemoveFamiliarMissingFileNoOp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := RemoveFamiliar("test", "no-such-session", "any-familiar"); err != nil {
		t.Fatalf("missing file should be no-op, got error: %v", err)
	}
}

func TestRemoveFamiliarMissingEntryLeavesFileUntouched(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	const sessionID = "owner__sid"
	path := FamiliarsJSONLPath("test", sessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	original := []byte(`[{"id":"only","sessionId":"familiar__only","created":"2026-01-01"}]`)
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := RemoveFamiliar("test", sessionID, "familiar__missing"); err != nil {
		t.Fatalf("missing entry should not error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != string(original) {
		t.Errorf("file should be untouched for missing entry\nwant: %s\ngot:  %s", original, data)
	}
}

// TestRemoveFamiliarProfileSlugged is a regression test for the bug Anya
// hit on 2026-08-25: × close button appeared to remove the familiar from
// the UI, but the next chat poll resurrected it because RemoveFamiliar was
// silently no-op'ing (os.IsNotExist on the wrong path).
//
// Root cause: FamiliarsJSONLPath used the raw profile name ("HumanHorizon")
// while the on-disk layout uses the lowercase slug ("humanhorizon"). With
// the slug-aware path, the same familiarID removes correctly.
func TestRemoveFamiliarProfileSlugged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	const sessionID = "owner__sid"
	const keepSID = "familiar__keep"
	const dropSID = "familiar__drop"

	// Pass capitalised profile — FamiliarsJSONLPath must slug it.
	path := FamiliarsJSONLPath("HumanHorizon", sessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := fmt.Sprintf(`[
  {"id":"keep","sessionId":%q,"created":"2026-01-01"},
  {"id":"drop","sessionId":%q,"created":"2026-01-02"}
]`, keepSID, dropSID)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if err := RemoveFamiliar("HumanHorizon", sessionID, dropSID); err != nil {
		t.Fatalf("RemoveFamiliar with capitalised profile: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after remove: %v", err)
	}
	var entries []FamiliarEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("parse after remove: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry left, got %d: %s", len(entries), data)
	}
	if entries[0].SessionID != keepSID {
		t.Errorf("kept entry mismatch: want %q got %q", keepSID, entries[0].SessionID)
	}
}

func writeSessionFixture(t *testing.T, home, cwd, sessionID string, modTime time.Time) string {
	t.Helper()

	dir := filepath.Join(home, ".ai", "just", "pi", "sessions", EncodeCwdDir(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}

	path := filepath.Join(dir, fmt.Sprintf("%d_%s.jsonl", modTime.UnixNano(), sessionID))
	header := fmt.Sprintf("{\"type\":\"session\",\"version\":3,\"id\":%q,\"cwd\":%q}\n", sessionID, cwd)
	if err := os.WriteFile(path, []byte(header), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("set fixture modification time: %v", err)
	}
	return path
}
