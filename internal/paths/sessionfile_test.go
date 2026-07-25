package paths

import (
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

	got := FindSessionJSONL(sessionID, currentCWD)

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

	got := FindSessionJSONL(sessionID, currentCWD)

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

	got := FindSessionJSONL(sessionID, "/current")

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

	got := FindSessionJSONL(sessionID, "/missing")

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
	deleted, err := DeleteSessionJSONL(sessionID, "/current")
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
