package notesactualize

import (
	"bytes"
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
)

const (
	defaultCallTimeout      = 3 * time.Minute
	processStopGracePeriod  = 2 * time.Second
	processStopPollInterval = 20 * time.Millisecond
	processWaitDelay        = processStopGracePeriod + time.Second
	maxStdoutBytes          = 256 * 1024
	maxStderrBytes          = 8 * 1024
)

// PiRunner calls the configured just CLI without tools, sessions, project context,
// extension discovery, or a shell. Credentials remain in the existing agent directory.
type PiRunner struct {
	Executable string
	AgentDir   string
	Profile    string
	DataHome   string
	Timeout    time.Duration
}

// Run invokes just once with a bounded prompt and returns its text response.
func (r PiRunner) Run(ctx context.Context, prompt string, options RunOptions) (string, error) {
	if strings.TrimSpace(prompt) == "" {
		return "", errors.New("empty AI prompt")
	}
	executable := strings.TrimSpace(r.Executable)
	if executable == "" {
		return "", errors.New("just executable is not configured")
	}
	settings, err := LoadSettings(r.AgentDir)
	if err != nil {
		return "", err
	}
	if err := settings.ValidateOptions(options); err != nil {
		return "", err
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = defaultCallTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := []string{
		"--print", "--mode", "text", "--no-session", "--no-tools", "--no-mcp",
		"--no-extensions", "--no-context-files", "--no-skills", "--no-prompt-templates",
		"--no-approve", "--model", options.Model, "--thinking", options.ThinkingLevel, prompt,
	}
	command := exec.CommandContext(callCtx, executable, args...)
	command.Dir = os.TempDir()
	command.Env = runnerEnvironment(os.Environ(), r)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.WaitDelay = processWaitDelay
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		return terminateProcessGroup(command.Process.Pid, processStopGracePeriod)
	}
	stdout := &boundedBuffer{max: maxStdoutBytes}
	stderr := &boundedBuffer{max: maxStderrBytes}
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		if errors.Is(callCtx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("just request exceeded %s timeout", timeout)
		}
		if errors.Is(callCtx.Err(), context.Canceled) {
			return "", fmt.Errorf("just request canceled: %w", callCtx.Err())
		}
		return "", fmt.Errorf("just request failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if err := callCtx.Err(); err != nil {
		return "", fmt.Errorf("just request canceled: %w", err)
	}
	if stdout.overflow {
		return "", fmt.Errorf("pi response exceeds %d bytes", maxStdoutBytes)
	}
	response := strings.TrimSpace(stdout.String())
	if response == "" {
		return "", errors.New("pi returned an empty response")
	}
	return response, nil
}

func terminateProcessGroup(processGroupID int, gracePeriod time.Duration) error {
	if processGroupID <= 0 {
		return errors.New("invalid just process group ID")
	}
	if err := syscall.Kill(-processGroupID, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return fmt.Errorf("send SIGTERM to just process group: %w", err)
	}

	timer := time.NewTimer(gracePeriod)
	defer timer.Stop()
	ticker := time.NewTicker(processStopPollInterval)
	defer ticker.Stop()
	for {
		err := syscall.Kill(-processGroupID, 0)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if err != nil && !errors.Is(err, syscall.EPERM) {
			return fmt.Errorf("check just process group: %w", err)
		}
		select {
		case <-ticker.C:
		case <-timer.C:
			err := syscall.Kill(-processGroupID, syscall.SIGKILL)
			if errors.Is(err, syscall.ESRCH) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("send SIGKILL to just process group: %w", err)
			}
			return nil
		}
	}
}

// ResolveExecutable locates the just CLI without invoking a shell.
func ResolveExecutable(lookup func(string) (string, error)) (string, error) {
	if lookup == nil {
		lookup = exec.LookPath
	}
	path, err := lookup("just")
	if err != nil {
		return "", fmt.Errorf("just CLI is unavailable on PATH: %w", err)
	}
	return path, nil
}

func runnerEnvironment(environment []string, runner PiRunner) []string {
	overrides := map[string]string{
		"AI_PROFILE":            runner.Profile,
		"AUTOMATA_PROFILE":      runner.Profile,
		"AI_DATA_HOME":          runner.DataHome,
		"AUTOMATA_HUMAN_EXPORT": "1",
	}
	if runner.AgentDir != "" {
		overrides["PI_CODING_AGENT_DIR"] = filepath.Clean(runner.AgentDir)
	}
	result := make([]string, 0, len(environment)+len(overrides))
	for _, entry := range environment {
		key, _, found := strings.Cut(entry, "=")
		if !found {
			result = append(result, entry)
			continue
		}
		if _, overridden := overrides[key]; overridden {
			continue
		}
		result = append(result, entry)
	}
	for key, value := range overrides {
		result = append(result, key+"="+value)
	}
	return result
}

type boundedBuffer struct {
	bytes.Buffer
	max      int
	overflow bool
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	originalLength := len(data)
	remaining := b.max - b.Len()
	if remaining <= 0 {
		b.overflow = true
		return originalLength, nil
	}
	if len(data) > remaining {
		b.overflow = true
		data = data[:remaining]
	}
	_, _ = b.Buffer.Write(data)
	return originalLength, nil
}

var _ io.Writer = (*boundedBuffer)(nil)
