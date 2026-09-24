package paths

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SessionRoots returns every ~/.ai/<agent>/pi/sessions/ directory that
// currently exists on this machine. Each just-pi-style agent (just, getic,
// synth, vexa, weft, ask, …) keeps its sessions under its own pi/ subdir,
// so a new agent works without code changes. Falls back to
// ~/.ai/just/pi/sessions/ when ~/.ai itself is missing or empty.
func SessionRoots() []string {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "/Users/a"
	}
	base := filepath.Join(home, ".ai")
	entries, err := os.ReadDir(base)
	if err != nil {
		return []string{filepath.Join(base, "just", "pi", "sessions")}
	}
	var roots []string
	seen := make(map[string]bool)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sessionsDir := filepath.Join(base, e.Name(), "pi", "sessions")
		if info, err := os.Stat(sessionsDir); err == nil && info.IsDir() {
			if !seen[sessionsDir] {
				roots = append(roots, sessionsDir)
				seen[sessionsDir] = true
			}
		}
	}
	if len(roots) == 0 {
		return []string{filepath.Join(base, "just", "pi", "sessions")}
	}
	return roots
}

// EncodeCwdDir returns the subdirectory name used by pi for a given working directory.
// Format: leading and trailing dashes around the path with /, \\, : replaced by -.
func EncodeCwdDir(cwd string) string {
	cleaned := strings.TrimLeft(cwd, "/\\")
	return "--" + strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(cleaned) + "--"
}

// FindSessionJSONL searches for the .jsonl file for a given session id, working
// directory and pi agent. When agentDir is non-empty the search is restricted
// to that single agent's sessions dir — this is the safe path used by Clear so
// that one profile cannot pick up another profile's sessions. When agentDir is
// empty the search falls back to all known roots (discovered by SessionRoots),
// which is the legacy behaviour used by tools and tests.
func FindSessionJSONL(sessionID, cwd, agentDir string) string {
	if ValidateSessionID(sessionID) != nil {
		return ""
	}

	roots := sessionRootsFor(agentDir)
	subdir := EncodeCwdDir(cwd)
	preferred := sessionFileCandidate{}
	for _, root := range roots {
		candidate, ok := newestSessionFile(filepath.Join(root, subdir), sessionID)
		if ok && candidate.newerThan(preferred) {
			preferred = candidate
		}
	}
	if preferred.path != "" {
		return preferred.path
	}

	fallback := sessionFileCandidate{}
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() || entry.Name() == subdir {
				continue
			}
			candidate, ok := newestSessionFile(filepath.Join(root, entry.Name()), sessionID)
			if ok && candidate.newerThan(fallback) {
				fallback = candidate
			}
		}
	}
	return fallback.path
}

// sessionRootsFor returns the search roots for FindSessionJSONL. When agentDir
// is non-empty, only that agent's sessions dir is returned; otherwise all
// known roots are used.
func sessionRootsFor(agentDir string) []string {
	if agentDir == "" {
		return SessionRoots()
	}
	return []string{filepath.Join(agentDir, "sessions")}
}

type sessionFileCandidate struct {
	path    string
	modTime time.Time
}

func (candidate sessionFileCandidate) newerThan(other sessionFileCandidate) bool {
	return other.path == "" || candidate.modTime.After(other.modTime)
}

func newestSessionFile(dir, sessionID string) (sessionFileCandidate, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return sessionFileCandidate{}, false
	}

	newest := sessionFileCandidate{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		if readSessionID(path) != sessionID {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		candidate := sessionFileCandidate{path: path, modTime: info.ModTime()}
		if candidate.newerThan(newest) {
			newest = candidate
		}
	}
	return newest, newest.path != ""
}

func readSessionID(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}

	scanner := bufio.NewScanner(file)
	scanned := scanner.Scan()
	closeErr := file.Close()
	if !scanned || closeErr != nil {
		return ""
	}

	var header struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(scanner.Bytes(), &header); err != nil {
		return ""
	}
	return header.ID
}

// DeleteSessionJSONL removes the .jsonl file for a given session and cwd,
// restricted to the given pi agent. Passing an empty agentDir is an error:
// Clear must always know which agent it's clearing, otherwise it could
// delete another profile's session by accident.
func DeleteSessionJSONL(sessionID, cwd, agentDir string) (string, error) {
	if err := ValidateSessionID(sessionID); err != nil {
		return "", err
	}
	if agentDir == "" {
		return "", fmt.Errorf("agentDir is required for safe deletion")
	}
	p := FindSessionJSONL(sessionID, cwd, agentDir)
	if p == "" {
		return "", fmt.Errorf("session file not found for %q in agent %q", sessionID, agentDir)
	}
	if err := os.Remove(p); err != nil {
		return "", err
	}
	return p, nil
}

// FamiliarsJSONLPath returns the absolute path to familiars.json for the given
// session under the canonical profile-scoped sessions directory.
//
// Profile names are case-preserved in memory (e.g. "HumanHorizon") but the
// on-disk layout uses the normalized slug ("humanhorizon"). Empty profiles use
// the canonical "default" profile, just like SessionDir and StatePath.
func FamiliarsJSONLPath(profile, sessionID string) string {
	return filepath.Join(SessionDir(profile, sessionID), "familiars.json")
}

// ClearFamiliarsJSONL writes an empty list ("[]") to the session's
// familiars.json file. Returns nil if the file does not exist or is
// successfully written. The empty list is meaningful: checkFamiliars in
// ChatPanel distinguishes "no file" from "explicit empty list" and uses
// the latter as a clean state.
func ClearFamiliarsJSONL(profile, sessionID string) error {
	if err := ValidateSessionID(sessionID); err != nil {
		return err
	}
	path := FamiliarsJSONLPath(profile, sessionID)
	if _, err := os.Stat(filepath.Dir(path)); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	return writeFileAtomic(path, []byte("[]\n"), 0o644)
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".automata-atomic-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

// FamiliarEntry mirrors the known fields in familiars.json. Cleanup and
// rename operations use raw JSON records so unknown metadata survives writes.
type FamiliarEntry struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Created   string `json:"created,omitempty"`
}

// RemoveFamiliar deletes the entry whose SessionID equals familiarID from
// the session's familiars.json, then rewrites the file with the remaining
// entries. A missing file or a missing entry is a no-op (returns nil).
// Used by ChatPanel when the user closes a familiar via the × button.
func RemoveFamiliar(profile, sessionID, familiarID string) error {
	if err := ValidateFamiliarSessionID(sessionID, familiarID); err != nil {
		return err
	}
	path := FamiliarsJSONLPath(profile, sessionID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var records []map[string]json.RawMessage
	if err := json.Unmarshal(data, &records); err != nil {
		// File exists but isn't a JSON array — leave it alone rather
		// than silently trampling it. Surface the error so the caller
		// can log it.
		return fmt.Errorf("familiars.json at %s is not a JSON array: %w", path, err)
	}
	kept := make([]map[string]json.RawMessage, 0, len(records))
	for _, record := range records {
		var sessionID string
		if raw, ok := record["sessionId"]; ok {
			if err := json.Unmarshal(raw, &sessionID); err != nil {
				return fmt.Errorf("decode familiar session id in %s: %w", path, err)
			}
		}
		if sessionID == familiarID {
			continue
		}
		kept = append(kept, record)
	}
	if len(kept) == len(records) {
		// Nothing to remove — leave the file untouched.
		return nil
	}
	out, err := json.MarshalIndent(kept, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(out, '\n'), 0o644)
}
