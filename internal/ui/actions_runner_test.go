package ui

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	akactions "github.com/HumanHorizon/automata/internal/ai-knowledge/actions"
	"github.com/HumanHorizon/automata/internal/paths"
)

func TestExecuteSystemActionCancelsWhenPiPublishesSharedStopRequest(t *testing.T) {
	profile, domain := "runner-stop", "runner-stop__project"
	t.Setenv("AI_DATA_HOME", t.TempDir())
	if err := paths.EnsureDomainDir(profile, domain); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(paths.DomainDir(profile, domain), "actions"), 0o700); err != nil {
		t.Fatal(err)
	}
	action := akactions.Action{ID: strings.Repeat("e", 64), Name: "Server", Command: "server", CWD: t.TempDir()}
	runID := strings.Repeat("f", 32)
	if _, err := akactions.ClaimRun(profile, domain, action.ID, runID, "automata-ui"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := akactions.FinishRun(profile, domain, action.ID, runID); err != nil {
			t.Errorf("finish run: %v", err)
		}
	}()
	ctx := withActionRuntime(context.Background(), profile, domain, action.ID, runID)
	result := make(chan error, 1)
	go func() {
		_, err := executeSystemAction(ctx, action, "", func(ctx context.Context, _, _ string, _ []string, _ io.Writer) error {
			<-ctx.Done()
			return ctx.Err()
		})
		result <- err
	}()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if err := akactions.RequestStop(profile, domain, action.ID, runID); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("run error = %v, want context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shared Pi stop request did not cancel the UI-owned runner")
	}
}

func TestExecuteSystemActionRequiresValidCommandAndAbsoluteDirectory(t *testing.T) {
	cwd := t.TempDir()
	const command = "printf '%s\\n' 'must not be executed by this test'"
	action := akactions.Action{Name: "Test", Command: command, CWD: cwd}
	called := false
	runner := func(_ context.Context, gotCommand, gotCWD string, args []string, _ io.Writer) error {
		called = true
		if gotCommand != "/bin/sh" || gotCWD != cwd || !reflect.DeepEqual(args, []string{"-c", command}) {
			t.Fatalf("runner received command=%q cwd=%q args=%#v", gotCommand, gotCWD, args)
		}
		return nil
	}
	if _, err := executeSystemAction(context.Background(), action, "", runner); err != nil {
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
		_, err := executeSystemAction(context.Background(), invalid, "", func(context.Context, string, string, []string, io.Writer) error {
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
	if _, err := executeSystemAction(context.Background(), akactions.Action{Command: "true", CWD: filePath}, "", runner); err == nil {
		t.Fatal("file working directory was accepted")
	}
}

func TestExecuteSystemActionReturnsRunnerFailure(t *testing.T) {
	failure := errors.New("synthetic process error")
	_, err := executeSystemAction(context.Background(), akactions.Action{Command: "false", CWD: t.TempDir()}, "", func(context.Context, string, string, []string, io.Writer) error {
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("runner failure = %v, want %v", err, failure)
	}
}

func TestExecuteSystemActionOverwritesFixedLogWithHeaderAndOutput(t *testing.T) {
	cwd := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "logs", "id.log")
	action := akactions.Action{Name: "Build", Command: "make test", CWD: cwd}
	write := func(text string) func(context.Context, string, string, []string, io.Writer) error {
		return func(_ context.Context, _, _ string, _ []string, output io.Writer) error {
			_, err := io.WriteString(output, text)
			return err
		}
	}

	if logErr, err := executeSystemAction(context.Background(), action, logPath, write("first run\n")); logErr != nil || err != nil {
		t.Fatalf("first run logErr=%v err=%v", logErr, err)
	}
	if logErr, err := executeSystemAction(context.Background(), action, logPath, write("second run\n")); logErr != nil || err != nil {
		t.Fatalf("second run logErr=%v err=%v", logErr, err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "first run") || !strings.Contains(text, "second run") {
		t.Fatalf("log was not overwritten by the second run: %q", text)
	}
	for _, want := range []string{"action: Build", "command: make test", "cwd: " + cwd, "---\n"} {
		if !strings.Contains(text, want) {
			t.Fatalf("log header misses %q: %q", want, text)
		}
	}
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("log mode = %o, want 600", got)
	}
}

func TestExecuteSystemActionRunsWhenLogCannotBeWritten(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.log")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	symlinkLog := filepath.Join(root, "link.log")
	if err := os.Symlink(target, symlinkLog); err != nil {
		t.Fatal(err)
	}
	action := akactions.Action{Name: "Build", Command: "true", CWD: root}
	for _, logPath := range []string{"", symlinkLog} {
		ran := false
		logErr, err := executeSystemAction(context.Background(), action, logPath, func(_ context.Context, _, _ string, _ []string, output io.Writer) error {
			ran = true
			if output == nil {
				t.Fatal("runner received a nil output")
			}
			return nil
		})
		if logErr == nil {
			t.Fatalf("log path %q produced no log error", logPath)
		}
		if err != nil || !ran {
			t.Fatalf("log path %q blocked the run: ran=%v err=%v", logPath, ran, err)
		}
	}
	if data, err := os.ReadFile(target); err != nil || len(data) != 0 {
		t.Fatalf("symlink target was written: data=%q err=%v", data, err)
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
	go func() { result <- runShellAction(ctx, "/bin/sh", cwd, []string{"-c", script}, io.Discard) }()

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
	if err := runShellAction(context.Background(), "/bin/sh", t.TempDir(), []string{"-c", "exit 0"}, io.Discard); err != nil {
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
