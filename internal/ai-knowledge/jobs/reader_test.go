package jobs

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestCachedReaderNoticesNestedJobMetadataChange(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	t.Setenv("AI_PROFILE", "test")

	sessionID := "test__chat"
	jobDir := filepath.Join(sessionDir(sessionID), "jobs", "job_active")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	metaPath := filepath.Join(jobDir, "job.json")
	if err := os.WriteFile(metaPath, []byte(`{"status":"running"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	calls := 0
	reader := newCachedReader(func(string) ([]Job, error) {
		calls++
		return []Job{{ID: "call_" + strconv.Itoa(calls)}}, nil
	})

	first, err := reader.List(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	cached, err := reader.List(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || first[0].ID != cached[0].ID {
		t.Fatalf("unchanged metadata should use cache: calls=%d first=%q cached=%q", calls, first[0].ID, cached[0].ID)
	}

	if err := os.WriteFile(metaPath, []byte(`{"status":"exited","changed":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	updated, err := reader.List(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || updated[0].ID != "call_2" {
		t.Fatalf("nested job.json change must invalidate cache: calls=%d updated=%q", calls, updated[0].ID)
	}
}

func TestListKeepsLiveJobWithoutStartedAt(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	t.Setenv("AI_PROFILE", "test")

	sessionID := "test__chat"
	jobDir := filepath.Join(sessionDir(sessionID), "jobs", "job_active")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	record := `{"id":"job_active","command":"serve","pid":` + strconv.Itoa(os.Getpid()) + `,"status":"running"}`
	if err := os.WriteFile(filepath.Join(jobDir, "job.json"), []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}

	listed, err := List(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != "job_active" {
		t.Fatalf("live job disappeared from panel: %#v", listed)
	}
}

// TestPidIsSameProcessDeadPIDReturnsFalse locks in the foundation: a dead
// PID is never the same process, regardless of what startedAt says.
func TestPidIsSameProcessDeadPIDReturnsFalse(t *testing.T) {
	if pidIsSameProcess(2_147_483_647, time.Now().UTC().Format(time.RFC3339)) {
		t.Fatalf("expected unused PID %d to be considered dead", 2_147_483_647)
	}
	if pidIsSameProcess(0, "") {
		t.Fatalf("pid 0 must be rejected")
	}
	if pidIsSameProcess(-5, "") {
		t.Fatalf("negative pid must be rejected")
	}
}

// TestPidIsSameProcessKeepsLiveJobEvenWhenPSMissing: when ps is unreachable
// (exec failure) the kill(0) signal is the source of truth. Losing a live
// job to the panel because /bin/ps is broken would be the worse failure.
func TestPidIsSameProcessKeepsLiveJobEvenWhenPSMissing(t *testing.T) {
	pid := os.Getpid()
	runner := func(int) (string, error) { return "", errFakePS }
	if !pidIsSameProcessWithRunner(pid, "2026-01-01T00:00:00Z", runner) {
		t.Fatalf("live PID must stay visible when ps errors: pid=%d", pid)
	}
}

// TestPidIsSameProcessDetectsRecycledPID: a live PID whose ps start time is
// far from the recorded startedAt (>5s) is a recycled PID and must NOT be
// trusted. Returning true would silently merge a foreign process into the
// job list, which is exactly the kind of bug we are trying to remove.
func TestPidIsSameProcessDetectsRecycledPID(t *testing.T) {
	pid := os.Getpid()
	// Pretend ps reports a start time 10 minutes from now — impossible for
	// the test process, so the diff blows past the 5s tolerance window.
	runner := func(int) (string, error) {
		return time.Now().Add(10 * time.Minute).Local().Format("Mon Jan 2 15:04:05 2006"), nil
	}
	if pidIsSameProcessWithRunner(pid, time.Now().UTC().Format(time.RFC3339), runner) {
		t.Fatalf("recycled PID must be flagged: live pid=%d with mismatched start time", pid)
	}
}

// TestPidIsSameProcessAllowsSmallClockSkew: macOS kqueue can report a start
// time a few seconds off from what we record. A live PID whose ps start
// time is within pidStartSkewTolerance (5s) is still the same process.
func TestPidIsSameProcessAllowsSmallClockSkew(t *testing.T) {
	pid := os.Getpid()
	startedAt := time.Now().UTC().Format(time.RFC3339)
	runner := func(int) (string, error) {
		// 3 seconds in the past — well inside the 5s tolerance.
		return time.Now().Add(-3 * time.Second).Local().Format("Mon Jan 2 15:04:05 2006"), nil
	}
	if !pidIsSameProcessWithRunner(pid, startedAt, runner) {
		t.Fatalf("live PID with 3s skew must remain visible: pid=%d", pid)
	}
}

// TestListDoesNotMutateDisk guards the "List is read-only" contract that
// keeps the panel responsive: even when ps reports a mismatch, the
// on-disk job.json must stay as it was so PruneStaleSession remains the
// single point of truth for the running→exited transition.
func TestListDoesNotMutateDisk(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	t.Setenv("AI_PROFILE", "test")

	sessionID := "test__chat"
	jobDir := filepath.Join(sessionDir(sessionID), "jobs", "job_dirty")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	metaPath := filepath.Join(jobDir, "job.json")
	startedAt := "2026-01-01T00:00:00.000Z"
	before := []byte(`{"id":"job_dirty","command":"x","pid":99999999,"status":"running","startedAt":"` + startedAt + `"}`)
	if err := os.WriteFile(metaPath, before, 0o644); err != nil {
		t.Fatal(err)
	}

	listed, err := List(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 0 {
		t.Fatalf("dead PID must be hidden from the list, got %+v", listed)
	}
	after, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("List must not rewrite job.json\nbefore: %s\nafter:  %s", before, after)
	}
}

// TestPruneStaleSessionMarksDeadJobs verifies the new explicit cleanup path
// is the one that flips running→exited and removes the job directory.
func TestPruneStaleSessionMarksDeadJobs(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	t.Setenv("AI_PROFILE", "test")

	sessionID := "test__chat"
	jobDir := filepath.Join(sessionDir(sessionID), "jobs", "job_dead")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	metaPath := filepath.Join(jobDir, "job.json")
	if err := os.WriteFile(metaPath, []byte(`{"id":"job_dead","command":"x","pid":99999999,"status":"running"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := PruneStaleSession(sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(jobDir); !os.IsNotExist(err) {
		t.Fatalf("expected job directory to be removed, stat err=%v", err)
	}

	count, err := RunningCount(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected 0 running jobs after prune, got %d", count)
	}
}

// errFakePS is returned by the stubbed ps runner in tests to simulate a
// missing or broken `ps` binary. The detection logic must fall back to
// trusting kill(0) instead of dropping the job.
var errFakePS = errString("ps unavailable")

type errString string

func (e errString) Error() string { return string(e) }
