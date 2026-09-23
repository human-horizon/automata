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

func TestRunningCountForEmptyExplicitProfileUsesDefault(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	t.Setenv("AI_PROFILE", "wrong-profile")

	const sessionID = "default-chat"
	defaultJobs := filepath.Join(paths.SessionDir("", sessionID), "jobs", "job_active")
	wrongJobs := filepath.Join(paths.SessionDir("wrong-profile", sessionID), "jobs", "job_active")
	for _, dir := range []string{defaultJobs, wrongJobs} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(defaultJobs, "job.json"), []byte(`{"id":"default","status":"running"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wrongJobs, "job.json"), []byte(`{"id":"wrong","status":"running"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	count, err := RunningCountForProfile("", sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("empty explicit profile running count = %d, want 1 from default", count)
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

// TestPidIsSameProcessKeepsLiveJobEvenWhenPSMissing preserves the
// read-only behavior: an inconclusive identity remains visible in the panel.
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

func TestKillSessionFailsClosedForUnknownPIDIdentity(t *testing.T) {
	tests := []struct {
		name       string
		startedAt  string
		psResponse func(int) (string, error)
	}{
		{
			name:       "ps unavailable",
			startedAt:  time.Now().UTC().Format(time.RFC3339),
			psResponse: func(int) (string, error) { return "", errFakePS },
		},
		{
			name:       "malformed ps output",
			startedAt:  time.Now().UTC().Format(time.RFC3339),
			psResponse: func(int) (string, error) { return "not a date", nil },
		},
		{
			name:       "malformed startedAt",
			startedAt:  "not a timestamp",
			psResponse: matchingPSRunner(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AI_DATA_HOME", t.TempDir())
			t.Setenv("AI_PROFILE", "test")
			const sessionID = "test__kill-unknown"

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
			psRunnerOverride = tt.psResponse

			_, metaPath := writeKillSessionRecord(t, sessionID, os.Getpid(), tt.startedAt)
			if err := KillSession(sessionID); err == nil {
				t.Fatal("KillSession succeeded with unknown PID identity")
			}
			if called != 0 {
				t.Fatalf("unknown PID identity received a signal: %d calls", called)
			}
			var record JobRecord
			if err := readJSON(metaPath, &record); err != nil {
				t.Fatal(err)
			}
			if record.Status != "running" {
				t.Fatalf("unknown PID status = %q, want running", record.Status)
			}
		})
	}
}

func TestKillSessionLeavesMetadataRunningWhenProcessSurvivesSIGTERM(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	t.Setenv("AI_PROFILE", "test")
	const sessionID = "test__kill-still-running"

	previousSignal := processSignal
	previousPS := psRunnerOverride
	previousProbe := processProbeFn
	t.Cleanup(func() {
		processSignal = previousSignal
		psRunnerOverride = previousPS
		processProbeFn = previousProbe
	})
	processSignal = func(int, syscall.Signal) error { return nil }
	psRunnerOverride = matchingPSRunner()
	processProbeFn = func(int) bool { return true }

	jobDir, metaPath := writeKillSessionRecord(t, sessionID, os.Getpid(), time.Now().UTC().Format(time.RFC3339))
	if err := KillSession(sessionID); err == nil || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("KillSession error = %v, want still-running error", err)
	}
	var record JobRecord
	if err := readJSON(metaPath, &record); err != nil {
		t.Fatal(err)
	}
	if record.Status != "running" {
		t.Fatalf("surviving process status = %q, want running", record.Status)
	}
	if _, err := os.Stat(jobDir); err != nil {
		t.Fatalf("job metadata disappeared: %v", err)
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
	previousPS := psRunnerOverride
	previousProbe := processProbeFn
	t.Cleanup(func() {
		processSignal = previousSignal
		psRunnerOverride = previousPS
		processProbeFn = previousProbe
	})
	processSignal = func(pid int, signal syscall.Signal) error {
		signaledPID = pid
		signaledSignal = signal
		return nil
	}
	psRunnerOverride = matchingPSRunner()
	processProbeFn = func(int) bool { return false }

	jobDir, metaPath := writeKillSessionRecord(t, sessionID, os.Getpid(), time.Now().UTC().Format(time.RFC3339))
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
	previousPS := psRunnerOverride
	t.Cleanup(func() {
		processSignal = previousSignal
		psRunnerOverride = previousPS
	})
	processSignal = func(int, syscall.Signal) error { return signalErr }
	psRunnerOverride = matchingPSRunner()

	jobDir, metaPath := writeKillSessionRecord(t, sessionID, os.Getpid(), time.Now().UTC().Format(time.RFC3339))
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

func TestKillPlanExecutesAgainstMigratedSessionDirectory(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	t.Setenv("AI_PROFILE", "test")
	const (
		profile = "test"
		oldID   = "test__old-chat"
		newID   = "test__new-chat"
		jobName = "job_migrated"
	)
	oldJobDir := filepath.Join(paths.SessionDir(profile, oldID), "jobs", jobName)
	if err := os.MkdirAll(oldJobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	metaPath := filepath.Join(oldJobDir, "job.json")
	startedAt := time.Now().UTC().Format(time.RFC3339)
	data := `{"id":"job_migrated","command":"sleep","pid":` + strconv.Itoa(os.Getpid()) + `,"status":"running","startedAt":"` + startedAt + `"}`
	if err := os.WriteFile(metaPath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	previousSignal := processSignal
	previousPS := psRunnerOverride
	previousProbe := processProbeFn
	t.Cleanup(func() {
		processSignal = previousSignal
		psRunnerOverride = previousPS
		processProbeFn = previousProbe
	})
	signals := 0
	processSignal = func(int, syscall.Signal) error {
		signals++
		return nil
	}
	psRunnerOverride = matchingPSRunner()
	processProbeFn = func(int) bool { return false }

	plan, err := PrepareKillSessionForProfile(profile, oldID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(paths.SessionDir(profile, oldID), paths.SessionDir(profile, newID)); err != nil {
		t.Fatal(err)
	}
	if err := plan.ExecuteForProfile(profile, newID); err != nil {
		t.Fatal(err)
	}
	if signals != 1 {
		t.Fatalf("processSignal calls = %d, want 1", signals)
	}
	if _, err := os.Stat(paths.SessionDir(profile, oldID)); !os.IsNotExist(err) {
		t.Fatalf("old session directory remains: %v", err)
	}
	migratedMeta := filepath.Join(paths.SessionDir(profile, newID), "jobs", jobName, "job.json")
	var record JobRecord
	if err := readJSON(migratedMeta, &record); err != nil {
		t.Fatal(err)
	}
	if record.Status != "exited" {
		t.Fatalf("migrated job status = %q, want exited", record.Status)
	}
}

func TestKillSessionForProfileAfterFilesystemMigration(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	profile := "Canonical Profile"
	oldID := "canonical-profile__old-chat"
	newID := "canonical-profile__new-chat"
	jobDir := filepath.Join(sessionDirForProfile(profile, oldID), "jobs", "job_kill")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	metaPath := filepath.Join(jobDir, "job.json")
	startedAt := time.Now().UTC().Format(time.RFC3339)
	data := `{"id":"job_kill","command":"sleep","pid":` + strconv.Itoa(os.Getpid()) + `,"status":"running","startedAt":"` + startedAt + `"}`
	if err := os.WriteFile(metaPath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	previousSignal := processSignal
	previousPS := psRunnerOverride
	previousProbe := processProbeFn
	t.Cleanup(func() {
		processSignal = previousSignal
		psRunnerOverride = previousPS
		processProbeFn = previousProbe
	})
	signals := 0
	processSignal = func(pid int, signal syscall.Signal) error {
		signals++
		if pid != os.Getpid() || signal != syscall.SIGTERM {
			t.Fatalf("signal = pid %d signal %v, want pid %d SIGTERM", pid, signal, os.Getpid())
		}
		return nil
	}
	psRunnerOverride = matchingPSRunner()
	processProbeFn = func(int) bool { return false }

	oldDir := sessionDirForProfile(profile, oldID)
	newDir := sessionDirForProfile(profile, newID)
	if err := os.Rename(oldDir, newDir); err != nil {
		t.Fatal(err)
	}
	if err := KillSessionForProfile(profile, newID); err != nil {
		t.Fatal(err)
	}
	if signals != 1 {
		t.Fatalf("processSignal calls = %d, want 1", signals)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("old session directory remains: %v", err)
	}
	var record JobRecord
	if err := readJSON(filepath.Join(newDir, "jobs", "job_kill", "job.json"), &record); err != nil {
		t.Fatal(err)
	}
	if record.Status != "exited" {
		t.Fatalf("migrated job status = %q, want exited", record.Status)
	}
}

func TestKillPlanAttemptsAllCandidatesAfterSignalFailure(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	t.Setenv("AI_PROFILE", "test")
	const sessionID = "test__multi-job"
	jobsDir := filepath.Join(sessionDir(sessionID), "jobs")
	startedAt := time.Now().UTC().Format(time.RFC3339)
	for _, job := range []struct {
		name string
		id   string
	}{
		{name: "job_a", id: "job_a"},
		{name: "job_b", id: "job_b"},
	} {
		jobDir := filepath.Join(jobsDir, job.name)
		if err := os.MkdirAll(jobDir, 0o755); err != nil {
			t.Fatal(err)
		}
		data := `{"id":"` + job.id + `","command":"sleep","pid":` + strconv.Itoa(os.Getpid()) + `,"status":"running","startedAt":"` + startedAt + `"}`
		if err := os.WriteFile(filepath.Join(jobDir, "job.json"), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	previousSignal := processSignal
	previousPS := psRunnerOverride
	previousProbe := processProbeFn
	t.Cleanup(func() {
		processSignal = previousSignal
		psRunnerOverride = previousPS
		processProbeFn = previousProbe
	})
	signaled := make([]int, 0, 2)
	processSignal = func(pid int, _ syscall.Signal) error {
		signaled = append(signaled, pid)
		if len(signaled) == 2 {
			return errString("second signal failed")
		}
		return nil
	}
	psRunnerOverride = matchingPSRunner()
	processProbeFn = func(int) bool { return false }

	plan, err := PrepareKillSessionForProfile("test", sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Execute(); err == nil || !strings.Contains(err.Error(), "second signal failed") {
		t.Fatalf("KillPlan error = %v, want aggregate signal failure", err)
	}
	if len(signaled) != 2 {
		t.Fatalf("processSignal calls = %d, want 2", len(signaled))
	}
	var first, second JobRecord
	if err := readJSON(filepath.Join(jobsDir, "job_a", "job.json"), &first); err != nil {
		t.Fatal(err)
	}
	if err := readJSON(filepath.Join(jobsDir, "job_b", "job.json"), &second); err != nil {
		t.Fatal(err)
	}
	if first.Status != "exited" || second.Status != "running" {
		t.Fatalf("statuses after partial signal: first=%q second=%q", first.Status, second.Status)
	}
}

func TestPrepareKillSessionFailsClosedAcrossAllJobsBeforeSignal(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	t.Setenv("AI_PROFILE", "test")
	const sessionID = "test__prepare-all"
	jobsDir := filepath.Join(sessionDir(sessionID), "jobs")
	for _, job := range []struct {
		name      string
		startedAt string
	}{
		{name: "job_same", startedAt: time.Now().UTC().Format(time.RFC3339)},
		{name: "job_unknown", startedAt: "invalid"},
	} {
		jobDir := filepath.Join(jobsDir, job.name)
		if err := os.MkdirAll(jobDir, 0o755); err != nil {
			t.Fatal(err)
		}
		data := `{"id":"` + job.name + `","command":"sleep","pid":` + strconv.Itoa(os.Getpid()) + `,"status":"running","startedAt":"` + job.startedAt + `"}`
		if err := os.WriteFile(filepath.Join(jobDir, "job.json"), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	previousPS := psRunnerOverride
	previousSignal := processSignal
	t.Cleanup(func() {
		psRunnerOverride = previousPS
		processSignal = previousSignal
	})
	psRunnerOverride = matchingPSRunner()
	signals := 0
	processSignal = func(int, syscall.Signal) error {
		signals++
		return nil
	}

	if _, err := PrepareKillSessionForProfile("test", sessionID); err == nil {
		t.Fatal("prepare unexpectedly succeeded with unknown identity")
	}
	if signals != 0 {
		t.Fatalf("prepare sent %d signals, want 0", signals)
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

// matchingPSRunner returns the current process start time in the format
// emitted by macOS ps.
func matchingPSRunner() psRunner {
	return func(int) (string, error) {
		return time.Now().Local().Format("Mon Jan 2 15:04:05 2006"), nil
	}
}

// errFakePS is returned by the stubbed ps runner in tests to simulate a
// missing or broken `ps` binary.
var errFakePS = errString("ps unavailable")

type errString string

func (e errString) Error() string { return string(e) }
