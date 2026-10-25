package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	akactions "github.com/HumanHorizon/automata/internal/ai-knowledge/actions"
	"github.com/HumanHorizon/automata/internal/paths"
)

// actionStopGracePeriod bounds how long a stopped action may handle SIGTERM
// before its whole process group receives SIGKILL.
const actionStopGracePeriod = 2 * time.Second

type actionCommandRunner func(ctx context.Context, command, cwd string, args []string, output io.Writer) error

// processGroupAlive reports whether any process in the group pid still exists.
func processGroupAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(-pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// executeSystemAction runs a confirmed action. Log problems are returned
// separately and never prevent the command from starting.
type actionRuntimeContextKey struct{}

type actionRuntimeContext struct {
	profile  string
	domain   string
	actionID string
	runID    string
}

func withActionRuntime(ctx context.Context, profile, domain, actionID, runID string) context.Context {
	return context.WithValue(ctx, actionRuntimeContextKey{}, actionRuntimeContext{
		profile: profile, domain: domain, actionID: actionID, runID: runID,
	})
}

func executeSystemAction(ctx context.Context, action akactions.Action, logPath string, runner actionCommandRunner) (logErr error, runErr error) {
	if strings.TrimSpace(action.Command) == "" || strings.ContainsRune(action.Command, '\x00') {
		return nil, errors.New("action command is empty or contains NUL")
	}
	if !filepath.IsAbs(action.CWD) {
		return nil, errors.New("action working directory must be absolute")
	}
	info, err := os.Stat(action.CWD)
	if err != nil {
		return nil, fmt.Errorf("inspect action working directory: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("action working directory is not a directory")
	}
	if runner == nil {
		return nil, errors.New("action command runner is unavailable")
	}
	output, logErr := openActionLog(logPath, action)
	if output != nil {
		defer func() { _ = output.Close() }()
	}
	writer := io.Writer(io.Discard)
	if output != nil {
		writer = output
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	monitorDone := make(chan struct{})
	if control, ok := runCtx.Value(actionRuntimeContextKey{}).(actionRuntimeContext); ok {
		go func() {
			defer close(monitorDone)
			ticker := time.NewTicker(50 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-runCtx.Done():
					return
				case <-ticker.C:
					requested, err := akactions.StopRequested(control.profile, control.domain, control.actionID, control.runID)
					if err != nil || requested {
						cancel()
						return
					}
				}
			}
		}()
	} else {
		close(monitorDone)
	}
	runErr = runner(runCtx, "/bin/sh", action.CWD, []string{"-c", action.Command}, writer)
	cancel()
	<-monitorDone
	return logErr, runErr
}

// openActionLog creates or overwrites the fixed log file of an action and
// writes a header describing the run. A nil file means no log is available.
func openActionLog(logPath string, action akactions.Action) (*os.File, error) {
	if logPath == "" {
		return nil, errors.New("action log path is unavailable")
	}
	if err := paths.EnsurePrivateDir(filepath.Dir(logPath)); err != nil {
		return nil, fmt.Errorf("prepare action log directory: %w", err)
	}
	info, err := os.Lstat(logPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("inspect action log: %w", err)
	case info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular():
		return nil, errors.New("action log path is not a regular file")
	}
	file, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, paths.PrivateFileMode)
	if err != nil {
		return nil, fmt.Errorf("open action log: %w", err)
	}
	header := fmt.Sprintf("started: %s\naction: %s\ncwd: %s\ncommand: %s\n---\n",
		time.Now().Format(time.RFC3339), action.Name, action.CWD, action.Command)
	if _, err := io.WriteString(file, header); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("write action log header: %w", err)
	}
	return file, nil
}

// runShellAction starts the command in its own process group. Cancelling ctx
// sends SIGTERM to the whole group and SIGKILL after actionStopGracePeriod, so
// children started by the shell are stopped together with it.
func runShellAction(ctx context.Context, command, cwd string, args []string, output io.Writer) error {
	process := exec.Command(command, args...)
	process.Dir = cwd
	process.Stdout = output
	process.Stderr = output
	process.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := process.Start(); err != nil {
		return err
	}
	groupID := process.Process.Pid
	if control, ok := ctx.Value(actionRuntimeContextKey{}).(actionRuntimeContext); ok {
		if err := akactions.PublishRunPID(control.profile, control.domain, control.actionID, control.runID, groupID); err != nil {
			_ = syscall.Kill(-groupID, syscall.SIGTERM)
			_ = process.Wait()
			return fmt.Errorf("publish action process identity: %w", err)
		}
	}
	waitErr := make(chan error, 1)
	go func() { waitErr <- process.Wait() }()

	select {
	case err := <-waitErr:
		return err
	case <-ctx.Done():
	}

	_ = syscall.Kill(-groupID, syscall.SIGTERM)
	timer := time.NewTimer(actionStopGracePeriod)
	defer timer.Stop()
	select {
	case <-waitErr:
	case <-timer.C:
		_ = syscall.Kill(-groupID, syscall.SIGKILL)
		<-waitErr
	}
	_ = syscall.Kill(-groupID, syscall.SIGKILL)
	return ctx.Err()
}
