package paths

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// LegacySessionProfileMode selects how duplicate filesystem matches are handled.
type LegacySessionProfileMode uint8

const (
	LegacySessionProfileReadOnly LegacySessionProfileMode = iota
	LegacySessionProfileDestructive
)

// ErrAmbiguousLegacySessionProfile indicates that a session directory exists
// under more than one candidate profile and a destructive legacy operation
// cannot safely choose one.
var ErrAmbiguousLegacySessionProfile = errors.New("ambiguous legacy session profile")

// ResolveLegacySessionProfile finds the profile for a legacy API that receives
// only a session ID. Existing directories take precedence over fallback. When
// multiple directories exist, read-only callers use candidate order while
// destructive callers fail closed.
func ResolveLegacySessionProfile(sessionID string, mode LegacySessionProfileMode) (string, error) {
	if mode != LegacySessionProfileReadOnly && mode != LegacySessionProfileDestructive {
		return "", fmt.Errorf("invalid legacy session profile mode %d", mode)
	}

	candidates := legacySessionProfileCandidates(sessionID)
	if sessionID == "" {
		return candidates[0], nil
	}

	matches := make([]string, 0, len(candidates))
	for _, profile := range candidates {
		info, err := os.Stat(SessionDir(profile, sessionID))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", fmt.Errorf("inspect session directory for profile %q: %w", profile, err)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("session path for profile %q is not a directory", profile)
		}
		matches = append(matches, profile)
	}

	switch len(matches) {
	case 0:
		return candidates[0], nil
	case 1:
		return matches[0], nil
	default:
		if mode == LegacySessionProfileDestructive {
			return "", fmt.Errorf("%w: session %q exists in profiles %s", ErrAmbiguousLegacySessionProfile, sessionID, strings.Join(matches, ", "))
		}
		return matches[0], nil
	}
}

func legacySessionProfileCandidates(sessionID string) []string {
	candidates := make([]string, 0, 3)
	if index := strings.Index(sessionID, "__"); index > 0 {
		candidates = append(candidates, ProfileSlug(sessionID[:index]))
	}
	candidates = append(candidates, ProfileSlug(os.Getenv("AI_PROFILE")), ProfileSlug(""))

	unique := candidates[:0]
	seen := make(map[string]struct{}, len(candidates))
	for _, profile := range candidates {
		if _, exists := seen[profile]; exists {
			continue
		}
		seen[profile] = struct{}{}
		unique = append(unique, profile)
	}
	return unique
}
