package actions

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/HumanHorizon/automata/internal/atomicfile"
	"github.com/HumanHorizon/automata/internal/paths"
)

const runtimeVersion = 1

var validRunID = regexp.MustCompile(`^[0-9a-f]{32}$`)

// RunRecord is the shared wire format used by Automata UI and Pi Action tools.
type RunRecord struct {
	Version   int       `json:"version"`
	RunID     string    `json:"runId"`
	Profile   string    `json:"profile"`
	Domain    string    `json:"domain"`
	ActionID  string    `json:"actionId"`
	Source    string    `json:"source"`
	StartedAt time.Time `json:"startedAt"`
	PID       int       `json:"pid,omitempty"`
}

// EnsureRuntimeDirectory creates and validates the folder Action runtime directory.
func EnsureRuntimeDirectory(profile, domain string) (string, error) {
	actionDir, err := Directory(profile, domain)
	if err != nil {
		return "", err
	}
	if err := verifyActionPath(profile, domain, actionDir); err != nil {
		return "", err
	}
	runtimeDir := filepath.Join(actionDir, "runtime")
	if err := paths.EnsurePrivateDir(runtimeDir); err != nil {
		return "", fmt.Errorf("prepare action runtime directory: %w", err)
	}
	if err := verifyRealDirectory(runtimeDir); err != nil {
		return "", err
	}
	return runtimeDir, nil
}

// ClaimRun reserves one Action for exactly one process across Pi and Automata.
func ClaimRun(profile, domain, actionID, runID, source string) (RunRecord, error) {
	if !validActionID(actionID) || !validRunID.MatchString(runID) {
		return RunRecord{}, errors.New("invalid action runtime identity")
	}
	if source != "pi" && source != "automata-ui" {
		return RunRecord{}, errors.New("invalid action runtime source")
	}
	actionDir, err := Directory(profile, domain)
	if err != nil {
		return RunRecord{}, err
	}
	if err := verifyActionPath(profile, domain, actionDir); err != nil {
		return RunRecord{}, err
	}
	runtimeDir, err := EnsureRuntimeDirectory(profile, domain)
	if err != nil {
		return RunRecord{}, err
	}
	record := RunRecord{
		Version: runtimeVersion, RunID: runID,
		Profile: paths.ProfileSlug(profile), Domain: domain, ActionID: actionID,
		Source: source, StartedAt: time.Now().UTC(),
	}
	data, err := json.Marshal(record)
	if err != nil {
		return RunRecord{}, err
	}
	filePath := runRecordPath(runtimeDir, actionID)
	temporary, err := os.CreateTemp(runtimeDir, ".action-run-*.tmp")
	if err != nil {
		return RunRecord{}, fmt.Errorf("create action runtime temp: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(paths.PrivateFileMode); err != nil {
		return RunRecord{}, err
	}
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		return RunRecord{}, err
	}
	if err := temporary.Sync(); err != nil {
		return RunRecord{}, err
	}
	if err := temporary.Close(); err != nil {
		return RunRecord{}, err
	}
	if err := os.Link(temporaryPath, filePath); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return RunRecord{}, errors.New("action is already running")
		}
		return RunRecord{}, fmt.Errorf("claim action run: %w", err)
	}
	committed = true
	if err := os.Remove(temporaryPath); err != nil {
		return RunRecord{}, fmt.Errorf("remove action runtime temp: %w", err)
	}
	return record, nil
}

// PublishRunPID publishes the process identity only while the same run owns the claim.
func PublishRunPID(profile, domain, actionID, runID string, pid int) error {
	if pid <= 0 {
		return errors.New("invalid action process ID")
	}
	record, filePath, err := ReadRun(profile, domain, actionID)
	if err != nil {
		return err
	}
	if record.RunID != runID {
		return errors.New("action runtime ownership changed")
	}
	record.PID = pid
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := atomicfile.Write(filePath, append(data, '\n'), paths.PrivateFileMode); err != nil && !atomicfile.IsCommitted(err) {
		return fmt.Errorf("publish action process: %w", err)
	}
	return nil
}

