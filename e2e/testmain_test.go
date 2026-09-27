package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func goEnvValue(name string) (string, error) {
	cmd := exec.Command("go", "env", name)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("go env %s: %w", name, err)
	}
	value := strings.TrimSpace(string(out))
	if value == "" {
		return "", fmt.Errorf("go env %s returned an empty value", name)
	}
	return value, nil
}

func TestMain(m *testing.M) {
	originalGoPath, err := goEnvValue("GOPATH")
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve E2E GOPATH: %v\n", err)
		os.Exit(1)
	}
	originalGoCache, err := goEnvValue("GOCACHE")
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve E2E GOCACHE: %v\n", err)
		os.Exit(1)
	}

	sandbox, err := os.MkdirTemp("", "automata-e2e-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create E2E sandbox: %v\n", err)
		os.Exit(1)
	}

	cleanup := func() error { return os.RemoveAll(sandbox) }
	failSetup := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
		if cleanupErr := cleanup(); cleanupErr != nil {
			fmt.Fprintf(os.Stderr, "remove failed E2E sandbox: %v\n", cleanupErr)
		}
		os.Exit(1)
	}

	homeDir := filepath.Join(sandbox, "home")
	dataDir := filepath.Join(homeDir, ".ai", "automata")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		failSetup("create E2E home: %v", err)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		failSetup("create E2E data dir: %v", err)
	}
	if err := os.Setenv("HOME", homeDir); err != nil {
		failSetup("set E2E HOME: %v", err)
	}
	if err := os.Setenv("GOPATH", originalGoPath); err != nil {
		failSetup("set E2E GOPATH: %v", err)
	}
	if err := os.Setenv("GOCACHE", originalGoCache); err != nil {
		failSetup("set E2E GOCACHE: %v", err)
	}
	if err := os.Setenv("AI_DATA_HOME", dataDir); err != nil {
		failSetup("set E2E AI_DATA_HOME: %v", err)
	}
	if os.Getenv("AUTOMATA_E2E_ARTIFACTS_DIR") == "" {
		if err := os.Setenv("AUTOMATA_E2E_ARTIFACTS_DIR", filepath.Join(sandbox, "artifacts")); err != nil {
			failSetup("set E2E artifact dir: %v", err)
		}
	}

	if os.Getenv("AUTOMATA_BIN") == "" {
		root, err := filepath.Abs("..")
		if err != nil {
			failSetup("resolve project root: %v", err)
		}
		binary := filepath.Join(sandbox, "automata")
		cmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", binary, ".")
		cmd.Dir = root
		output, err := cmd.CombinedOutput()
		if err != nil {
			failSetup("build E2E Automata: %v\n%s", err, output)
		}
		if err := os.Setenv("AUTOMATA_BIN", binary); err != nil {
			failSetup("set AUTOMATA_BIN: %v", err)
		}
	}

	code := m.Run()
	if err := cleanup(); err != nil {
		fmt.Fprintf(os.Stderr, "remove E2E sandbox: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

func automataBinary(t *testing.T) string {
	t.Helper()
	binary := os.Getenv("AUTOMATA_BIN")
	if binary == "" {
		t.Fatal("AUTOMATA_BIN is empty after TestMain setup")
	}
	return binary
}
