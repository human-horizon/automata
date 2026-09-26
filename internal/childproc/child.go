package childproc

import (
	"errors"
	"os/exec"
)

// StartAndReap starts a short-lived external command and reaps it asynchronously.
func StartAndReap(command *exec.Cmd) error {
	return startAndReap(command, func(command *exec.Cmd) error {
		return command.Wait()
	})
}

func startAndReap(command *exec.Cmd, wait func(*exec.Cmd) error) error {
	if command == nil {
		return errors.New("cannot start a nil command")
	}
	if wait == nil {
		return errors.New("cannot start a command without a wait function")
	}
	if err := command.Start(); err != nil {
		return err
	}
	go func() {
		_ = wait(command)
	}()
	return nil
}
