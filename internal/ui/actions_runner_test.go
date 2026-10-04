package ui

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	akactions "github.com/HumanHorizon/automata/internal/ai-knowledge/actions"
)

func TestExecuteSystemActionRequiresValidCommandAndAbsoluteDirectory(t *testing.T) {
	cwd := t.TempDir()
	const command = "printf '%s\\n' 'must not be executed by this test'"
	action := akactions.Action{Name: "Test", Command: command, CWD: cwd}
	called := false
	runner := func(gotCommand, gotCWD string, args []string) error {
		called = true
		if gotCommand != "/bin/sh" || gotCWD != cwd || !reflect.DeepEqual(args, []string{"-c", command}) {
			t.Fatalf("runner received command=%q cwd=%q args=%#v", gotCommand, gotCWD, args)
		}
		return nil
	}
	if err := executeSystemAction(action, runner); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("valid confirmed action did not reach the injected runner")
	}

	for _, invalid := range []akactions.Action{
		{Name: "empty command", Command: " ", CWD: cwd},
		{Name: "NUL command", Command: "echo\x00bad", CWD: cwd},
		{Name: "relative cwd", Command: "true", CWD: "relative"},
		{Name: "missing cwd", Command: "true", CWD: filepath.Join(cwd, "missing")},
	} {
		runnerCalled := false
		err := executeSystemAction(invalid, func(string, string, []string) error {
			runnerCalled = true
			return nil
		})
		if err == nil || runnerCalled {
			t.Errorf("invalid action reached runner: %#v err=%v called=%v", invalid, err, runnerCalled)
		}
	}

	filePath := filepath.Join(cwd, "not-a-directory")
	if err := os.WriteFile(filePath, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := executeSystemAction(akactions.Action{Command: "true", CWD: filePath}, runner); err == nil {
		t.Fatal("file working directory was accepted")
	}
}

func TestExecuteSystemActionReturnsRunnerFailure(t *testing.T) {
	failure := errors.New("synthetic process error")
	err := executeSystemAction(akactions.Action{Command: "false", CWD: t.TempDir()}, func(string, string, []string) error {
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("runner failure = %v, want %v", err, failure)
	}
}
