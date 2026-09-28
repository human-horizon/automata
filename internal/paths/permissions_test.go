package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureSessionDirHardensLegacyDirectoryChain(t *testing.T) {
	base := filepath.Join(t.TempDir(), "automata")
	t.Setenv("AI_DATA_HOME", base)
	session := SessionDir("Private Profile", "private-profile__chat")
	if err := os.MkdirAll(session, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{base, filepath.Join(base, "profiles"), ProfileDir("Private Profile"), SessionsDir("Private Profile"), session} {
		if err := os.Chmod(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := EnsureSessionDir("Private Profile", "private-profile__chat"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{base, filepath.Join(base, "profiles"), ProfileDir("Private Profile"), SessionsDir("Private Profile"), session} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != PrivateDirMode {
			t.Fatalf("%s mode = %o, want %o", path, got, PrivateDirMode)
		}
	}
}

func TestEnsurePrivateDirOutsideBaseHardensTarget(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "automata")
	t.Setenv("AI_DATA_HOME", base)
	target := filepath.Join(root, "outside")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateDir(target); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != PrivateDirMode {
		t.Fatalf("outside target mode = %o, want %o", got, PrivateDirMode)
	}
}
