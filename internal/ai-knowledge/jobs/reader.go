package jobs

import (
	"encoding/json"
	"errors"
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

// PIDIdentity describes how confidently a job record can be associated with
// the process currently occupying its PID.
type PIDIdentity int

const (
	PIDDead PIDIdentity = iota
	PIDSame
	PIDDifferent
	PIDUnknown
)

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

// inspectPIDIdentity distinguishes a dead PID, a definitely different
// process, a matching process, and an inconclusive identity check. The last
// state is intentionally separate: read-only callers may keep showing a job,
// while destructive callers must refuse to signal it.
func inspectPIDIdentity(pid int, startedAt string) PIDIdentity {
	runner := psRunnerOverride
	if runner == nil {
		runner = defaultPsRunner
	}
	return inspectPIDIdentityWithRunner(pid, startedAt, runner)
}

func inspectPIDIdentityWithRunner(pid int, startedAt string, runner psRunner) PIDIdentity {
	if pid <= 0 {
		return PIDDead
	}

	if err := syscall.Kill(pid, 0); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return PIDDead
		}
		return PIDUnknown
	}

	psStart, err := runPs(runner, pid)
	if err != nil || psStart == "" {
		return PIDUnknown
	}
	if startedAt == "" {
		return PIDUnknown
	}

	jobTime, err := time.Parse(time.RFC3339, startedAt)
	if err != nil {
		return PIDUnknown
	}
	psTime, err := time.ParseInLocation("Mon Jan 2 15:04:05 2006", psStart, time.Local)
	if err != nil {
		return PIDUnknown
	}
	psTime = psTime.UTC()

	diff := jobTime.Sub(psTime)
	if diff < 0 {
		diff = -diff
	}
	if diff <= pidStartSkewTolerance {
		return PIDSame
	}
	return PIDDifferent
}

// pidIsSameProcess preserves the conservative read-only behavior used by the
// job list and stale-prune paths: an unknown identity remains visible rather
// than being treated as stale. KillSessionForProfile uses the full identity
// value and therefore fails closed for PIDUnknown.
func pidIsSameProcess(pid int, startedAt string) bool {
	identity := inspectPIDIdentity(pid, startedAt)
	return identity == PIDSame || identity == PIDUnknown
}

func pidIsSameProcessWithRunner(pid int, startedAt string, runner psRunner) bool {
	identity := inspectPIDIdentityWithRunner(pid, startedAt, runner)
	return identity == PIDSame || identity == PIDUnknown
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

// processProbe is injectable so KillSessionForProfile can distinguish a
// delivered SIGTERM from a confirmed process exit without making tests sleep
// on real processes.
type processProbe func(pid int) bool

var processProbeFn processProbe = func(pid int) bool {
	return pid > 0 && syscall.Kill(pid, 0) == nil
}

const (
	processExitProbeAttempts = 5
	processExitProbeInterval = 20 * time.Millisecond
)

func waitForProcessExit(pid int) bool {
	for attempt := 0; attempt < processExitProbeAttempts; attempt++ {
		if !processProbeFn(pid) {
			return true
		}
		if attempt+1 < processExitProbeAttempts {
			time.Sleep(processExitProbeInterval)
		}
	}
	return false
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

type killCandidate struct {
	jobDirName string
	record     JobRecord
	identity   PIDIdentity
}

// KillPlan is an immutable snapshot of jobs whose process identity was
// inspected before any destructive signal was sent. The session ID used at
// execution time may differ from the one used during preparation when a
// rename or move has committed the session directory migration.
type KillPlan struct {
	profile    string
	sessionID  string
	candidates []killCandidate
}

// PrepareKillSessionForProfile reads and verifies every running job under an
// explicit profile without sending signals or mutating metadata. Unknown
// identity fails closed, so callers can prepare several sessions before any
// destructive commit begins.
func PrepareKillSessionForProfile(profile, sessionID string) (*KillPlan, error) {
	jobsDir := filepath.Join(sessionDirForProfile(profile, sessionID), "jobs")
	plan := &KillPlan{profile: profile, sessionID: sessionID}
	entries, err := os.ReadDir(jobsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return plan, nil
		}
		return nil, err
	}

	plan.candidates = make([]killCandidate, 0, len(entries))
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

		identity := inspectPIDIdentity(rec.PID, rec.StartedAt)
		if identity == PIDUnknown {
			return nil, fmt.Errorf("cannot verify process identity for job %s", rec.ID)
		}
		plan.candidates = append(plan.candidates, killCandidate{
			jobDirName: entry.Name(),
			record:     rec,
			identity:   identity,
		})
	}
	return plan, nil
}

// Execute terminates the prepared jobs in their original session directory.
func (p *KillPlan) Execute() error {
	if p == nil {
		return nil
	}
	return p.ExecuteForProfile(p.profile, p.sessionID)
}

// ExecuteForProfile terminates a prepared plan under sessionID. Re-checking
// identity immediately before signalling keeps the fail-closed guarantee when
// a PID changes between prepare and commit. All candidates are attempted and
// failures are joined so one failed signal cannot hide later cleanup work.
func (p *KillPlan) ExecuteForProfile(profile, sessionID string) error {
	if p == nil {
		return nil
	}
	jobsDir := filepath.Join(sessionDirForProfile(profile, sessionID), "jobs")
	var failures []error
	for _, candidate := range p.candidates {
		rec := candidate.record
		jobDir := filepath.Join(jobsDir, candidate.jobDirName)
		metaPath := filepath.Join(jobDir, "job.json")

		identity := inspectPIDIdentity(rec.PID, rec.StartedAt)
		if identity == PIDUnknown {
			failures = append(failures, fmt.Errorf("cannot verify process identity for job %s", rec.ID))
			continue
		}
		if identity == PIDDead || identity == PIDDifferent {
			if _, err := os.Stat(jobDir); os.IsNotExist(err) {
				continue
			} else if err != nil {
				failures = append(failures, fmt.Errorf("stat stale job %s: %w", rec.ID, err))
				continue
			}
			if err := materializeStaleJob(jobDir, metaPath, &rec); err != nil {
				failures = append(failures, fmt.Errorf("materialize stale job %s: %w", rec.ID, err))
			}
			continue
		}

		if _, err := os.Stat(jobDir); err != nil {
			failures = append(failures, fmt.Errorf("locate job %s after session migration: %w", rec.ID, err))
			continue
		}
		if err := processSignal(rec.PID, syscall.SIGTERM); err != nil {
			failures = append(failures, fmt.Errorf("signal job %s: %w", rec.ID, err))
			continue
		}
		if !waitForProcessExit(rec.PID) {
			failures = append(failures, fmt.Errorf("job %s is still running after SIGTERM", rec.ID))
			continue
		}

		rec.Status = "exited"
		rec.StoppedAt = time.Now().UTC().Format(time.RFC3339)
		if err := writeJSON(metaPath, &rec); err != nil {
			failures = append(failures, fmt.Errorf("write stopped job %s: %w", rec.ID, err))
		}
	}
	return errors.Join(failures...)
}

// KillSessionForProfile prepares and executes all jobs under an explicit
// canonical profile. Preparation is signal-free; execution attempts every
// verified candidate and returns an aggregate error when any cleanup fails.
func KillSessionForProfile(profile, sessionID string) error {
	plan, err := PrepareKillSessionForProfile(profile, sessionID)
	if err != nil {
		return err
	}
	return plan.Execute()
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
