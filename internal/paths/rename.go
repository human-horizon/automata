package paths

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RenameDirectory moves one Automata data directory without merging it with
// an existing target. Missing source and target are both treated as a no-op;
// a missing source with an existing target is an error so stale data cannot be
// silently adopted by a renamed item.
func RenameDirectory(oldPath, newPath string) error {
	if oldPath == newPath {
		return nil
	}

	oldInfo, oldErr := os.Stat(oldPath)
	newInfo, newErr := os.Stat(newPath)
	switch {
	case oldErr == nil && !oldInfo.IsDir():
		return fmt.Errorf("source %q is not a directory", oldPath)
	case oldErr != nil && !os.IsNotExist(oldErr):
		return fmt.Errorf("stat source %q: %w", oldPath, oldErr)
	case newErr == nil && !newInfo.IsDir():
		return fmt.Errorf("target %q exists and is not a directory", newPath)
	case newErr != nil && !os.IsNotExist(newErr):
		return fmt.Errorf("stat target %q: %w", newPath, newErr)
	}

	if oldErr != nil {
		if newErr == nil {
			return fmt.Errorf("source %q is missing while target exists", oldPath)
		}
		return nil
	}
	if newErr == nil {
		return fmt.Errorf("target %q already exists", newPath)
	}

	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		return fmt.Errorf("create target parent %q: %w", filepath.Dir(newPath), err)
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		return fmt.Errorf("rename %q to %q: %w", oldPath, newPath, err)
	}
	return nil
}

// MigrateSessionJSONL updates only the session id in the first JSONL header
// line. Message history and all following lines are preserved byte-for-byte.
// The search is restricted to agentDir so one pi agent cannot modify another
// agent's history.
func MigrateSessionJSONL(oldID, newID, cwd, agentDir string) (string, error) {
	if oldID == "" || newID == "" || oldID == newID {
		return "", nil
	}
	oldPath := FindSessionJSONL(oldID, cwd, agentDir)
	newPath := FindSessionJSONL(newID, cwd, agentDir)
	if newPath != "" && newPath != oldPath {
		return "", fmt.Errorf("target session JSONL already exists for %q", newID)
	}
	if oldPath == "" {
		return "", nil
	}

	data, err := os.ReadFile(oldPath)
	if err != nil {
		return "", fmt.Errorf("read session JSONL %q: %w", oldPath, err)
	}
	lineEnd := bytes.IndexByte(data, '\n')
	firstLine := data
	rest := []byte{}
	if lineEnd >= 0 {
		firstLine = data[:lineEnd]
		rest = data[lineEnd:]
	}

	var header map[string]json.RawMessage
	if err := json.Unmarshal(firstLine, &header); err != nil {
		return "", fmt.Errorf("decode session JSONL header %q: %w", oldPath, err)
	}
	var headerID string
	if rawID, ok := header["id"]; ok {
		if err := json.Unmarshal(rawID, &headerID); err != nil {
			return "", fmt.Errorf("decode session id in %q: %w", oldPath, err)
		}
	}
	if headerID != oldID {
		return "", fmt.Errorf("session JSONL %q has id %q, expected %q", oldPath, headerID, oldID)
	}
	newIDJSON, err := json.Marshal(newID)
	if err != nil {
		return "", fmt.Errorf("encode session id %q: %w", newID, err)
	}
	header["id"] = newIDJSON
	newHeader, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("encode session JSONL header %q: %w", oldPath, err)
	}
	newData := append(newHeader, rest...)

	info, err := os.Stat(oldPath)
	if err != nil {
		return "", fmt.Errorf("stat session JSONL %q: %w", oldPath, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(oldPath), ".automata-session-*.jsonl")
	if err != nil {
		return "", fmt.Errorf("create temporary session JSONL: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("preserve session JSONL mode: %w", err)
	}
	if _, err := tmp.Write(newData); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write temporary session JSONL: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("sync temporary session JSONL: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close temporary session JSONL: %w", err)
	}
	if err := os.Rename(tmpPath, oldPath); err != nil {
		return "", fmt.Errorf("replace session JSONL %q: %w", oldPath, err)
	}
	return oldPath, nil
}

// ReadFamiliars returns the familiar records owned by a session. A missing
// familiars.json is equivalent to an empty list.
func ReadFamiliars(profile, ownerID string) ([]FamiliarEntry, error) {
	data, err := os.ReadFile(FamiliarsJSONLPath(profile, ownerID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var entries []FamiliarEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("decode familiars.json: %w", err)
	}
	return entries, nil
}

// RewriteFamiliarSessionIDs updates familiar owner-prefixed IDs in place and
// returns the old-to-new ID mapping. Unknown JSON fields are preserved.
func RewriteFamiliarSessionIDs(profile, oldOwnerID, newOwnerID string) (map[string]string, error) {
	mapping := make(map[string]string)
	if oldOwnerID == "" || newOwnerID == "" || oldOwnerID == newOwnerID {
		return mapping, nil
	}
	path := FamiliarsJSONLPath(profile, newOwnerID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return mapping, nil
		}
		return nil, err
	}

	var records []map[string]json.RawMessage
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("decode familiars.json: %w", err)
	}
	prefix := oldOwnerID + "__"
	for _, record := range records {
		rawID, ok := record["sessionId"]
		if !ok {
			continue
		}
		var oldID string
		if err := json.Unmarshal(rawID, &oldID); err != nil {
			return nil, fmt.Errorf("decode familiar session id: %w", err)
		}
		if !strings.HasPrefix(oldID, prefix) {
			continue
		}
		newID := newOwnerID + oldID[len(oldOwnerID):]
		newRawID, err := json.Marshal(newID)
		if err != nil {
			return nil, fmt.Errorf("encode familiar session id: %w", err)
		}
		record["sessionId"] = newRawID
		mapping[oldID] = newID
	}
	if len(mapping) == 0 {
		return mapping, nil
	}

	updated, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode familiars.json: %w", err)
	}
	if err := os.WriteFile(path, updated, 0o644); err != nil {
		return nil, fmt.Errorf("write familiars.json: %w", err)
	}
	return mapping, nil
}
