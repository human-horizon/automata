// Package atomicfile writes files using a same-directory temporary file and rename.
package atomicfile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type temporaryFile interface {
	Name() string
	Chmod(os.FileMode) error
	Write([]byte) (int, error)
	Sync() error
	Close() error
}

type syncCloser interface {
	Sync() error
	Close() error
}

type operations struct {
	createTemp func(string, string) (temporaryFile, error)
	rename     func(string, string) error
	openDir    func(string) (syncCloser, error)
	remove     func(string) error
}

var defaultOperations = operations{
	createTemp: func(dir, pattern string) (temporaryFile, error) {
		return os.CreateTemp(dir, pattern)
	},
	rename:  os.Rename,
	openDir: func(path string) (syncCloser, error) { return os.Open(path) },
	remove:  os.Remove,
}

// CommittedError reports a failure after the target was replaced by rename.
// Callers must treat the new file contents as committed and must not roll them back.
type CommittedError struct {
	Path string
	Err  error
}

func (e *CommittedError) Error() string {
	return fmt.Sprintf("atomic file %s committed but directory sync failed: %v", e.Path, e.Err)
}

func (e *CommittedError) Unwrap() error { return e.Err }

// IsCommitted reports whether err happened after the atomic rename committed.
func IsCommitted(err error) bool {
	var committed *CommittedError
	return errors.As(err, &committed)
}

// Write atomically replaces path with data and mode. Parent directories must
// already exist. Errors before rename leave the previous target untouched;
// errors after rename are returned as CommittedError.
func Write(path string, data []byte, mode os.FileMode) error {
	return WriteFrom(path, bytes.NewReader(data), mode)
}

// WriteFrom atomically replaces path with bytes read from source and mode.
// The source is streamed to avoid buffering large migration or session files.
func WriteFrom(path string, source io.Reader, mode os.FileMode) error {
	return writeFromWithOperations(path, source, mode, defaultOperations)
}

func writeWithOperations(path string, data []byte, mode os.FileMode, ops operations) error {
	return writeFromWithOperations(path, bytes.NewReader(data), mode, ops)
}

func writeFromWithOperations(path string, source io.Reader, mode os.FileMode, ops operations) error {
	dir := filepath.Dir(path)
	temporary, err := ops.createTemp(dir, ".atomicfile-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary file for %s: %w", path, err)
	}
	temporaryPath := temporary.Name()
	committed := false
	closed := false
	defer func() {
		if !closed {
			_ = temporary.Close()
		}
		if !committed {
			_ = ops.remove(temporaryPath)
		}
	}()

	if err := temporary.Chmod(mode.Perm()); err != nil {
		return fmt.Errorf("set temporary file mode for %s: %w", path, err)
	}
	if _, err := io.Copy(temporary, source); err != nil {
		return fmt.Errorf("write temporary file for %s: %w", path, err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary file for %s: %w", path, err)
	}
	if err := temporary.Close(); err != nil {
		closed = true
		return fmt.Errorf("close temporary file for %s: %w", path, err)
	}
	closed = true
	if err := ops.rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	committed = true

	directory, err := ops.openDir(dir)
	if err != nil {
		return &CommittedError{Path: path, Err: fmt.Errorf("open parent directory: %w", err)}
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil || closeErr != nil {
		return &CommittedError{Path: path, Err: errors.Join(syncErr, closeErr)}
	}
	return nil
}
