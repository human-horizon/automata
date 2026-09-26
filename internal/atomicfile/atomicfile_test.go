package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type failingTemporary struct {
	*os.File
	stage string
	err   error
}

func (f *failingTemporary) Chmod(mode os.FileMode) error {
	if f.stage == "chmod" {
		return f.err
	}
	return f.File.Chmod(mode)
}

func (f *failingTemporary) Write(data []byte) (int, error) {
	if f.stage == "write" {
		return 0, f.err
	}
	return f.File.Write(data)
}

func (f *failingTemporary) Sync() error {
	if f.stage == "sync" {
		return f.err
	}
	return f.File.Sync()
}

func (f *failingTemporary) Close() error {
	closeErr := f.File.Close()
	if f.stage == "close" {
		return errors.Join(f.err, closeErr)
	}
	return closeErr
}

type failingDirectory struct{ err error }

func (d failingDirectory) Sync() error  { return d.err }
func (d failingDirectory) Close() error { return nil }

func TestWriteReplacesTargetAndAppliesMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("contents = %q, want new", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 600", got)
	}
}

func TestWritePrecommitFailuresPreserveTargetAndRemoveTemporaryFile(t *testing.T) {
	for _, stage := range []string{"create", "chmod", "write", "sync", "close", "rename"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "state.json")
			original := []byte("original")
			if err := os.WriteFile(path, original, 0o644); err != nil {
				t.Fatal(err)
			}
			injected := errors.New("injected " + stage + " failure")
			ops := defaultOperations
			ops.createTemp = func(tempDir, pattern string) (temporaryFile, error) {
				if stage == "create" {
					return nil, injected
				}
				file, err := os.CreateTemp(tempDir, pattern)
				if err != nil {
					return nil, err
				}
				return &failingTemporary{File: file, stage: stage, err: injected}, nil
			}
			if stage == "rename" {
				ops.rename = func(string, string) error { return injected }
			}

			err := writeWithOperations(path, []byte("replacement"), 0o644, ops)
			if err == nil || IsCommitted(err) || !errors.Is(err, injected) {
				t.Fatalf("write error = %v, want precommit %q", err, injected)
			}
			got, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(got) != string(original) {
				t.Fatalf("precommit failure changed target: %q", got)
			}
			entries, readErr := os.ReadDir(dir)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if len(entries) != 1 || entries[0].Name() != "state.json" {
				t.Fatalf("temporary file was not removed: %#v", entries)
			}
		})
	}
}

func TestWriteDirectorySyncFailureReturnsCommittedError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	syncErr := errors.New("injected directory sync failure")
	ops := defaultOperations
	ops.openDir = func(string) (syncCloser, error) { return failingDirectory{err: syncErr}, nil }

	err := writeWithOperations(path, []byte("new"), 0o644, ops)
	var committed *CommittedError
	if !errors.As(err, &committed) || !errors.Is(err, syncErr) || committed.Path != path {
		t.Fatalf("write error = %v, want committed directory-sync failure", err)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "new" {
		t.Fatalf("committed target = %q, want new", got)
	}
}
