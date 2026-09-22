package jobs

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/HumanHorizon/automata/internal/paths"
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

func TestRunningCountForProfileUsesExplicitCanonicalSessionDir(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	t.Setenv("AI_PROFILE", "wrong-profile")

	profile := "Explicit Profile"
	sessionID := "explicit-profile__chat"
	jobDir := filepath.Join(paths.SessionDir(profile, sessionID), "jobs", "job_active")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "job.json"), []byte(`{"id":"job_active","status":"running"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	count, err := RunningCountForProfile(profile, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("explicit profile running count = %d, want 1", count)
	}
	legacy, err := RunningCount(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if legacy != 0 {
		t.Fatalf("legacy running count leaked explicit profile: %d", legacy)
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

func writeKillSessionRecord(t *testing.T, sessionID string, pid int, startedAt string) (string, string) {
	t.Helper()
	jobDir := filepath.Join(sessionDir(sessionID), "jobs", "job_kill")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	metaPath := filepath.Join(jobDir, "job.json")
	record := `{"id":"job_kill","command":"sleep","pid":` + strconv.Itoa(pid) + `,"status":"running"`
	if startedAt != "" {
		record += `,"startedAt":"` + startedAt + `"`
	}
	record += "}"
	if err := os.WriteFile(metaPath, []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
	return jobDir, metaPath
}

func TestKillSessionDoesNotSignalRecycledPIDAndCleansStaleRecord(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	t.Setenv("AI_PROFILE", "test")
	const sessionID = "test__kill-recycled"

	called := 0
	previousSignal := processSignal
	previousPS := psRunnerOverride
	t.Cleanup(func() {
		processSignal = previousSignal
		psRunnerOverride = previousPS
	})
	processSignal = func(int, syscall.Signal) error {
		called++
		return nil
	}
	psRunnerOverride = func(int) (string, error) {
		return time.Now().Add(10 * time.Minute).Local().Format("Mon Jan 2 15:04:05 2006"), nil
	}

	jobDir, _ := writeKillSessionRecord(t, sessionID, os.Getpid(), time.Now().UTC().Format(time.RFC3339))
	if err := KillSession(sessionID); err != nil {
		t.Fatal(err)
	}
	if called != 0 {
		t.Fatalf("recycled PID received a signal: %d calls", called)
	}
	if _, err := os.Stat(jobDir); !os.IsNotExist(err) {
		t.Fatalf("stale recycled job was not cleaned: %v", err)
	}
}

func TestKillSessionDoesNotSignalDeadPIDAndCleansStaleRecord(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	t.Setenv("AI_PROFILE", "test")
	const sessionID = "test__kill-dead"

	called := 0
	previousSignal := processSignal
	t.Cleanup(func() { processSignal = previousSignal })
	processSignal = func(int, syscall.Signal) error {
		called++
		return nil
	}

	jobDir, _ := writeKillSessionRecord(t, sessionID, 2_147_483_647, "")
	if err := KillSession(sessionID); err != nil {
		t.Fatal(err)
	}
	if called != 0 {
		t.Fatalf("dead PID received a signal: %d calls", called)
	}
	if _, err := os.Stat(jobDir); !os.IsNotExist(err) {
		t.Fatalf("stale dead job was not cleaned: %v", err)
	}
}

func TestKillSessionSignalsMatchingLivePIDAndWritesExitedMetadata(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	t.Setenv("AI_PROFILE", "test")
	const sessionID = "test__kill-live"

	var signaledPID int
	var signaledSignal syscall.Signal
	previousSignal := processSignal
	t.Cleanup(func() { processSignal = previousSignal })
	processSignal = func(pid int, signal syscall.Signal) error {
		signaledPID = pid
		signaledSignal = signal
		return nil
	}

	jobDir, metaPath := writeKillSessionRecord(t, sessionID, os.Getpid(), "")
	if err := KillSession(sessionID); err != nil {
		t.Fatal(err)
	}
	if signaledPID != os.Getpid() || signaledSignal != syscall.SIGTERM {
		t.Fatalf("signal = pid %d, signal %v; want pid %d, SIGTERM", signaledPID, signaledSignal, os.Getpid())
	}
	var record JobRecord
	if err := readJSON(metaPath, &record); err != nil {
		t.Fatal(err)
	}
	if record.Status != "exited" {
		t.Fatalf("live job status = %q, want exited", record.Status)
	}
	if _, err := os.Stat(jobDir); err != nil {
		t.Fatalf("live job metadata disappeared: %v", err)
	}
}

func TestKillSessionReturnsSignalErrorAndPreservesLiveMetadata(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	t.Setenv("AI_PROFILE", "test")
	const sessionID = "test__kill-error"

	signalErr := errString("signal denied")
	previousSignal := processSignal
	t.Cleanup(func() { processSignal = previousSignal })
	processSignal = func(int, syscall.Signal) error { return signalErr }

	jobDir, metaPath := writeKillSessionRecord(t, sessionID, os.Getpid(), "")
	if err := KillSession(sessionID); err == nil || !strings.Contains(err.Error(), "signal denied") {
		t.Fatalf("KillSession error = %v, want signal error", err)
	}
	var record JobRecord
	if err := readJSON(metaPath, &record); err != nil {
		t.Fatal(err)
	}
	if record.Status != "running" {
		t.Fatalf("live job status after signal error = %q, want running", record.Status)
	}
	if _, err := os.Stat(jobDir); err != nil {
		t.Fatalf("live job metadata disappeared after signal error: %v", err)
	}
}

func writeProfileStaleJob(t *testing.T, profile, sessionID string) string {
	t.Helper()
	jobDir := filepath.Join(paths.SessionDir(profile, sessionID), "jobs", "job_stale")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "job.json"), []byte(`{"id":"job_stale","pid":2147483647,"status":"running"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return jobDir
}

func TestCleanupStaleUsesExplicitProfileScope(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	t.Setenv("AI_PROFILE", "wrong-profile")
	profileA := "Profile A"
	profileB := "Profile B"
	jobA := writeProfileStaleJob(t, profileA, "chat-a")
	jobB := writeProfileStaleJob(t, profileB, "chat-b")

	if err := CleanupStaleForProfile(profileA); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(jobA); !os.IsNotExist(err) {
		t.Fatalf("explicit profile cleanup left profile A job: %v", err)
	}
	if _, err := os.Stat(jobB); err != nil {
		t.Fatalf("explicit profile cleanup touched profile B unexpectedly: %v", err)
	}

	if err := CleanupStale(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(jobB); !os.IsNotExist(err) {
		t.Fatalf("all-profile cleanup left profile B job: %v", err)
	}
}

// errFakePS is returned by the stubbed ps runner in tests to simulate a
// missing or broken `ps` binary. The detection logic must fall back to
// trusting kill(0) instead of dropping the job.
var errFakePS = errString("ps unavailable")

type errString string

func (e errString) Error() string { return string(e) }
