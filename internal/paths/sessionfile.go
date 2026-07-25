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

// SessionRoots returns the directory where just-pi keeps session .jsonl files.
func SessionRoots() []string {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "/Users/a"
	}
	return []string{filepath.Join(home, ".ai", "just", "pi", "sessions")}
}

// EncodeCwdDir returns the subdirectory name used by pi for a given working directory.
// Format: leading and trailing dashes around the path with /, \\, : replaced by -.
func EncodeCwdDir(cwd string) string {
	cleaned := strings.TrimLeft(cwd, "/\\")
	return "--" + strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(cleaned) + "--"
}

// FindSessionJSONL searches for the .jsonl file for a given session id and working directory.
// It prefers the current working directory, then falls back to all session directories.
func FindSessionJSONL(sessionID, cwd string) string {
	if sessionID == "" {
		return ""
	}

	subdir := EncodeCwdDir(cwd)
	preferred := sessionFileCandidate{}
	for _, root := range SessionRoots() {
		candidate, ok := newestSessionFile(filepath.Join(root, subdir), sessionID)
		if ok && candidate.newerThan(preferred) {
			preferred = candidate
		}
	}
	if preferred.path != "" {
		return preferred.path
	}

	fallback := sessionFileCandidate{}
	for _, root := range SessionRoots() {
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

// DeleteSessionJSONL removes the .jsonl file for a given session and cwd.
// Returns the deleted path and nil on success, or empty string and error.
func DeleteSessionJSONL(sessionID, cwd string) (string, error) {
	p := FindSessionJSONL(sessionID, cwd)
	if p == "" {
		return "", fmt.Errorf("session file not found for %q", sessionID)
	}
	if err := os.Remove(p); err != nil {
		return "", err
	}
	return p, nil
}

// FamiliarsJSONLPath returns the absolute path to familiars.json for the given
// session. Profile-aware: when profile is non-empty the file lives under
// ~/.ai/automata/profiles/<profile>/sessions/<sessionID>/familiars.json.
func FamiliarsJSONLPath(profile, sessionID string) string {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "/Users/a"
	}
	base := filepath.Join(home, ".ai", "automata")
	if profile != "" {
		base = filepath.Join(base, "profiles", profile)
	}
	return filepath.Join(base, "sessions", sessionID, "familiars.json")
}

// ClearFamiliarsJSONL writes an empty list ("[]") to the session's
// familiars.json file. Returns nil if the file does not exist or is
// successfully written. The empty list is meaningful: checkFamiliars in
// ChatPanel distinguishes "no file" from "explicit empty list" and uses
// the latter as a clean state.
func ClearFamiliarsJSONL(profile, sessionID string) error {
	path := FamiliarsJSONLPath(profile, sessionID)
	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}
	return os.WriteFile(path, []byte("[]"), 0644)
}
