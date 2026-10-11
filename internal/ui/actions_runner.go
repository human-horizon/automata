package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	akactions "github.com/HumanHorizon/automata/internal/ai-knowledge/actions"
)

// actionStopGracePeriod bounds how long a stopped action may handle SIGTERM
// before its whole process group receives SIGKILL.
const actionStopGracePeriod = 2 * time.Second

type actionCommandRunner func(ctx context.Context, command, cwd string, args []string) error

func executeSystemAction(ctx context.Context, action akactions.Action, runner actionCommandRunner) error {
	if strings.TrimSpace(action.Command) == "" || strings.ContainsRune(action.Command, '\x00') {
		return errors.New("action command is empty or contains NUL")
	}
	if !filepath.IsAbs(action.CWD) {
		return errors.New("action working directory must be absolute")
	}
	info, err := os.Stat(action.CWD)
	if err != nil {
		return fmt.Errorf("inspect action working directory: %w", err)
	}
	if !info.IsDir() {
		return errors.New("action working directory is not a directory")
	}
	if runner == nil {
		return errors.New("action command runner is unavailable")
	}
	return runner(ctx, "/bin/sh", action.CWD, []string{"-c", action.Command})
}

// runShellAction starts the command in its own process group. Cancelling ctx
// sends SIGTERM to the whole group and SIGKILL after actionStopGracePeriod, so
// children started by the shell are stopped together with it.
func runShellAction(ctx context.Context, command, cwd string, args []string) error {
	process := exec.Command(command, args...)
	process.Dir = cwd
	process.Stdout = io.Discard
	process.Stderr = io.Discard
	process.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := process.Start(); err != nil {
		return err
	}
	groupID := process.Process.Pid
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
