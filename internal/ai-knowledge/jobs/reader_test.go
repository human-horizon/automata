package jobs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/HumanHorizon/automata/internal/cache"
	"github.com/HumanHorizon/automata/internal/paths"
)

func sessionDir(sessionID string) string {
	return paths.SessionDir("test", sessionID)
}

func TestListForProfileReturnsValidJobsAndMetadataDiagnostics(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const sessionID = "test__partial-jobs"
	jobsDir := filepath.Join(sessionDir(sessionID), "jobs")
	validDir := filepath.Join(jobsDir, "valid-job")
	invalidDir := filepath.Join(jobsDir, "invalid-job")
	for _, dir := range []string{validDir, invalidDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	validRecord := `{"id":"valid-job","command":"echo retained","pid":` + strconv.Itoa(os.Getpid()) + `,"status":"running"}`
	if err := os.WriteFile(filepath.Join(validDir, "job.json"), []byte(validRecord), 0o644); err != nil {
		t.Fatal(err)
	}
	invalidPath := filepath.Join(invalidDir, "job.json")
	if err := os.WriteFile(invalidPath, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}

	jobs, err := ListForProfile("test", sessionID)
	if err == nil || !strings.Contains(err.Error(), invalidPath) {
		t.Fatalf("ListForProfile error = %v, want path-specific metadata diagnostic", err)
	}
	if len(jobs) != 1 || jobs[0].ID != "valid-job" {
		t.Fatalf("valid job was not retained: %#v", jobs)
	}

	cachedJobs, cachedErr := NewCachedReader().ListForProfile("test", sessionID)
	if cachedErr == nil || !strings.Contains(cachedErr.Error(), invalidPath) {
		t.Fatalf("CachedReader error = %v, want path-specific metadata diagnostic", cachedErr)
	}
	if len(cachedJobs) != 1 || cachedJobs[0].ID != "valid-job" {
		t.Fatalf("CachedReader dropped valid partial job: %#v", cachedJobs)
	}
}

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

	first, err := reader.ListForProfile("test", sessionID)
	if err != nil {
		t.Fatal(err)
	}
	cached, err := reader.ListForProfile("test", sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || first[0].ID != cached[0].ID {
		t.Fatalf("unchanged metadata should use cache: calls=%d first=%q cached=%q", calls, first[0].ID, cached[0].ID)
	}

	if err := os.WriteFile(metaPath, []byte(`{"status":"exited","changed":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	updated, err := reader.ListForProfile("test", sessionID)
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
	if err := os.WriteFile(filepath.Join(jobDir, "job.json"), []byte(`{"id":"job_active","pid":`+strconv.Itoa(os.Getpid())+`,"status":"running"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	count, err := RunningCountForProfile(profile, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("explicit profile running count = %d, want 1", count)
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

	listed, err := ListForProfile("test", sessionID)
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

	listed, err := ListForProfile("test", sessionID)
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

// TestPruneStaleSessionMarksDeadJobs verifies the explicit cleanup path
// flips running→exited while preserving completed job history.
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

	if err := PruneStaleSessionForProfile("test", sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(jobDir); err != nil {
		t.Fatalf("completed job directory was not preserved: %v", err)
	}
	var record JobRecord
	if err := readJSON(metaPath, &record); err != nil {
		t.Fatal(err)
	}
	if record.Status != "exited" {
		t.Fatalf("pruned job status = %q, want exited", record.Status)
	}

	count, err := RunningCountForProfile("test", sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected 0 running jobs after prune, got %d", count)
	}
}

func TestWriteJSONFailurePreservesPreviousMetadata(t *testing.T) {
	jobDir := t.TempDir()
	metaPath := filepath.Join(jobDir, "job.json")
	original := []byte(`{"id":"job-1","status":"running","pid":0}`)
	if err := os.WriteFile(metaPath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	writeErr := errors.New("injected atomic rename failure")
	previousWriter := writeJobMetadataAtomic
	writeJobMetadataAtomic = func(string, []byte, os.FileMode) error { return writeErr }
	t.Cleanup(func() { writeJobMetadataAtomic = previousWriter })

	record := &JobRecord{ID: "job-1", Status: "exited", PID: 0}
	if err := writeJSON(metaPath, record); !errors.Is(err, writeErr) {
		t.Fatalf("writeJSON error = %v, want injected failure", err)
	}
	got, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("failed atomic write changed existing metadata: %s", got)
	}
	entries, err := os.ReadDir(jobDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "job.json" {
		t.Fatalf("temporary metadata was not cleaned up: %#v", entries)
	}
}

func TestWriteJSONPreservesJobExtensionAndUnknownMetadata(t *testing.T) {
	jobDir := t.TempDir()
	metaPath := filepath.Join(jobDir, "job.json")
	original := []byte(`{"id":"job-1","command":"go test","cwd":"/workspace","pid":0,"status":"running","startedAt":"2026-09-24T10:00:00Z","exitCode":null,"sessionID":"chat__worker","taskId":"task-1","lifecycleEventStatus":"completed","lifecycleEventAt":"2026-09-24T10:01:00Z","agent":"tester","future":{"enabled":true}}`)
	if err := os.WriteFile(metaPath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	var record JobRecord
	if err := readJSON(metaPath, &record); err != nil {
		t.Fatal(err)
	}
	record.Status = "exited"
	if err := writeJSON(metaPath, &record); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"cwd":                  "/workspace",
		"sessionID":            "chat__worker",
		"taskId":               "task-1",
		"lifecycleEventStatus": "completed",
		"agent":                "tester",
	} {
		var got string
		if err := json.Unmarshal(fields[key], &got); err != nil || got != want {
			t.Errorf("%s = %q, error = %v, want %q", key, got, err, want)
		}
	}
	if string(fields["exitCode"]) != "null" {
		t.Errorf("exitCode = %s, want null", fields["exitCode"])
	}
	var future map[string]bool
	if err := json.Unmarshal(fields["future"], &future); err != nil || !future["enabled"] {
		t.Errorf("unknown metadata was lost: value=%s error=%v", fields["future"], err)
	}
	var status string
	if err := json.Unmarshal(fields["status"], &status); err != nil || status != "exited" {
		t.Errorf("status = %q, error = %v, want exited", status, err)
	}
}

func TestPruneStaleSessionPreservesJobDirectoryOnMetadataWriteFailure(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const (
		profile   = "metadata-write-failure"
		sessionID = "metadata-write-failure__chat"
	)
	jobDir := filepath.Join(paths.SessionDir(profile, sessionID), "jobs", "job_stale")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	metaPath := filepath.Join(jobDir, "job.json")
	original := []byte(`{"id":"job_stale","command":"x","pid":0,"status":"running"}`)
	if err := os.WriteFile(metaPath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	writeErr := errors.New("injected metadata replacement failure")
	previousWriter := writeJobMetadataAtomic
	writeJobMetadataAtomic = func(string, []byte, os.FileMode) error { return writeErr }
	t.Cleanup(func() { writeJobMetadataAtomic = previousWriter })

	if err := PruneStaleSessionForProfile(profile, sessionID); !errors.Is(err, writeErr) {
		t.Fatalf("PruneStaleSessionForProfile error = %v, want injected failure", err)
	}
	got, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("job metadata was lost: %v", err)
	}
	if string(got) != string(original) {
		t.Fatalf("failed prune changed job metadata: %s", got)
	}
	if _, err := os.Stat(jobDir); err != nil {
		t.Fatalf("failed metadata write removed only job directory: %v", err)
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
	if err := KillSessionForProfile("test", sessionID); err != nil {
		t.Fatal(err)
	}
	if called != 0 {
		t.Fatalf("recycled PID received a signal: %d calls", called)
	}
	if _, err := os.Stat(jobDir); err != nil {
		t.Fatalf("stale recycled job history was not preserved: %v", err)
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

	jobDir, _ := writeKillSessionRecord(t, sessionID, 2_147_483_647, time.Now().UTC().Format(time.RFC3339))
	if err := KillSessionForProfile("test", sessionID); err != nil {
		t.Fatal(err)
	}
	if called != 0 {
		t.Fatalf("dead PID received a signal: %d calls", called)
	}
	if _, err := os.Stat(jobDir); err != nil {
		t.Fatalf("stale dead job history was not preserved: %v", err)
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
			if err := KillSessionForProfile("test", sessionID); err == nil {
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
	if err := KillSessionForProfile("test", sessionID); err == nil || !strings.Contains(err.Error(), "still running") {
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
	if err := KillSessionForProfile("test", sessionID); err != nil {
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
	if err := KillSessionForProfile("test", sessionID); err == nil || !strings.Contains(err.Error(), "signal denied") {
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

func TestKillPlanSignalsAllJobsBeforeWaitingForExit(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const sessionID = "test__batched-wait"
	jobsDir := filepath.Join(sessionDir(sessionID), "jobs")
	startedAt := time.Now().UTC().Format(time.RFC3339)
	for _, jobID := range []string{"job_a", "job_b"} {
		jobDir := filepath.Join(jobsDir, jobID)
		if err := os.MkdirAll(jobDir, paths.PrivateDirMode); err != nil {
			t.Fatal(err)
		}
		data := fmt.Sprintf("{\"id\":%q,\"command\":\"sleep\",\"pid\":%d,\"status\":\"running\",\"startedAt\":%q}", jobID, os.Getpid(), startedAt)
		if err := os.WriteFile(filepath.Join(jobDir, "job.json"), []byte(data), paths.PrivateFileMode); err != nil {
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
	signals := 0
	processSignal = func(int, syscall.Signal) error {
		signals++
		return nil
	}
	psRunnerOverride = matchingPSRunner()
	probes := 0
	processProbeFn = func(int) bool {
		probes++
		if signals != 2 {
			t.Fatalf("process probe ran after %d signals, want all 2 signals first", signals)
		}
		return false
	}

	plan, err := PrepareKillSessionForProfile("test", sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Execute(); err != nil {
		t.Fatal(err)
	}
	if signals != 2 || probes != 2 {
		t.Fatalf("signals=%d probes=%d, want 2/2", signals, probes)
	}
}

func TestKillPlanBatchSignalsAcrossSessionsBeforeSharedPolling(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	t.Setenv("AI_PROFILE", "test")
	startedAt := time.Now().UTC().Format(time.RFC3339)
	const firstSession = "test__batch-first"
	const secondSession = "test__batch-second"
	writeKillSessionRecord(t, firstSession, os.Getpid(), startedAt)
	writeKillSessionRecord(t, secondSession, os.Getpid(), startedAt)

	previousSignal := processSignal
	previousPS := psRunnerOverride
	previousProbe := processProbeFn
	previousSleep := processExitSleepFn
	t.Cleanup(func() {
		processSignal = previousSignal
		psRunnerOverride = previousPS
		processProbeFn = previousProbe
		processExitSleepFn = previousSleep
	})

	signals := 0
	processSignal = func(int, syscall.Signal) error {
		signals++
		return nil
	}
	psRunnerOverride = matchingPSRunner()
	probes := 0
	probesByPID := make(map[int]int)
	processProbeFn = func(pid int) bool {
		probes++
		if signals != 2 {
			t.Fatalf("process polling started after %d signals; both sessions must be signaled first", signals)
		}
		probesByPID[pid]++
		return probesByPID[pid] == 1
	}
	sleeps := 0
	processExitSleepFn = func(delay time.Duration) {
		sleeps++
		if delay != processExitProbeInterval {
			t.Errorf("exit poll delay = %s, want %s", delay, processExitProbeInterval)
		}
	}

	firstPlan, err := PrepareKillSessionForProfile("test", firstSession)
	if err != nil {
		t.Fatal(err)
	}
	secondPlan, err := PrepareKillSessionForProfile("test", secondSession)
	if err != nil {
		t.Fatal(err)
	}
	if err := ExecuteKillPlansForProfile("test", []KillPlanTarget{
		{Plan: firstPlan, SessionID: firstSession},
		{Plan: secondPlan, SessionID: secondSession},
	}); err != nil {
		t.Fatal(err)
	}
	if signals != 2 || probes != 3 || sleeps != 1 {
		t.Fatalf("signals=%d probes=%d sleeps=%d, want 2/3/1", signals, probes, sleeps)
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

func TestKillSessionPreflightRejectsMalformedMetadataBeforeSignalOrMutation(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*JobRecord)
		missing   bool
		malformed []byte
	}{
		{name: "malformed JSON", malformed: []byte(`{"id":`)},
		{name: "missing metadata", missing: true},
		{name: "empty ID", mutate: func(record *JobRecord) { record.ID = "" }},
		{name: "mismatched ID", mutate: func(record *JobRecord) { record.ID = "other" }},
		{name: "invalid PID", mutate: func(record *JobRecord) { record.PID = 0 }},
		{name: "missing start time", mutate: func(record *JobRecord) { record.StartedAt = "" }},
		{name: "invalid start time", mutate: func(record *JobRecord) { record.StartedAt = "not-a-time" }},
		{name: "unknown status", mutate: func(record *JobRecord) { record.Status = "queued" }},
		{name: "malformed stopped record", mutate: func(record *JobRecord) {
			record.Status = "stopped"
			record.StartedAt = ""
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("AI_DATA_HOME", t.TempDir())
			const sessionID = "test__malformed-preflight"
			jobsDir := filepath.Join(sessionDirForProfile("test", sessionID), "jobs")
			validDir := filepath.Join(jobsDir, "job_valid")
			invalidDir := filepath.Join(jobsDir, "job_invalid")
			for _, dir := range []string{validDir, invalidDir} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			startedAt := time.Now().UTC().Format(time.RFC3339Nano)
			validRecord := JobRecord{ID: "job_valid", Command: "sleep", PID: os.Getpid(), Status: "running", StartedAt: startedAt}
			validData, err := json.Marshal(validRecord)
			if err != nil {
				t.Fatal(err)
			}
			validMeta := filepath.Join(validDir, "job.json")
			if err := os.WriteFile(validMeta, validData, 0o644); err != nil {
				t.Fatal(err)
			}

			var invalidData []byte
			if test.malformed != nil {
				invalidData = test.malformed
			} else if !test.missing {
				record := JobRecord{ID: "job_invalid", Command: "sleep", PID: os.Getpid(), Status: "running", StartedAt: startedAt}
				if test.mutate != nil {
					test.mutate(&record)
				}
				invalidData, err = json.Marshal(record)
				if err != nil {
					t.Fatal(err)
				}
			}
			if invalidData != nil {
				if err := os.WriteFile(filepath.Join(invalidDir, "job.json"), invalidData, 0o644); err != nil {
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
			signals := 0
			processSignal = func(int, syscall.Signal) error {
				signals++
				return nil
			}
			psRunnerOverride = matchingPSRunner()
			processProbeFn = func(int) bool { return false }

			if err := KillSessionForProfile("test", sessionID); err == nil {
				t.Fatal("malformed job metadata unexpectedly allowed kill")
			}
			if signals != 0 {
				t.Fatalf("processSignal called %d times, want 0", signals)
			}
			var got JobRecord
			if err := readJSON(validMeta, &got); err != nil {
				t.Fatal(err)
			}
			if got.Status != "running" {
				t.Fatalf("valid job status changed before complete preflight: %q", got.Status)
			}
			if _, err := os.Stat(invalidDir); err != nil {
				t.Fatalf("malformed job directory was mutated: %v", err)
			}
			if invalidData != nil {
				gotData, err := os.ReadFile(filepath.Join(invalidDir, "job.json"))
				if err != nil {
					t.Fatal(err)
				}
				if string(gotData) != string(invalidData) {
					t.Fatalf("malformed metadata changed: want %q got %q", invalidData, gotData)
				}
			}
		})
	}
}

func TestKillSessionSkipsStructurallyValidNonRunningJob(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const sessionID = "test__terminal-job"
	jobDir := filepath.Join(sessionDirForProfile("test", sessionID), "jobs", "job_exited")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().UTC().Format(time.RFC3339Nano)
	data, err := json.Marshal(JobRecord{ID: "job_exited", Command: "sleep", PID: os.Getpid(), Status: "exited", StartedAt: startedAt})
	if err != nil {
		t.Fatal(err)
	}
	metaPath := filepath.Join(jobDir, "job.json")
	if err := os.WriteFile(metaPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	previousSignal := processSignal
	previousPS := psRunnerOverride
	t.Cleanup(func() {
		processSignal = previousSignal
		psRunnerOverride = previousPS
	})
	signals := 0
	processSignal = func(int, syscall.Signal) error {
		signals++
		return nil
	}
	psRunnerOverride = matchingPSRunner()

	if err := KillSessionForProfile("test", sessionID); err != nil {
		t.Fatalf("KillSessionForProfile: %v", err)
	}
	if signals != 0 {
		t.Fatalf("processSignal called %d times for a non-running job", signals)
	}
	got, err := os.ReadFile(metaPath)
	if err != nil || string(got) != string(data) {
		t.Fatalf("valid non-running record changed: data=%q err=%v", got, err)
	}
}

func TestJobSessionAPIsRejectUnsafeIDs(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	for _, test := range []struct {
		name      string
		sessionID string
	}{
		{name: "empty", sessionID: ""},
		{name: "traversal", sessionID: "../outside"},
		{name: "separator", sessionID: `test\\outside`},
	} {
		t.Run(test.name, func(t *testing.T) {
			sessionID := test.sessionID
			if _, err := ListForProfile("test", sessionID); err == nil {
				t.Fatal("ListForProfile accepted unsafe session ID")
			}
			if _, err := RunningCountForProfile("test", sessionID); err == nil {
				t.Fatal("RunningCountForProfile accepted unsafe session ID")
			}
			if err := PruneStaleSessionForProfile("test", sessionID); err == nil {
				t.Fatal("PruneStaleSessionForProfile accepted unsafe session ID")
			}
			if _, err := PrepareKillSessionForProfile("test", sessionID); err == nil {
				t.Fatal("PrepareKillSessionForProfile accepted unsafe session ID")
			}
		})
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

	readStatus := func(jobDir string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(jobDir, "job.json"))
		if err != nil {
			t.Fatal(err)
		}
		var record JobRecord
		if err := json.Unmarshal(data, &record); err != nil {
			t.Fatal(err)
		}
		return record.Status
	}

	if err := CleanupStaleForProfile(profileA); err != nil {
		t.Fatal(err)
	}
	if got := readStatus(jobA); got != "exited" {
		t.Fatalf("profile A stale status = %q, want exited", got)
	}
	if got := readStatus(jobB); got != "running" {
		t.Fatalf("explicit profile cleanup changed profile B status to %q", got)
	}

	if err := CleanupStale(); err != nil {
		t.Fatal(err)
	}
	if got := readStatus(jobB); got != "exited" {
		t.Fatalf("all-profile cleanup status = %q, want exited", got)
	}
}

func TestCachedReaderInvalidateRemovesOneSession(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	calls := 0
	reader := newCachedReader(func(string) ([]Job, error) {
		calls++
		return []Job{{ID: "job"}}, nil
	})
	const profile = "invalidate-profile"
	const sessionID = "invalidate-profile__chat"
	for range 2 {
		if _, err := reader.ListForProfile(profile, sessionID); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("list calls before invalidation = %d, want 1", calls)
	}
	reader.Invalidate(profile, sessionID)
	if _, err := reader.ListForProfile(profile, sessionID); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("list calls after invalidation = %d, want 2", calls)
	}
}

func TestCachedReaderIOOutsideMutexAndInvalidationWins(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	reader := NewCachedReader()
	started := make(chan struct{}, 1)
	release := make(chan struct{}, 1)
	firstCall := true
	calls := 0
	reader.listProfileFn = func(string, string) ([]Job, error) {
		calls++
		if firstCall {
			firstCall = false
			started <- struct{}{}
			<-release
		}
		return []Job{{ID: fmt.Sprintf("job-%d", calls)}}, nil
	}

	const profile = "concurrent-profile"
	const sessionID = "concurrent-profile__chat"
	result := make(chan error, 1)
	go func() {
		_, err := reader.ListForProfile(profile, sessionID)
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("reader did not reach injected job I/O")
	}

	invalidated := make(chan struct{})
	go func() {
		reader.Invalidate(profile, sessionID)
		close(invalidated)
	}()
	select {
	case <-invalidated:
	case <-time.After(time.Second):
		release <- struct{}{}
		t.Fatal("Invalidate blocked behind job listing")
	}
	release <- struct{}{}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("ListForProfile returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("reader did not finish after injected I/O was released")
	}

	jobs, err := reader.ListForProfile(profile, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(jobs) != 1 || jobs[0].ID != "job-2" {
		t.Fatalf("post-invalidation jobs = %+v, list calls = %d", jobs, calls)
	}
}

func TestCachedReaderBoundsEntriesAndSkipsOversizedMetadata(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	reader := NewCachedReader()
	for index := range 500 {
		sessionID := fmt.Sprintf("bounded-jobs__chat-%d", index)
		if _, err := reader.ListForProfile("bounded-jobs", sessionID); err != nil {
			t.Fatalf("list session %d: %v", index, err)
		}
	}
	if got := reader.cache.Len(); got > cache.ReaderCacheCapacity {
		t.Fatalf("cache entries = %d, exceeds capacity %d", got, cache.ReaderCacheCapacity)
	}

	const profile = "oversized-jobs"
	const sessionID = "oversized-jobs__session"
	jobDir := filepath.Join(paths.SessionDir(profile, sessionID), "jobs", "large-job")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := `{"id":"large-job","status":"exited","payload":"` + strings.Repeat("x", int(cache.MaxSourceMetadataBytes)+1) + `"}`
	if err := os.WriteFile(filepath.Join(jobDir, "job.json"), []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ListForProfile(profile, sessionID); err != nil {
		t.Fatalf("list oversized source: %v", err)
	}
	if _, cached := reader.cache.Get(profile + "\x00" + sessionID); cached {
		t.Fatal("oversized job metadata was cached")
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
