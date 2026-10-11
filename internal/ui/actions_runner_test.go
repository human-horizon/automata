package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	akactions "github.com/HumanHorizon/automata/internal/ai-knowledge/actions"
)

func TestExecuteSystemActionRequiresValidCommandAndAbsoluteDirectory(t *testing.T) {
	cwd := t.TempDir()
	const command = "printf '%s\\n' 'must not be executed by this test'"
	action := akactions.Action{Name: "Test", Command: command, CWD: cwd}
	called := false
	runner := func(_ context.Context, gotCommand, gotCWD string, args []string) error {
		called = true
		if gotCommand != "/bin/sh" || gotCWD != cwd || !reflect.DeepEqual(args, []string{"-c", command}) {
			t.Fatalf("runner received command=%q cwd=%q args=%#v", gotCommand, gotCWD, args)
		}
		return nil
	}
	if err := executeSystemAction(context.Background(), action, runner); err != nil {
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
		err := executeSystemAction(context.Background(), invalid, func(context.Context, string, string, []string) error {
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
	if err := executeSystemAction(context.Background(), akactions.Action{Command: "true", CWD: filePath}, runner); err == nil {
		t.Fatal("file working directory was accepted")
	}
}

func TestExecuteSystemActionReturnsRunnerFailure(t *testing.T) {
	failure := errors.New("synthetic process error")
	err := executeSystemAction(context.Background(), akactions.Action{Command: "false", CWD: t.TempDir()}, func(context.Context, string, string, []string) error {
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("runner failure = %v, want %v", err, failure)
	}
}

// TestRunShellActionCancelStopsProcessGroup starts a harmless local sleep in a
// background child of the shell, cancels the context and verifies that the
// child is gone too. The command is test-owned and never comes from user data.
func TestRunShellActionCancelStopsProcessGroup(t *testing.T) {
	cwd := t.TempDir()
	pidFile := filepath.Join(cwd, "child.pid")
	script := "sleep 30 & echo $! > child.pid; wait"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- runShellAction(ctx, "/bin/sh", cwd, []string{"-c", script}) }()

	childPID := waitForPIDFile(t, pidFile)
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled action error = %v, want context.Canceled", err)
		}
	case <-time.After(actionStopGracePeriod + 3*time.Second):
		t.Fatal("cancelled action did not return")
	}
	deadline := time.Now().Add(2 * time.Second)
	for syscall.Kill(childPID, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("child process %d survived the group stop", childPID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRunShellActionReturnsCompletionWithoutCancel(t *testing.T) {
	if err := runShellAction(context.Background(), "/bin/sh", t.TempDir(), []string{"-c", "exit 0"}); err != nil {
		t.Fatalf("successful command error = %v", err)
	}
}

func waitForPIDFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, convErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if convErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pid file %q was not written", path)
	return 0
}
