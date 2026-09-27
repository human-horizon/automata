package childproc

import (
	"fmt"
	"os/exec"
	"runtime"
)

// OpenPath asks the platform file manager to reveal/open path and reaps the launcher process.
func OpenPath(path string) error {
	command, err := openPathCommand(runtime.GOOS, path)
	if err != nil {
		return err
	}
	return StartAndReap(command)
}

func openPathCommand(goos, path string) (*exec.Cmd, error) {
	switch goos {
	case "darwin":
		return exec.Command("open", path), nil
	case "linux":
		return exec.Command("xdg-open", path), nil
	case "windows":
		return exec.Command("cmd", "/c", "start", "", path), nil
	default:
		return nil, fmt.Errorf("opening paths is unsupported on %s", goos)
	}
}
