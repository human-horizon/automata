package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/HumanHorizon/automata/internal/atomicfile"
	"github.com/HumanHorizon/automata/internal/paths"
)

// Snapshot captures the exact on-disk notes bytes and their decoded model.
type Snapshot struct {
	Profile     string
	Domain      string
	Notes       []NoteSummary
	Raw         []byte
	Exists      bool
	Fingerprint string
}

var notesLocks sync.Map

func notesLock(path string) *sync.Mutex {
	lock, _ := notesLocks.LoadOrStore(path, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

// Fingerprint returns a content hash that distinguishes a missing notes file
// from an existing empty file.
func Fingerprint(raw []byte, exists bool) string {
	if !exists {
		return "missing"
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Capture reads one project's exact notes file and decodes that same snapshot.
func Capture(profile, domain string) (Snapshot, error) {
	if err := validateNotesDomain(domain); err != nil {
		return Snapshot{}, err
	}
	path := notesPath(profile, domain)
	lock := notesLock(path)
	lock.Lock()
	defer lock.Unlock()
	return capturePath(profile, domain, path)
}

func capturePath(profile, domain, path string) (Snapshot, error) {
	raw, exists, err := readNotesBytes(path)
	if err != nil {
		return Snapshot{}, err
	}
	notes := make([]NoteSummary, 0)
	if exists {
		if err := json.Unmarshal(raw, &notes); err != nil {
			return Snapshot{}, fmt.Errorf("decode notes file %s: %w", path, err)
		}
	}
	return Snapshot{
		Profile: profile, Domain: domain, Notes: normalizeNotes(notes), Raw: raw,
		Exists: exists, Fingerprint: Fingerprint(raw, exists),
	}, nil
}

// WriteIfUnchanged calls prepare while holding the same per-domain lock used by
// Write, then atomically replaces notes only if the original snapshot remains.
// rollback removes prepared auxiliary data if the notes replacement did not commit.
func WriteIfUnchanged(
	snapshot Snapshot,
	notes []NoteSummary,
	prepare func(raw []byte, exists bool, resultFingerprint string) (rollback func() error, err error),
) (committed bool, err error) {
	if err := validateNotesDomain(snapshot.Domain); err != nil {
		return false, err
	}
	path := notesPath(snapshot.Profile, snapshot.Domain)
	lock := notesLock(path)
	lock.Lock()
	defer lock.Unlock()

	currentRaw, currentExists, err := readNotesBytes(path)
	if err != nil {
		return false, err
	}
	if Fingerprint(currentRaw, currentExists) != snapshot.Fingerprint {
		return false, fmt.Errorf("notes changed during actualization")
	}

	if notes == nil {
		notes = make([]NoteSummary, 0)
	}
	encoded, err := json.MarshalIndent(notes, "", "  ")
	if err != nil {
		return false, fmt.Errorf("marshal notes: %w", err)
	}
	encoded = append(encoded, '\n')
	if prepare == nil {
		return false, fmt.Errorf("notes write preparation callback is required")
	}
	rollback, err := prepare(append([]byte(nil), currentRaw...), currentExists, Fingerprint(encoded, true))
	if err != nil {
		if rollback != nil {
			err = errors.Join(err, rollback())
		}
		return false, err
	}
	writeErr := atomicfile.Write(path, encoded, paths.PrivateFileMode)
	if writeErr == nil || atomicfile.IsCommitted(writeErr) {
		return true, writeErr
	}
	if rollback != nil {
		writeErr = errors.Join(writeErr, rollback())
	}
	return false, fmt.Errorf("replace notes file: %w", writeErr)
}

// RestoreIfCurrent restores an exact prior snapshot only if current notes still
// have the expected fingerprint. Normal in-process memory.Write calls share the
// per-domain lock, so they cannot race the comparison and replacement.
func RestoreIfCurrent(profile, domain, expectedFingerprint string, original Snapshot) (bool, error) {
	if err := validateNotesDomain(domain); err != nil {
		return false, err
	}
	if original.Profile != profile || original.Domain != domain || Fingerprint(original.Raw, original.Exists) != original.Fingerprint {
		return false, fmt.Errorf("invalid original notes snapshot")
	}
	path := notesPath(profile, domain)
	lock := notesLock(path)
	lock.Lock()
	defer lock.Unlock()

	currentRaw, currentExists, err := readNotesBytes(path)
	if err != nil {
		return false, err
	}
	if Fingerprint(currentRaw, currentExists) != expectedFingerprint {
		return false, fmt.Errorf("notes changed since actualization; undo refused")
	}
	if original.Exists {
		if err := atomicfile.Write(path, original.Raw, paths.PrivateFileMode); err != nil {
			return atomicfile.IsCommitted(err), fmt.Errorf("restore notes: %w", err)
		}
		return true, nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("remove actualized notes: %w", err)
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return true, fmt.Errorf("notes removed but parent directory sync failed: %w", err)
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil || closeErr != nil {
		return true, fmt.Errorf("notes removed but parent directory sync failed: %w", errors.Join(syncErr, closeErr))
	}
	return true, nil
}

func readNotesBytes(path string) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("inspect notes file %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("notes path %s is not a regular file", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false, fmt.Errorf("read notes file %s: %w", path, err)
	}
	return raw, true, nil
}

func validateNotesDomain(domain string) error {
	if domain == "" || domain == "." || domain == ".." || filepath.Base(domain) != domain || filepath.IsAbs(domain) {
		return fmt.Errorf("invalid notes domain %q", domain)
	}
	return nil
}

func notesPath(profile, domain string) string {
	return filepath.Join(paths.DomainDir(effectiveProfile(profile), domain), "notes.json")
}
