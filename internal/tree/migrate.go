package tree

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

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
		if err := migrateProfileTree(legacyProfileDir, newProfileDir); err != nil {
			return fmt.Errorf("migrate legacy profile %s: %w", name, err)
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
	if complete, err := migrationComplete(defaultProfileDir); err != nil {
		return err
	} else if complete {
		return nil
	}
	if err := os.MkdirAll(defaultProfileDir, 0o755); err != nil {
		return fmt.Errorf("create default profile dir: %w", err)
	}

	legacyState := filepath.Join(legacyDir, "state.json")
	if err := copyIfPresent(legacyState, filepath.Join(defaultProfileDir, "state.json")); err != nil {
		return fmt.Errorf("copy state.json: %w", err)
	}
	for _, name := range []string{"sessions", "domains"} {
		legacySub := filepath.Join(legacyDir, name)
		if _, err := os.Stat(legacySub); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return fmt.Errorf("stat legacy %s: %w", name, err)
		}
		if err := copyDir(legacySub, filepath.Join(defaultProfileDir, name)); err != nil {
			return fmt.Errorf("copy legacy %s: %w", name, err)
		}
	}
	if err := verifyIfPresent(legacyState, filepath.Join(defaultProfileDir, "state.json")); err != nil {
		return fmt.Errorf("verify state.json: %w", err)
	}
	for _, name := range []string{"sessions", "domains"} {
		legacySub := filepath.Join(legacyDir, name)
		if _, err := os.Stat(legacySub); os.IsNotExist(err) {
			continue
		}
		if err := verifyCopiedTree(legacySub, filepath.Join(defaultProfileDir, name)); err != nil {
			return fmt.Errorf("verify legacy %s: %w", name, err)
		}
	}
	return writeMigrationMarker(defaultProfileDir)
}

const migrationMarkerName = ".legacy-migration-complete"

func migrateProfileTree(src, dst string) error {
	if complete, err := migrationComplete(dst); err != nil {
		return err
	} else if complete {
		return nil
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	if err := copyDir(src, dst); err != nil {
		return err
	}
	if err := verifyCopiedTree(src, dst); err != nil {
		return err
	}
	return writeMigrationMarker(dst)
}

func migrationComplete(profileDir string) (bool, error) {
	info, err := os.Stat(filepath.Join(profileDir, migrationMarkerName))
	if err == nil {
		if !info.Mode().IsRegular() {
			return false, fmt.Errorf("migration marker is not a regular file")
		}
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func writeMigrationMarker(profileDir string) error {
	temporary, err := os.CreateTemp(profileDir, ".legacy-migration-*.tmp")
	if err != nil {
		return fmt.Errorf("create migration marker: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("chmod migration marker: %w", err)
	}
	if _, err := temporary.WriteString("version=1\ncompletedAt=" + time.Now().UTC().Format(time.RFC3339Nano) + "\n"); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write migration marker: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync migration marker: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close migration marker: %w", err)
	}
	if err := os.Rename(temporaryPath, filepath.Join(profileDir, migrationMarkerName)); err != nil {
		return fmt.Errorf("commit migration marker: %w", err)
	}
	return nil
}

// verifyCopiedTree confirms every source entry has a compatible destination.
// Existing destination files are authoritative and are never overwritten.
func verifyCopiedTree(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		srcInfo, err := entry.Info()
		if err != nil {
			return err
		}
		dstInfo, err := os.Stat(dstPath)
		if err != nil {
			return fmt.Errorf("verify %s: %w", dstPath, err)
		}
		if srcInfo.IsDir() {
			if !dstInfo.IsDir() {
				return fmt.Errorf("verify %s: destination is not a directory", dstPath)
			}
			if err := verifyCopiedTree(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		if dstInfo.IsDir() {
			return fmt.Errorf("verify %s: destination is a directory", dstPath)
		}
	}
	return nil
}

func copyIfPresent(src, dst string) error {
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err := os.Stat(dst); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return copyFile(src, dst)
}

func verifyIfPresent(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	dstInfo, err := os.Stat(dst)
	if err != nil {
		return err
	}
	if srcInfo.IsDir() != dstInfo.IsDir() {
		return fmt.Errorf("source and destination types differ")
	}
	return nil
}

// copyDir recursively copies src into dst. Existing destination files are
// preserved. New files are copied through a sibling temporary file and an
// atomic rename so an interrupted migration leaves no partial destination.
func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if info, err := os.Stat(dstPath); err == nil && !info.IsDir() {
				return fmt.Errorf("destination %s is not a directory", dstPath)
			} else if err != nil && !os.IsNotExist(err) {
				return err
			}
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		if _, err := os.Stat(dstPath); err == nil {
			continue
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

	temporary, err := os.CreateTemp(filepath.Dir(dst), ".legacy-copy-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := io.Copy(temporary, in); err != nil {
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
	return os.Rename(temporaryPath, dst)
}
