// Package testutil exposes helpers shared by Automata tests.
//
// RunMain is meant to be called from a package-level TestMain to redirect
// HOME to a temporary directory for the lifetime of the test binary.
// Tests that call tree.AddChat, tree.AddFolder, or anything that triggers
// autoSave otherwise write to the real ~/.ai/automata/profiles/<profile>/
// state.json and pollute the user's profile. The original HOME is
// restored after the test run, so manual runs of `automata` are
// unaffected.
package testutil

import (
	"os"
	"testing"
)

// RunMain wraps m.Run() with HOME isolation. The caller must put a
// TestMain in their *_test.go that calls this:
//
//	func TestMain(m *testing.M) { testutil.RunMain(m) }
//
// Any test that calls paths.BaseDir() (directly or via Tree.SaveState,
// Tree.LoadState, status.NewCachedReader, etc.) will then read and write
// only inside the temporary directory.
func RunMain(m *testing.M) {
	origHome, hadHome := os.LookupEnv("HOME")
	origData, hadData := os.LookupEnv("AI_DATA_HOME")

	tmp, err := os.MkdirTemp("", "automata-test-home-")
	if err != nil {
		panic("testutil: cannot create temp HOME: " + err.Error())
	}
	defer os.RemoveAll(tmp)

	// HOME drives os.UserHomeDir(); paths.dataHome() honours
	// AI_DATA_HOME, so we set both for belt-and-suspenders coverage.
	if err := os.Setenv("HOME", tmp); err != nil {
		panic("testutil: cannot set HOME: " + err.Error())
	}
	if err := os.Setenv("AI_DATA_HOME", tmp); err != nil {
		panic("testutil: cannot set AI_DATA_HOME: " + err.Error())
	}

	code := m.Run()

	if hadHome {
		_ = os.Setenv("HOME", origHome)
	} else {
		_ = os.Unsetenv("HOME")
	}
	if hadData {
		_ = os.Setenv("AI_DATA_HOME", origData)
	} else {
		_ = os.Unsetenv("AI_DATA_HOME")
	}
	os.Exit(code)
}
