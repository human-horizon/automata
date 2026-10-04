package main

import (
	"errors"
	"fmt"

	"github.com/HumanHorizon/automata/internal/ai-knowledge/human"
	"github.com/HumanHorizon/automata/internal/paths"
)

// clearHumanProjections runs only after history cleanup and runtime shutdown
// succeed, so a still-running exporter cannot recreate a cleared projection.
func clearHumanProjections(profile, ownerID string, sessionIDs []string) error {
	if err := paths.ValidateSessionID(ownerID); err != nil {
		return fmt.Errorf("clear Human owner: %w", err)
	}
	ids := []string{ownerID}
	seen := map[string]struct{}{ownerID: {}}
	for _, sessionID := range sessionIDs {
		if _, exists := seen[sessionID]; exists {
			continue
		}
		if err := paths.ValidateFamiliarSessionID(ownerID, sessionID); err != nil {
			return fmt.Errorf("clear Human familiar: %w", err)
		}
		seen[sessionID] = struct{}{}
		ids = append(ids, sessionID)
	}
	var failures []error
	for _, sessionID := range ids {
		if err := human.ClearForProfile(paths.ProfileSlug(profile), sessionID); err != nil {
			failures = append(failures, fmt.Errorf("clear Human %q: %w", sessionID, err))
		}
	}
	return errors.Join(failures...)
}
