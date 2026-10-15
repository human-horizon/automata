package notesactualize

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/HumanHorizon/automata/internal/ai-knowledge/memory"
	"github.com/HumanHorizon/automata/internal/atomicfile"
	"github.com/HumanHorizon/automata/internal/paths"
)

const (
	backupDirectoryName = ".notes-backups"
	backupPrefix        = "actualize-"
	backupRetention     = 5
)

// BackupStore stores exact pre-actualization bytes privately inside each domain.
type BackupStore struct{}

type backupManifest struct {
	OriginalExists      bool   `json:"originalExists"`
	OriginalFingerprint string `json:"originalFingerprint"`
	ResultFingerprint   string `json:"resultFingerprint"`
}

// NewBackupStore creates a store rooted at the current Automata data home.
func NewBackupStore() *BackupStore { return &BackupStore{} }

func (s *BackupStore) prepare(original memory.Snapshot, resultFingerprint string) (string, func() error, error) {
	root := s.root(original.Profile, original.Domain)
	if err := paths.EnsurePrivateDir(root); err != nil {
		return "", nil, fmt.Errorf("create notes backup directory: %w", err)
	}
	id, err := newBackupID()
	if err != nil {
		return "", nil, fmt.Errorf("create backup ID: %w", err)
	}
	dir := filepath.Join(root, id)
	if err := os.Mkdir(dir, paths.PrivateDirMode); err != nil {
		return "", nil, fmt.Errorf("create notes backup: %w", err)
	}
	cleanup := func() error { return os.RemoveAll(dir) }
	if original.Exists {
		if err := atomicfile.Write(filepath.Join(dir, "original.json"), original.Raw, paths.PrivateFileMode); err != nil {
			return "", cleanup, fmt.Errorf("write original notes backup: %w", err)
		}
	}
	manifest := backupManifest{
		OriginalExists:      original.Exists,
		OriginalFingerprint: original.Fingerprint,
		ResultFingerprint:   resultFingerprint,
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return "", cleanup, fmt.Errorf("encode backup metadata: %w", err)
	}
	if err := atomicfile.Write(filepath.Join(dir, "manifest.json"), append(encoded, '\n'), paths.PrivateFileMode); err != nil {
		return "", cleanup, fmt.Errorf("write backup metadata: %w", err)
	}
	if err := syncDirectory(root); err != nil {
		return "", cleanup, fmt.Errorf("sync notes backup directory: %w", err)
	}
	return id, cleanup, nil
}

// UndoLast restores the newest backup only while current notes still match the
// corresponding actualized result. It never overwrites subsequent user edits.
func (s *BackupStore) UndoLast(profile, domain string) (string, error) {
	entries, err := s.backupIDs(profile, domain)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", errors.New("no notes actualization backup is available")
	}
	id := entries[0]
	dir := filepath.Join(s.root(profile, domain), id)
	manifest, err := readManifest(dir)
	if err != nil {
		return "", err
	}
	var raw []byte
	if manifest.OriginalExists {
		path := filepath.Join(dir, "original.json")
		if err := requireRegularPrivateFile(path); err != nil {
			return "", err
		}
		raw, err = os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read original notes backup: %w", err)
		}
	}
	original := memory.Snapshot{
		Profile: profile, Domain: domain, Raw: raw, Exists: manifest.OriginalExists,
		Fingerprint: memory.Fingerprint(raw, manifest.OriginalExists),
	}
	if original.Fingerprint != manifest.OriginalFingerprint {
		return "", errors.New("notes backup fingerprint is invalid")
	}
	committed, err := memory.RestoreIfCurrent(profile, domain, manifest.ResultFingerprint, original)
	if err != nil {
		if committed {
			return id, fmt.Errorf("notes were restored but durability sync failed: %w", err)
		}
		return "", err
	}
	return id, nil
}

func (s *BackupStore) root(profile, domain string) string {
	return filepath.Join(paths.DomainDir(profile, domain), backupDirectoryName)
}

func (s *BackupStore) backupIDs(profile, domain string) ([]string, error) {
	root := s.root(profile, domain)
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list notes backups: %w", err)
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), backupPrefix) {
			continue
		}
		ids = append(ids, entry.Name())
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	return ids, nil
}

func (s *BackupStore) prune(profile, domain string) error {
	ids, err := s.backupIDs(profile, domain)
	if err != nil {
		return err
	}
	if len(ids) <= backupRetention {
		return nil
	}
	for _, id := range ids[backupRetention:] {
		if err := os.RemoveAll(filepath.Join(s.root(profile, domain), id)); err != nil {
			return fmt.Errorf("remove expired notes backup %s: %w", id, err)
		}
	}
	return nil
}

func readManifest(dir string) (backupManifest, error) {
	path := filepath.Join(dir, "manifest.json")
	if err := requireRegularPrivateFile(path); err != nil {
		return backupManifest{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return backupManifest{}, fmt.Errorf("read notes backup metadata: %w", err)
	}
	var manifest backupManifest
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return backupManifest{}, fmt.Errorf("decode notes backup metadata: %w", err)
	}
	if manifest.ResultFingerprint == "" || manifest.OriginalFingerprint == "" {
		return backupManifest{}, errors.New("notes backup metadata is incomplete")
	}
	return manifest, nil
}

func requireRegularPrivateFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect notes backup %s: %w", filepath.Base(path), err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("notes backup %s is not a private regular file", filepath.Base(path))
	}
	return nil
}

func newBackupID() (string, error) {
	var random [6]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return backupPrefix + time.Now().UTC().Format("20060102T150405.000000000Z") + "-" + hex.EncodeToString(random[:]), nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	return errors.Join(syncErr, closeErr)
}
