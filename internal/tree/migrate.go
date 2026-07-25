package tree

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/HumanHorizon/automata/internal/paths"
	"github.com/HumanHorizon/automata/internal/slug"
)

// migrateLegacyData copies data from the old ~/.automata layout into the new
// ~/.ai/automata/profiles/<profile> layout. It runs once per legacy source:
// - ~/.automata/state.json / sessions / domains -> profiles/default/
// - ~/.automata/<profile>/ -> profiles/<profile>/
func migrateLegacyData(profile string) error {
	if err := migrateDefaultProfile(); err != nil {
		return fmt.Errorf("migrate default profile: %w", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	legacyDir := filepath.Join(home, ".automata")
	if _, err := os.Stat(legacyDir); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("stat legacy dir: %w", err)
	}

	entries, err := os.ReadDir(legacyDir)
	if err != nil {
		return fmt.Errorf("read legacy dir: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "sessions" || name == "domains" {
			// These belong to the default profile and are handled by migrateDefaultProfile.
			continue
		}
		legacyProfileDir := filepath.Join(legacyDir, name)
		// A profile directory must contain a state.json file.
		if _, err := os.Stat(filepath.Join(legacyProfileDir, "state.json")); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return fmt.Errorf("stat legacy state for %s: %w", name, err)
		}
		profileSlug := slug.Slug(name)
		newProfileDir := paths.ProfileDir(profileSlug)
		if _, err := os.Stat(newProfileDir); err == nil {
			continue // already migrated
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat new profile dir for %s: %w", name, err)
		}
		if err := os.MkdirAll(newProfileDir, 0o755); err != nil {
			return fmt.Errorf("create profile dir %s: %w", profileSlug, err)
		}
		if err := copyDir(legacyProfileDir, newProfileDir); err != nil {
			return fmt.Errorf("copy legacy profile %s: %w", name, err)
		}
	}
	return nil
}

// migrateDefaultProfile copies ~/.automata/state.json, sessions/, and domains/
// into ~/.ai/automata/profiles/default/.
func migrateDefaultProfile() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	legacyDir := filepath.Join(home, ".automata")
	if _, err := os.Stat(legacyDir); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("stat legacy dir: %w", err)
	}

	defaultProfileDir := paths.ProfileDir("")
	if _, err := os.Stat(defaultProfileDir); err == nil {
		return nil // default profile already exists
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat default profile dir: %w", err)
	}
	if err := os.MkdirAll(defaultProfileDir, 0o755); err != nil {
		return fmt.Errorf("create default profile dir: %w", err)
	}

	// Copy state.json if it exists.
	legacyState := filepath.Join(legacyDir, "state.json")
	if _, err := os.Stat(legacyState); err == nil {
		if err := copyFile(legacyState, filepath.Join(defaultProfileDir, "state.json")); err != nil {
			return fmt.Errorf("copy state.json: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat legacy state.json: %w", err)
	}

	// Copy sessions/ and domains/ if they exist.
	for _, name := range []string{"sessions", "domains"} {
		legacySub := filepath.Join(legacyDir, name)
		if _, err := os.Stat(legacySub); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return fmt.Errorf("stat legacy %s: %w", name, err)
		}
		newSub := filepath.Join(defaultProfileDir, name)
		if err := copyDir(legacySub, newSub); err != nil {
			return fmt.Errorf("copy legacy %s: %w", name, err)
		}
	}
	return nil
}

// copyDir recursively copies src into dst. Existing files in dst are not
// overwritten.
func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := os.MkdirAll(dstPath, 0o755); err != nil {
				return err
			}
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		if _, err := os.Stat(dstPath); err == nil {
			continue // do not overwrite existing files
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := copyFile(srcPath, dstPath); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
