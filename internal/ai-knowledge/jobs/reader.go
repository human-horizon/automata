package jobs

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
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

// dataHome returns the base directory for ai-knowledge data.
func dataHome() string {
	if v := os.Getenv("AI_DATA_HOME"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "/Users/a"
	}
	return filepath.Join(home, ".ai", "automata")
}

// profileFromSessionID extracts the profile slug from the leading `profile__`
// prefix that Automata bakes into session IDs. Returns "" if the session ID has
// no profile prefix.
func profileFromSessionID(sessionID string) string {
	const sep = "__"
	idx := strings.Index(sessionID, sep)
	if idx <= 0 {
		return ""
	}
	return slugify(sessionID[:idx])
}

// profileSlug returns the profile directory name. Empty profile maps to "default".
func profileSlug() string {
	profile := os.Getenv("AI_PROFILE")
	if profile == "" {
		return "default"
	}
	return slugify(profile)
}

func slugify(s string) string {
	var out strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			out.WriteRune(r)
		} else if r >= 'A' && r <= 'Z' {
			out.WriteRune(r + 32) // tolower
		} else if r == ' ' || r == '.' {
			out.WriteRune('-')
		}
	}
	return out.String()
}

// sessionDir returns the session data directory.
func sessionDir(sessionID string) string {
	profile := profileFromSessionID(sessionID)
	if profile == "" {
		profile = profileSlug()
	}
	return filepath.Join(dataHome(), "profiles", profile, "sessions", sessionID)
}

// pidIsSameProcess checks if the given PID is running AND its start time
// matches the expected start time. This avoids the PID recycling problem
// where a dead process's PID is reused by a new process.
func pidIsSameProcess(pid int, startedAt string) bool {
	if pid <= 0 || startedAt == "" {
		return false
	}

	// Use ps to get the process start time. Format: "Mon Jan 2 15:04:05 2006"
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "lstart=").Output()
	if err != nil {
		return false
	}

	psStart := strings.TrimSpace(string(out))
	if psStart == "" {
		return false
	}

	// Parse the job's startedAt (RFC3339 format).
	jobTime, err := time.Parse(time.RFC3339, startedAt)
	if err != nil {
		return false
	}

	// Parse ps output. The format is like "Mon Jun 30 14:22:35 2026".
	// ps outputs in local time, but startedAt is in UTC. Parse in local time
	// then convert to UTC for comparison.
	psTime, err := time.ParseInLocation("Mon Jan 2 15:04:05 2006", psStart, time.Local)
	if err != nil {
		// If parsing fails, fall back to just checking if the PID exists.
		return true
	}
	psTime = psTime.UTC()

	// Compare times. Allow 2 second tolerance for clock skew.
	diff := jobTime.Sub(psTime)
	if diff < 0 {
		diff = -diff
	}
	return diff < 2*time.Second
}

// writeJSON writes a JobRecord back to disk.
func writeJSON(path string, rec *JobRecord) error {
	data, err := json.MarshalIndent(rec, "", "    ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// List returns running jobs for the given session.
// Stale jobs (status="running" but process dead) are silently marked as "exited".
func List(sessionID string) ([]Job, error) {
	jobsDir := filepath.Join(sessionDir(sessionID), "jobs")
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

		// Clean up stale "running" jobs whose process is already dead
		// or whose PID has been recycled by a different process.
		if rec.Status == "running" && !pidIsSameProcess(rec.PID, rec.StartedAt) {
			rec.Status = "exited"
			rec.StoppedAt = time.Now().UTC().Format(time.RFC3339)
			_ = writeJSON(metaPath, &rec) // best-effort
			continue
		}

		if rec.Status != "running" {
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

// KillSession kills all running jobs for the given session and marks them as exited.
func KillSession(sessionID string) error {
	jobsDir := filepath.Join(sessionDir(sessionID), "jobs")
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
		metaPath := filepath.Join(jobsDir, entry.Name(), "job.json")
		var rec JobRecord
		if err := readJSON(metaPath, &rec); err != nil || rec.ID == "" {
			continue
		}
		if rec.Status != "running" {
			continue
		}

		// Kill the process.
		if rec.PID > 0 {
			_ = exec.Command("kill", strconv.Itoa(rec.PID)).Run()
		}

		// Mark as exited.
		rec.Status = "exited"
		rec.StoppedAt = time.Now().UTC().Format(time.RFC3339)
		_ = writeJSON(metaPath, &rec) // best-effort
	}
	return nil
}

// CleanupStale scans all sessions in all profiles and marks stale "running"
// jobs as "exited". This is called at startup to clean up jobs that were left
// behind after a crash or unclean shutdown.
func CleanupStale() error {
	profilesDir := filepath.Join(dataHome(), "profiles")
	profiles, err := os.ReadDir(profilesDir)
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
		sessionsDir := filepath.Join(profilesDir, profile.Name(), "sessions")
		sessions, err := os.ReadDir(sessionsDir)
		if err != nil {
			continue
		}
		for _, session := range sessions {
			if !session.IsDir() {
				continue
			}
			jobsDir := filepath.Join(sessionsDir, session.Name(), "jobs")
			entries, err := os.ReadDir(jobsDir)
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				metaPath := filepath.Join(jobsDir, entry.Name(), "job.json")
				var rec JobRecord
				if err := readJSON(metaPath, &rec); err != nil || rec.ID == "" {
					continue
				}
				if rec.Status == "running" && !pidIsSameProcess(rec.PID, rec.StartedAt) {
					rec.Status = "exited"
					rec.StoppedAt = time.Now().UTC().Format(time.RFC3339)
					_ = writeJSON(metaPath, &rec) // best-effort
				}
			}
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

// CachedReader caches running-jobs list by jobs/ directory mtime.
type CachedReader struct {
	mu    sync.Mutex
	cache map[string]cachedJobsEntry
}

type cachedJobsEntry struct {
	jobs     []Job
	maxMtime time.Time
}

func NewCachedReader() *CachedReader {
	return &CachedReader{cache: make(map[string]cachedJobsEntry)}
}

func (r *CachedReader) List(sessionID string) ([]Job, error) {
	if sessionID == "" {
		return nil, nil
	}
	jobsDir := filepath.Join(sessionDir(sessionID), "jobs")

	var maxMtime time.Time
	if fi, err := os.Stat(jobsDir); err == nil {
		maxMtime = fi.ModTime()
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if entry, ok := r.cache[sessionID]; ok && !maxMtime.IsZero() && entry.maxMtime.Equal(maxMtime) {
		return entry.jobs, nil
	}

	jobs, err := List(sessionID)
	if err != nil {
		return jobs, err
	}
	r.cache[sessionID] = cachedJobsEntry{jobs: jobs, maxMtime: maxMtime}
	return jobs, nil
}
