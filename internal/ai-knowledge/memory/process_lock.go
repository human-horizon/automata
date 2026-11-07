package memory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const (
	notesFileLockTimeout = 10 * time.Second
	notesFileLockPoll    = 20 * time.Millisecond
)

// withNotesFileLock serializes Notes access with Pi's fs-ext flock on the
// stable sibling path <notes.json>.lock. The file is intentionally retained:
// unlinking a locked inode can split contenders across different lock files.
func withNotesFileLock(notesPath string, operation func() error) error {
	lockPath := notesPath + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return fmt.Errorf("create Notes lock directory: %w", err)
	}

	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open Notes lock: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat Notes lock: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("Notes lock is not a regular file: %s", lockPath)
	}

	deadline := time.Now().Add(notesFileLockTimeout)
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return fmt.Errorf("acquire Notes lock: %w", err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out acquiring Notes lock: %s", lockPath)
		}
		time.Sleep(notesFileLockPoll)
	}
	defer func() { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN) }()

	return operation()
}
