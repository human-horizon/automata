package ui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	akactions "github.com/HumanHorizon/automata/internal/ai-knowledge/actions"
)

type actionCommandRunner func(command, cwd string, args []string) error

func executeSystemAction(action akactions.Action, runner actionCommandRunner) error {
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
	return runner("/bin/sh", action.CWD, []string{"-c", action.Command})
}

func runShellAction(command, cwd string, args []string) error {
	process := exec.Command(command, args...)
	process.Dir = cwd
	process.Stdout = io.Discard
	process.Stderr = io.Discard
	return process.Run()
}
