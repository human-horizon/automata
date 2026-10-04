package main

import (
	"context"
	"crypto/sha256"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/HumanHorizon/automata/internal/paths"
)

func TestStorageEnvironmentIsIsolated(t *testing.T) {
	for _, name := range []string{"AI_DATA_HOME", "AUTOMATA_HOME", "AI_PROFILE", "AUTOMATA_PROFILE"} {
		if value, exists := os.LookupEnv(name); exists {
			t.Errorf("inherited %s is still present: %q", name, value)
		}
	}
	home := os.Getenv("HOME")
	if home == "" {
		t.Fatal("isolated HOME is empty")
	}
	if got, want := paths.BaseDir(), filepath.Join(home, ".ai", "automata"); got != want {
		t.Errorf("default data root = %q, want %q", got, want)
	}
}

func TestInheritedStateRootsAreUntouched(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		setData bool
		empty   bool
	}{
		{name: "unset"},
		{name: "empty", setData: true, empty: true},
		{name: "explicit", setData: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			home := filepath.Join(root, "home")
			data := filepath.Join(root, "data")
			legacy := filepath.Join(root, "legacy")
			for _, base := range []string{filepath.Join(home, ".ai", "automata"), data, legacy} {
				directory := filepath.Join(base, "profiles", "humanhorizon")
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatal(err)
				}
				state := []byte("{\"version\":2,\"items\":[{\"name\":\"retained-original\",\"is_folder\":true}]}\n")
				if err := os.WriteFile(filepath.Join(directory, "state.json"), state, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before := snapshotIsolationFixtures(t, root)
			environment := callerStateEnvironment(home, legacy)
			if test.setData {
				value := data
				if test.empty {
					value = ""
				}
				environment = append(environment, "AI_DATA_HOME="+value)
			}
			ctx := context.Background()
			if deadline, exists := t.Deadline(); exists {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, deadline)
				defer cancel()
			}
			cmd := exec.CommandContext(ctx, executable, "-test.run=^(TestStorageEnvironmentIsIsolated|TestMoveFolderMigratesActions|TestSmokeStatusBadges)$", "-test.count=1")
			cmd.Env = environment
			output, runErr := cmd.CombinedOutput()
			if after := snapshotIsolationFixtures(t, root); !reflect.DeepEqual(before, after) {
				t.Errorf("child tests changed caller-owned storage: before=%v after=%v", before, after)
			}
			if runErr != nil {
				t.Errorf("child tests failed: %v\n%s", runErr, output)
			}
		})
	}
}

type isolationFixtureState struct {
	Mode fs.FileMode
	Hash [sha256.Size]byte
}

func snapshotIsolationFixtures(t *testing.T, root string) map[string]isolationFixtureState {
	t.Helper()
	files := os.DirFS(root)
	state := make(map[string]isolationFixtureState)
	if err := fs.WalkDir(files, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		item := isolationFixtureState{Mode: info.Mode()}
		if info.Mode().IsRegular() {
			data, err := fs.ReadFile(files, path)
			if err != nil {
				return err
			}
			item.Hash = sha256.Sum256(data)
		}
		state[path] = item
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return state
}

func callerStateEnvironment(home, legacy string) []string {
	var environment []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		switch name {
		case "HOME", "AI_DATA_HOME", "AUTOMATA_HOME", "AI_PROFILE", "AUTOMATA_PROFILE":
			continue
		}
		environment = append(environment, entry)
	}
	return append(environment, "HOME="+home, "AUTOMATA_HOME="+legacy, "AI_PROFILE=caller-profile", "AUTOMATA_PROFILE=caller-profile")
}
