package paths

import (
	"fmt"
	"strings"
	"unicode"
)

// ValidateSessionID rejects values that cannot be safely used as a single
// session path component. Dots remain valid because canonical IDs encode
// folder ancestry with dotted components.
func ValidateSessionID(sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("session ID is empty")
	}
	if sessionID == "." || strings.Contains(sessionID, "..") {
		return fmt.Errorf("session ID %q contains a traversal component", sessionID)
	}
	for _, r := range sessionID {
		if unicode.IsControl(r) || !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-') {
			return fmt.Errorf("session ID %q contains an unsafe character", sessionID)
		}
	}
	if strings.HasPrefix(sessionID, ".") || strings.HasSuffix(sessionID, ".") {
		return fmt.Errorf("session ID %q has an unsafe dotted boundary", sessionID)
	}
	return nil
}

// ValidateFamiliarSessionID requires a familiar session to be a distinct,
// safely scoped child of its owner session.
func ValidateFamiliarSessionID(ownerSessionID, familiarSessionID string) error {
	if err := ValidateSessionID(ownerSessionID); err != nil {
		return fmt.Errorf("invalid owner session ID: %w", err)
	}
	if err := ValidateSessionID(familiarSessionID); err != nil {
		return fmt.Errorf("invalid familiar session ID: %w", err)
	}
	prefix := ownerSessionID + "__"
	if !strings.HasPrefix(familiarSessionID, prefix) {
		return fmt.Errorf("familiar session ID %q is not owned by %q", familiarSessionID, ownerSessionID)
	}
	if len(familiarSessionID) == len(prefix) {
		return fmt.Errorf("familiar session ID %q has an empty suffix", familiarSessionID)
	}
	return nil
}
