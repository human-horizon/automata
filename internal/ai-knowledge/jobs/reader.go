package jobs

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/HumanHorizon/automata/internal/paths"
)

// JobRecord matches the structure written by the pi job extension.
type JobRecord struct {
	ID        string `json:"id"`
	Command   string `json:"command"`
	PID       int    `json:"pid"`
	Status    string `json:"status"`
	StartedAt string `json:"startedAt"`
	StoppedAt string `json:"stoppedAt,omitempty"`
	ExitCode  *int   `json:"exitCode,omitempty"`
	Agent     string `json:"agent,omitempty"`
}

type Job struct {
	ID        string
	Command   string
	Running   bool
	UpdatedAt time.Time
	Agent     string
}

func dataHome() string {
	return paths.BaseDir()
}

// profileFromSessionID extracts the already canonical profile slug from the
// leading `profile__` prefix baked into session IDs.
func profileFromSessionID(sessionID string) string {
	const sep = "__"
	idx := strings.Index(sessionID, sep)
	if idx <= 0 {
		return ""
	}
	return sessionID[:idx]
}

func sessionDirForProfile(profile, sessionID string) string {
	if profile == "" {
		profile = os.Getenv("AI_PROFILE")
	}
	return paths.SessionDir(profile, sessionID)
}

// sessionDir returns the session data directory.
func sessionDir(sessionID string) string {
	return sessionDirForProfile(profileFromSessionID(sessionID), sessionID)
}

// pidStartSkewTolerance is the maximum allowed difference between the job's
// recorded start time and `ps -o lstart=`. macOS kqueue can report a start
// time a few seconds off from the kernel; 5s comfortably covers that without
// risking false negatives for legitimately recycled PIDs.
const pidStartSkewTolerance = 5 * time.Second

// psRunner abstracts exec.Command so tests can swap it for a fake `ps` to
// simulate absence, garbage output, or skewed start times.
type psRunner func(pid int) (string, error)

// defaultPsRunner shells out to the real `ps` binary. The PID is taken from
// the job record; on any error (binary missing, permission, dead process)
// the caller treats the situation as "ps is inconclusive".
func defaultPsRunner(pid int) (string, error) {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "lstart=").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

var psRunnerOverride psRunner

// pidIsSameProcess reports whether `pid` is still the same process the job
// record was started for. The decision order is intentional:
//
//  1. A dead PID (kill(0) errors) is never the same process, no matter what
//     startedAt or ps claims — recycled PIDs must NOT count as live jobs.
//  2. When ps is missing/garbled we trust kill(0) and return true. Losing a
//     stale row to the panel is a worse UX than briefly holding one extra.
//  3. When ps is healthy we still require the start time to line up with
//     startedAt within `pidStartSkewTolerance`. A recycled PID is a different
//     process regardless of whether it is currently running.
func pidIsSameProcess(pid int, startedAt string) bool {
	runner := psRunnerOverride
	if runner == nil {
		runner = defaultPsRunner
	}
	return pidIsSameProcessWithRunner(pid, startedAt, runner)
}

func pidIsSameProcessWithRunner(pid int, startedAt string, runner psRunner) bool {
	if pid <= 0 {
		return false
	}

	// kill(0) is the source of truth for "process exists". On Linux this only
	// succeeds if the PID is alive and we have permission to signal it; on
	// macOS the same check is also the cheapest race-free probe.
	if err := syscall.Kill(pid, 0); err != nil {
		return false
	}

	psStart, err := runPs(runner, pid)
	if err != nil || psStart == "" {
		// kill(0) just told us the process is alive. If we cannot reach ps
		// (e.g. PATH stripped, container without /bin/ps) we still consider
		// the job live — losing the row to the panel would be worse than
		// briefly showing a recycled PID, and PruneStaleSession will fix
		// any real mismatch on the next event.
		return true
	}

	if startedAt == "" {
		// Legacy record without a start time: trust kill(0).
		return true
	}

	jobTime, err := time.Parse(time.RFC3339, startedAt)
	if err != nil {
		return true
	}

	psTime, err := time.ParseInLocation("Mon Jan 2 15:04:05 2006", psStart, time.Local)
	if err != nil {
		return true
	}
	psTime = psTime.UTC()

	diff := jobTime.Sub(psTime)
	if diff < 0 {
		diff = -diff
	}
	return diff <= pidStartSkewTolerance
}

func runPs(runner psRunner, pid int) (string, error) {
	if runner == nil {
		runner = defaultPsRunner
	}
	return runner(pid)
}

