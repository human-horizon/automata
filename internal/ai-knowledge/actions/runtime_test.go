package actions

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/HumanHorizon/automata/internal/paths"
)

func TestActionRuntimeClaimStopAndFinish(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	profile, domain := "test", "test__folder"
	actionID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	runID := "0123456789abcdef0123456789abcdef"
	if err := paths.EnsureDomainDir(profile, domain); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(paths.DomainDir(profile, domain), "actions"), 0o700); err != nil {
		t.Fatal(err)
	}

	record, err := ClaimRun(profile, domain, actionID, runID, "automata-ui")
	if err != nil {
		t.Fatal(err)
	}
	if record.RunID != runID || record.PID != 0 {
		t.Fatalf("unexpected claim: %+v", record)
	}
	if _, err := ClaimRun(profile, domain, actionID, "abcdef0123456789abcdef0123456789", "pi"); err == nil {
		t.Fatal("expected duplicate claim rejection")
	}
	if err := PublishRunPID(profile, domain, actionID, runID, 1234); err != nil {
		t.Fatal(err)
	}
	if err := RequestStop(profile, domain, actionID, runID); err != nil {
		t.Fatal(err)
	}
	requested, err := StopRequested(profile, domain, actionID, runID)
	if err != nil || !requested {
		t.Fatalf("stop request = %v, %v", requested, err)
	}
	if err := FinishRun(profile, domain, actionID, runID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadRun(profile, domain, actionID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected run record removed, got %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(paths.DomainDir(profile, domain), "actions", "runtime"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("runtime artifacts remain: %v, %v", entries, err)
	}
}

func TestReadForProfileMarksSharedRuntimeAsRunning(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	profile, domain := "test", "test__folder"
	if err := paths.EnsureDomainDir(profile, domain); err != nil {
		t.Fatal(err)
	}
	actionDirectory := filepath.Join(paths.DomainDir(profile, domain), "actions")
	if err := os.MkdirAll(actionDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	name := "Build"
	idBytes := sha256.Sum256([]byte("build"))
	actionID := hex.EncodeToString(idBytes[:])
	actionData, err := json.Marshal(Action{ID: actionID, Name: name, Command: "make", CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(actionDirectory, actionID+".json"), actionData, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimRun(profile, domain, actionID, "0123456789abcdef0123456789abcdef", "pi"); err != nil {
		t.Fatal(err)
	}
	actions, err := ReadForProfile(profile, domain)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 || !actions[0].Running {
		t.Fatalf("shared runtime not reflected in action list: %+v", actions)
	}
}

func TestActionRuntimeRejectsForeignRunAndSymlinkRecord(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	profile, domain := "test", "test__folder"
	actionID := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	runID := "0123456789abcdef0123456789abcdef"
	if err := paths.EnsureDomainDir(profile, domain); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(paths.DomainDir(profile, domain), "actions"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimRun(profile, domain, actionID, runID, "pi"); err != nil {
		t.Fatal(err)
	}
	if err := RequestStop(profile, domain, actionID, "fedcba9876543210fedcba9876543210"); err == nil {
		t.Fatal("expected wrong run ID rejection")
	}

	runtimeDir := filepath.Join(paths.DomainDir(profile, domain), "actions", "runtime")
	if err := os.Remove(filepath.Join(runtimeDir, actionID+".json")); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(target, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(runtimeDir, actionID+".json")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadRun(profile, domain, actionID); err == nil {
		t.Fatal("expected symlink rejection")
	}
	var parsed map[string]any
	data, err := os.ReadFile(target)
	if err != nil || json.Unmarshal(data, &parsed) != nil || parsed["version"] != float64(1) {
		t.Fatalf("outside target changed: %s (%v)", data, err)
	}
}