// ReadRun reads and validates the current run for the Action.
func ReadRun(profile, domain, actionID string) (RunRecord, string, error) {
	if !validActionID(actionID) {
		return RunRecord{}, "", errors.New("invalid action ID")
	}
	actionDir, err := Directory(profile, domain)
	if err != nil {
		return RunRecord{}, "", err
	}
	if err := verifyActionPath(profile, domain, actionDir); err != nil {
		return RunRecord{}, "", err
	}
	runtimeDir := filepath.Join(actionDir, "runtime")
	if err := verifyRealDirectory(runtimeDir); err != nil {
		return RunRecord{}, "", err
	}
	filePath := runRecordPath(runtimeDir, actionID)
	info, err := os.Lstat(filePath)
	if err != nil {
		return RunRecord{}, "", err
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > 4096 {
		return RunRecord{}, "", errors.New("action runtime record is not a safe regular file")
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return RunRecord{}, "", err
	}
	var record RunRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return RunRecord{}, "", fmt.Errorf("decode action runtime record: %w", err)
	}
	if record.Version != runtimeVersion || !validRunID.MatchString(record.RunID) || record.ActionID != actionID || record.Domain != domain || record.Profile != paths.ProfileSlug(profile) || (record.Source != "pi" && record.Source != "automata-ui") || record.StartedAt.IsZero() || record.PID < 0 {
		return RunRecord{}, "", errors.New("action runtime record has invalid provenance")
	}
	return record, filePath, nil
}

// RequestStop atomically creates a run-specific stop request.
func RequestStop(profile, domain, actionID, runID string) error {
	if !validRunID.MatchString(runID) {
		return errors.New("invalid action run ID")
	}
	record, _, err := ReadRun(profile, domain, actionID)
	if err != nil {
		return err
	}
	if record.RunID != runID {
		return errors.New("action runtime ownership changed")
	}
	actionDir, _ := Directory(profile, domain)
	requestPath := stopRequestPath(filepath.Join(actionDir, "runtime"), actionID, runID)
	file, err := os.OpenFile(requestPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, paths.PrivateFileMode)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil
		}
		return fmt.Errorf("create action stop request: %w", err)
	}
	if _, err := file.WriteString(runID + "\n"); err != nil {
		_ = file.Close()
		_ = os.Remove(requestPath)
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(requestPath)
		return err
	}
	return file.Close()
}

// StopRequested reports whether a run-specific stop request exists and is valid.
func StopRequested(profile, domain, actionID, runID string) (bool, error) {
	if !validRunID.MatchString(runID) {
		return false, errors.New("invalid action run ID")
	}
	actionDir, err := Directory(profile, domain)
	if err != nil {
		return false, err
	}
	runtimeDir := filepath.Join(actionDir, "runtime")
	if err := verifyRealDirectory(runtimeDir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	filePath := stopRequestPath(runtimeDir, actionID, runID)
	info, err := os.Lstat(filePath)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > 128 {
		return false, errors.New("action stop request is not a safe regular file")
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(data)) == runID, nil
}

// FinishRun removes only the record and stop request still owned by runID.
func FinishRun(profile, domain, actionID, runID string) error {
	record, filePath, err := ReadRun(profile, domain, actionID)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if record.RunID != runID {
		return errors.New("action runtime ownership changed")
	}
	if err := os.Remove(filePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	actionDir, _ := Directory(profile, domain)
	requestPath := stopRequestPath(filepath.Join(actionDir, "runtime"), actionID, runID)
	if err := os.Remove(requestPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func runRecordPath(runtimeDir, actionID string) string {
	return filepath.Join(runtimeDir, actionID+".json")
}

func stopRequestPath(runtimeDir, actionID, runID string) string {
	return filepath.Join(runtimeDir, actionID+"-"+runID+".stop")
}

func verifyRealDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("action runtime path %q is not a real directory", path)
	}
	return nil
}

func verifyActionPath(profile, domain, actionDir string) error {
	for _, candidate := range []string{
		paths.BaseDir(), filepath.Join(paths.BaseDir(), "profiles"),
		paths.ProfileDir(profile), paths.DomainsDir(profile),
		paths.DomainDir(profile, domain), actionDir,
	} {
		if err := verifyRealDirectory(candidate); err != nil {
			return err
		}
	}
	return nil
}

// ValidRunID reports whether id is a lowercase random 128-bit identifier.
func ValidRunID(id string) bool { return validRunID.MatchString(id) }