// writeJSON writes a JobRecord back to disk.
func writeJSON(path string, rec *JobRecord) error {
	data, err := json.MarshalIndent(rec, "", "    ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// List returns running jobs for the given session. The function is read-only
// and never rewrites job.json on disk: callers that want to materialise the
// "process is dead" decision must invoke PruneStaleSession explicitly. This
// keeps panel reads cheap and prevents a transient `ps` hiccup from erasing
// the metadata of a job that is still legitimately running.
func listForProfile(profile, sessionID string) ([]Job, error) {
	jobsDir := filepath.Join(sessionDirForProfile(profile, sessionID), "jobs")
	entries, err := os.ReadDir(jobsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var jobs []Job
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		metaPath := filepath.Join(jobsDir, entry.Name(), "job.json")
		var rec JobRecord
		if err := readJSON(metaPath, &rec); err != nil || rec.ID == "" {
			continue
		}

		if rec.Status != "running" {
			continue
		}
		if !pidIsSameProcess(rec.PID, rec.StartedAt) {
			// Stale record — leave the on-disk status alone. The UI will
			// trigger PruneStaleSession to materialise the cleanup, which
			// gives us a single, deliberate place where the metadata flips.
			continue
		}

		info, err := os.Stat(metaPath)
		if err != nil {
			continue
		}
		jobs = append(jobs, Job{
			ID:        rec.ID,
			Command:   rec.Command,
			Running:   true,
			UpdatedAt: info.ModTime(),
			Agent:     rec.Agent,
		})
	}

	return jobs, nil
}

// List returns running jobs using the profile encoded in the session ID or
// AI_PROFILE for legacy unprefixed IDs.
func List(sessionID string) ([]Job, error) {
	return listForProfile("", sessionID)
}

// ListForProfile returns running jobs under an explicit canonical profile.
func ListForProfile(profile, sessionID string) ([]Job, error) {
	return listForProfile(profile, sessionID)
}

// PruneStaleSession scans a session's jobs/ directory and, for every record
// whose PID is no longer the same process, marks it `exited` and removes the
// record directory. It is the only path that mutates running→exited in
// job.json, which makes the cleanup behaviour easy to test in isolation.
func pruneStaleSessionForProfile(profile, sessionID string) error {
	jobsDir := filepath.Join(sessionDirForProfile(profile, sessionID), "jobs")
	entries, err := os.ReadDir(jobsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		jobDir := filepath.Join(jobsDir, entry.Name())
		metaPath := filepath.Join(jobDir, "job.json")
		var rec JobRecord
		if err := readJSON(metaPath, &rec); err != nil || rec.ID == "" {
			continue
		}
		if rec.Status != "running" {
			continue
		}
		if pidIsSameProcess(rec.PID, rec.StartedAt) {
			continue
		}
		rec.Status = "exited"
		rec.StoppedAt = now
		_ = writeJSON(metaPath, &rec) // best-effort
		_ = os.RemoveAll(jobDir)
	}
	return nil
}

// PruneStaleSession marks dead jobs using the profile encoded in the session
// ID or AI_PROFILE for legacy unprefixed IDs.
func PruneStaleSession(sessionID string) error {
	return pruneStaleSessionForProfile(profileFromSessionID(sessionID), sessionID)
}

// PruneStaleSessionForProfile marks dead jobs under an explicit profile.
func PruneStaleSessionForProfile(profile, sessionID string) error {
	return pruneStaleSessionForProfile(profile, sessionID)
}

// RunningCount returns the number of running job records on disk for the
// given session, regardless of whether the process is still alive. It is a
// cheap probe for callers that want to know "is there anything to look at?"
// before doing the more expensive pidIsSameProcess sweep.
func runningCountForProfile(profile, sessionID string) (int, error) {
	jobsDir := filepath.Join(sessionDirForProfile(profile, sessionID), "jobs")
	entries, err := os.ReadDir(jobsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		var rec JobRecord
		if err := readJSON(filepath.Join(jobsDir, entry.Name(), "job.json"), &rec); err != nil {
			continue
		}
		if rec.Status == "running" {
			count++
		}
	}
	return count, nil
}

// RunningCount returns the number of running job records using the profile
// encoded in the session ID or AI_PROFILE for legacy unprefixed IDs.
func RunningCount(sessionID string) (int, error) {
	return runningCountForProfile("", sessionID)
}

// RunningCountForProfile returns the number of running records under an
// explicit canonical profile.
func RunningCountForProfile(profile, sessionID string) (int, error) {
	return runningCountForProfile(profile, sessionID)
}

// processSignal is injectable so cleanup tests can verify signal safety without
// sending SIGTERM to a real process.
var processSignal = func(pid int, signal syscall.Signal) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Signal(signal)
}

func materializeStaleJob(jobDir, metaPath string, rec *JobRecord) error {
	rec.Status = "exited"
	rec.StoppedAt = time.Now().UTC().Format(time.RFC3339)
	if err := writeJSON(metaPath, rec); err != nil {
		return err
	}
	return os.RemoveAll(jobDir)
}

// KillSession uses the profile encoded in the session ID for compatibility
// with the legacy command-line API.
func KillSession(sessionID string) error {
	return KillSessionForProfile(profileFromSessionID(sessionID), sessionID)
}

// KillSessionForProfile terminates jobs under an explicit canonical profile.
// Dead or recycled records are materialized as stale without sending a
// signal. A failed signal leaves the running metadata untouched and returns
// the error to the caller.
func KillSessionForProfile(profile, sessionID string) error {
	jobsDir := filepath.Join(sessionDirForProfile(profile, sessionID), "jobs")
	entries, err := os.ReadDir(jobsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		jobDir := filepath.Join(jobsDir, entry.Name())
		metaPath := filepath.Join(jobDir, "job.json")
		var rec JobRecord
		if err := readJSON(metaPath, &rec); err != nil || rec.ID == "" {
			continue
		}
		if rec.Status != "running" {
			continue
		}

		if !pidIsSameProcess(rec.PID, rec.StartedAt) {
			if err := materializeStaleJob(jobDir, metaPath, &rec); err != nil {
				return fmt.Errorf("materialize stale job %s: %w", rec.ID, err)
			}
			continue
		}
		if err := processSignal(rec.PID, syscall.SIGTERM); err != nil {
			return fmt.Errorf("signal job %s: %w", rec.ID, err)
		}

		rec.Status = "exited"
		rec.StoppedAt = time.Now().UTC().Format(time.RFC3339)
		if err := writeJSON(metaPath, &rec); err != nil {
			return fmt.Errorf("write stopped job %s: %w", rec.ID, err)
		}
	}
	return nil
}

func cleanupStaleProfile(profile string) error {
	sessions, err := os.ReadDir(filepath.Join(paths.ProfileDir(profile), "sessions"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, session := range sessions {
		if !session.IsDir() {
			continue
		}
		if err := PruneStaleSessionForProfile(profile, session.Name()); err != nil {
			return err
		}
	}
	return nil
}

// CleanupStaleForProfile cleans stale jobs under one explicit profile.
func CleanupStaleForProfile(profile string) error {
	return cleanupStaleProfile(profile)
}

// CleanupStale scans all profile directories and cleans each one using its
// explicit profile scope. It never relies on AI_PROFILE while iterating.
func CleanupStale() error {
	profiles, err := os.ReadDir(filepath.Join(dataHome(), "profiles"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, profile := range profiles {
		if !profile.IsDir() {
			continue
		}
		if err := cleanupStaleProfile(profile.Name()); err != nil {
			return err
		}
	}
	return nil
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// CachedReader caches running jobs until the set of nested job.json files changes.
type CachedReader struct {
	mu            sync.Mutex
	cache         map[string]cachedJobsEntry
	listFn        func(string) ([]Job, error)
	listProfileFn func(string, string) ([]Job, error)
}

type cachedJobsEntry struct {
	jobs      []Job
	signature string
}

func NewCachedReader() *CachedReader {
	return &CachedReader{
		cache:         make(map[string]cachedJobsEntry),
		listFn:        List,
		listProfileFn: ListForProfile,
	}
}

func newCachedReader(listFn func(string) ([]Job, error)) *CachedReader {
	return &CachedReader{
		cache:         make(map[string]cachedJobsEntry),
		listFn:        listFn,
		listProfileFn: func(_ string, sessionID string) ([]Job, error) { return listFn(sessionID) },
	}
}

func jobsSignature(jobsDir string) string {
	entries, err := os.ReadDir(jobsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "missing"
		}
		return "unreadable"
	}

	var signature strings.Builder
	signature.WriteString("jobs:")
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		signature.WriteString(entry.Name())
		signature.WriteByte(':')
		info, err := os.Stat(filepath.Join(jobsDir, entry.Name(), "job.json"))
		if err != nil {
			signature.WriteString("missing;")
			continue
		}
		signature.WriteString(strconv.FormatInt(info.Size(), 10))
		signature.WriteByte(':')
		signature.WriteString(strconv.FormatInt(info.ModTime().UnixNano(), 10))
		signature.WriteByte(';')
	}
	return signature.String()
}

func (r *CachedReader) List(sessionID string) ([]Job, error) {
	return r.ListForProfile("", sessionID)
}

func (r *CachedReader) ListForProfile(profile, sessionID string) ([]Job, error) {
	if sessionID == "" {
		return nil, nil
	}
	jobsDir := filepath.Join(sessionDirForProfile(profile, sessionID), "jobs")
	signature := jobsSignature(jobsDir)
	cacheKey := profile + "\x00" + sessionID

	r.mu.Lock()
	defer r.mu.Unlock()

	if entry, ok := r.cache[cacheKey]; ok && entry.signature == signature {
		return entry.jobs, nil
	}

	listFn := r.listProfileFn
	if listFn == nil {
		listFn = func(_ string, id string) ([]Job, error) { return r.listFn(id) }
	}
	jobs, err := listFn(profile, sessionID)
	if err != nil {
		return jobs, err
	}
	r.cache[cacheKey] = cachedJobsEntry{jobs: jobs, signature: signature}
	return jobs, nil
}
