// Package actions reads folder-scoped launch actions shared by Automata and Pi.
package actions

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/HumanHorizon/automata/internal/paths"
)

const directoryName = "actions"

// Action is one user-confirmed command button registered by the Pi Automata extension.
type Action struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Command string `json:"command"`
	CWD     string `json:"cwd"`
}

// Directory returns the folder-domain action directory after validating its path component.
func Directory(profile, domain string) (string, error) {
	if err := validateDomain(domain); err != nil {
		return "", err
	}
	return filepath.Join(paths.DomainDir(profile, domain), directoryName), nil
}

// ReadForProfile reads the actions registered in one canonical profile and folder-domain.
// Missing action directories are an empty list; malformed individual records are reported
// while valid records remain available to the caller.
func ReadForProfile(profile, domain string) ([]Action, error) {
	directory, err := Directory(profile, domain)
	if err != nil {
		return nil, err
	}
	domainDirectory := filepath.Dir(directory)
	for _, candidate := range []string{domainDirectory, directory} {
		info, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("inspect action directory %q: %w", candidate, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
			return nil, fmt.Errorf("action path %q is not a real directory", candidate)
		}
	}
	return readFS(os.DirFS(directory))
}

// readFS loads action records from a filesystem rooted at its actions directory.
func readFS(fsys fs.FS) ([]Action, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read actions directory: %w", err)
	}

	actions := make([]Action, 0, len(entries))
	var failures []error
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !validActionID(id) {
			failures = append(failures, fmt.Errorf("invalid action filename %q", entry.Name()))
			continue
		}
		info, err := entry.Info()
		if err != nil {
			failures = append(failures, fmt.Errorf("inspect action %q: %w", entry.Name(), err))
			continue
		}
		if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
			failures = append(failures, fmt.Errorf("action %q is not a regular file", entry.Name()))
			continue
		}
		data, err := fs.ReadFile(fsys, entry.Name())
		if err != nil {
			failures = append(failures, fmt.Errorf("read action %q: %w", entry.Name(), err))
			continue
		}
		var action Action
		if err := json.Unmarshal(data, &action); err != nil {
			failures = append(failures, fmt.Errorf("decode action %q: %w", entry.Name(), err))
			continue
		}
		if err := validateAction(action, id); err != nil {
			failures = append(failures, fmt.Errorf("validate action %q: %w", entry.Name(), err))
			continue
		}
		actions = append(actions, action)
	}

	sort.Slice(actions, func(i, j int) bool {
		left, right := strings.ToLower(actions[i].Name), strings.ToLower(actions[j].Name)
		if left != right {
			return left < right
		}
		if actions[i].Name != actions[j].Name {
			return actions[i].Name < actions[j].Name
		}
		return actions[i].ID < actions[j].ID
	})
	return actions, errors.Join(failures...)
}

func validateDomain(domain string) error {
	if domain == "" || domain == "." || domain == ".." || filepath.IsAbs(domain) {
		return fmt.Errorf("invalid folder domain %q", domain)
	}
	for _, r := range domain {
		if r == '/' || r == '\\' || unicode.IsControl(r) {
			return fmt.Errorf("invalid folder domain %q", domain)
		}
	}
	return nil
}

func validActionID(id string) bool {
	if len(id) != 64 {
		return false
	}
	for _, r := range id {
		if r < '0' || (r > '9' && r < 'a') || r > 'f' {
			return false
		}
	}
	return true
}

func validateAction(action Action, filenameID string) error {
	if action.ID != filenameID || !validActionID(action.ID) {
		return errors.New("record ID does not match its filename")
	}
	if action.Name == "" || strings.TrimSpace(action.Name) != action.Name {
		return errors.New("name must be non-empty and trimmed")
	}
	for _, r := range action.Name {
		if unicode.IsControl(r) {
			return errors.New("name contains a control character")
		}
	}
	if strings.TrimSpace(action.Command) == "" || strings.ContainsRune(action.Command, '\x00') {
		return errors.New("command must be non-empty and contain no NUL")
	}
	if !filepath.IsAbs(action.CWD) || strings.ContainsRune(action.CWD, '\x00') {
		return errors.New("working directory must be absolute and contain no NUL")
	}
	return nil
}
