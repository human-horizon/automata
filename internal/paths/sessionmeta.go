package paths

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/HumanHorizon/automata/internal/atomicfile"
)

// CurrentSessionSchemaVersion is the latest session directory schema accepted by Automata.
const CurrentSessionSchemaVersion = 1

// SessionManifest records the version of Automata-owned session metadata.
type SessionManifest struct {
	SchemaVersion int `json:"schemaVersion"`
}

func ensureSessionDir(profile, sessionID string) error {
	dir := SessionDir(profile, sessionID)
	if err := EnsurePrivateDir(filepath.Dir(dir)); err != nil {
		return fmt.Errorf("create session parent: %w", err)
	}
	if err := os.Mkdir(dir, PrivateDirMode); err != nil {
		if !os.IsExist(err) {
			return fmt.Errorf("create session directory %s: %w", dir, err)
		}
		if err := EnsurePrivateDir(dir); err != nil {
			return err
		}
		if _, err := readSessionSchemaVersion(dir); err != nil {
			return err
		}
		return nil
	}

	manifest := SessionManifest{SchemaVersion: CurrentSessionSchemaVersion}
	data, err := json.Marshal(manifest)
	if err != nil {
		return rollbackNewSessionDir(dir, fmt.Errorf("encode session manifest: %w", err))
	}
	if err := atomicfile.Write(filepath.Join(dir, "session.json"), append(data, '\n'), PrivateFileMode); err != nil {
		if atomicfile.IsCommitted(err) {
			return fmt.Errorf("write session manifest: %w", err)
		}
		return rollbackNewSessionDir(dir, fmt.Errorf("write session manifest: %w", err))
	}
	return nil
}

func rollbackNewSessionDir(dir string, cause error) error {
	if err := os.Remove(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.Join(cause, fmt.Errorf("remove incomplete session directory %s: %w", dir, err))
	}
	return cause
}

// ReadSessionSchemaVersion returns 0 for a legacy session without a manifest.
// It never creates or migrates session data.
func ReadSessionSchemaVersion(profile, sessionID string) (int, error) {
	if err := ValidateSessionID(sessionID); err != nil {
		return 0, err
	}
	dir := SessionDir(profile, sessionID)
	info, err := os.Stat(dir)
	if err != nil {
		return 0, fmt.Errorf("stat session directory %s: %w", dir, err)
	}
	if !info.IsDir() {
		return 0, fmt.Errorf("session path %s is not a directory", dir)
	}
	return readSessionSchemaVersion(dir)
}

func readSessionSchemaVersion(sessionDir string) (int, error) {
	path := filepath.Join(sessionDir, "session.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("read session manifest %s: %w", path, err)
	}
	var manifest SessionManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return 0, fmt.Errorf("decode session manifest %s: %w", path, err)
	}
	if manifest.SchemaVersion != CurrentSessionSchemaVersion {
		return 0, fmt.Errorf("unsupported session schema version %d in %s (current %d)", manifest.SchemaVersion, path, CurrentSessionSchemaVersion)
	}
	return manifest.SchemaVersion, nil
}
