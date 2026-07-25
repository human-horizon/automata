// Package paths provides a single source of truth for the on-disk layout
// shared by Automata and ai-knowledge.
//
// Base layout:
//
//	~/.ai/automata/
// 	└── profiles/
// 	    └── <profile-slug>/
// 	        ├── state.json
// 	        ├── sessions/<session-id>/
// 	        │   ├── settings.json
// 	        │   ├── tasks.json
// 	        │   ├── plans.json
// 	        │   └── status.json
// 	        └── domains/<domain>/
// 	            └── notes.json
package paths

import (
	"os"
	"path/filepath"

	"github.com/HumanHorizon/automata/internal/slug"
)

// dataHome returns the base data directory. AI_DATA_HOME overrides the default
// ~/.ai/automata location for tests and alternative environments.
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

// BaseDir returns ~/.ai/automata (or $AI_DATA_HOME).
func BaseDir() string {
	return dataHome()
}

// profileSlug normalizes a profile name for use in paths. Empty profile maps
// to "default".
func profileSlug(profile string) string {
	if profile == "" {
		return "default"
	}
	return slug.Slug(profile)
}

// ProfileSlug normalizes a profile name for use in paths. Empty profile maps
// to "default". Exported so other packages can keep the same convention.
func ProfileSlug(profile string) string {
	return profileSlug(profile)
}

// ProfileDir returns the profile directory: ~/.ai/automata/profiles/<slug>.
func ProfileDir(profile string) string {
	return filepath.Join(BaseDir(), "profiles", profileSlug(profile))
}

// StatePath returns the Automata tree state file path.
func StatePath(profile string) string {
	return filepath.Join(ProfileDir(profile), "state.json")
}

// SessionsDir returns the sessions directory for a profile.
func SessionsDir(profile string) string {
	return filepath.Join(ProfileDir(profile), "sessions")
}

// SessionDir returns the directory for a specific session.
func SessionDir(profile, sessionID string) string {
	return filepath.Join(SessionsDir(profile), sessionID)
}

// DomainsDir returns the domains directory for a profile.
func DomainsDir(profile string) string {
	return filepath.Join(ProfileDir(profile), "domains")
}

// DomainDir returns the directory for a specific domain.
func DomainDir(profile, domain string) string {
	return filepath.Join(DomainsDir(profile), domain)
}

// EnsureProfileDir creates the profile directory tree if it does not exist.
func EnsureProfileDir(profile string) error {
	return os.MkdirAll(ProfileDir(profile), 0o755)
}

// EnsureSessionDir creates the session directory tree if it does not exist.
func EnsureSessionDir(profile, sessionID string) error {
	return os.MkdirAll(SessionDir(profile, sessionID), 0o755)
}

// EnsureDomainDir creates the domain directory tree if it does not exist.
func EnsureDomainDir(profile, domain string) error {
	return os.MkdirAll(DomainDir(profile, domain), 0o755)
}
