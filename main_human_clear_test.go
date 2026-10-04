package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HumanHorizon/automata/internal/ai-knowledge/human"
	"github.com/HumanHorizon/automata/internal/tree"
	"github.com/Starframe/portalis"
)

func writeHumanProjectionMarker(t *testing.T, profile, sessionID string) string {
	t.Helper()
	directory := human.Directory(profile, sessionID)
	if directory == "" {
		t.Fatal("invalid projection fixture identity")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(directory, "history.json")
	if err := os.WriteFile(marker, []byte("projection-before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return marker
}

func TestHumanSuccessfulClearRemovesProjectionAfterRuntimeStopBeforeRestart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AI_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("PI_CMD", "")
	installFakePi(t)
	const profile, owner = "human-clear", "human-clear__chat"
	familiar := owner + "__worker"
	ownerMarker := writeHumanProjectionMarker(t, profile, owner)
	familiarMarker := writeHumanProjectionMarker(t, profile, familiar)
	foreignMarker := writeHumanProjectionMarker(t, "other-profile", owner)
	old := portalis.NewEmulator(owner, "Main", "/bin/sh", nil)
	app := &App{
		profile: profile, tree: tree.New(), piAgentDir: filepath.Join(home, ".ai", "just", "pi"),
		activeSessions: map[string]struct{}{owner: {}}, emulatorCache: map[string]*portalis.Emulator{owner: old},
	}
	app.prepareJobSessionFn = func(string, string) error { return nil }
	app.killSessionFn = func(string, string) error { return nil }
	started := false
	app.startEmulatorSyncFn = func(*portalis.Emulator, []string) error {
		started = true
		if app.emulatorCache[owner] == old {
			t.Fatal("Human cleanup/restart ran before old runtime was removed")
		}
		for _, marker := range []string{ownerMarker, familiarMarker} {
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("restart saw stale projection %q: %v", marker, err)
			}
		}
		return nil
	}
	jobs := app.clearSessionCmd(owner, "", []string{familiar})().(clearSessionJobsCompletedMsg)
	if jobs.err != nil {
		t.Fatal(jobs.err)
	}
	if _, err := os.Stat(ownerMarker); err != nil {
		t.Fatal("projection was removed while the old exporter could still be running")
	}
	_, restart := app.Update(jobs)
	if restart == nil {
		t.Fatal("successful Clear did not schedule restart")
	}
	result := restart().(clearSessionRestartCompletedMsg)
	if result.err != nil || !started {
		t.Fatalf("Clear restart failed: %+v", result)
	}
	app.Update(result)
	if _, err := os.Stat(foreignMarker); err != nil {
		t.Fatal("Clear touched another profile")
	}
}

func TestHumanClearPreflightAndStopFailuresPreserveProjectionAndHistory(t *testing.T) {
	for _, stage := range []string{"preflight", "stop"} {
		t.Run(stage, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("AI_DATA_HOME", filepath.Join(home, "data"))
			const profile, owner = "human-clear", "human-clear__chat"
			marker := writeHumanProjectionMarker(t, profile, owner)
			history := writeJSONLFixture(t, home, "/workspace", owner, time.Now())
			before, err := os.ReadFile(history)
			if err != nil {
				t.Fatal(err)
			}
			failure := errors.New("injected " + stage + " failure")
			app := &App{profile: profile, piAgentDir: filepath.Join(home, ".ai", "just", "pi")}
			app.prepareJobSessionFn = func(string, string) error {
				if stage == "preflight" {
					return failure
				}
				return nil
			}
			app.killSessionFn = func(string, string) error { return failure }
			app.startEmulatorSyncFn = func(*portalis.Emulator, []string) error {
				t.Fatal("failed Clear restarted Pi")
				return nil
			}
			jobs := app.clearSessionCmd(owner, "/workspace", nil)().(clearSessionJobsCompletedMsg)
			if !errors.Is(jobs.err, failure) {
				t.Fatalf("Clear failure = %v", jobs.err)
			}
			app.Update(jobs)
			if got, err := os.ReadFile(marker); err != nil || string(got) != "projection-before\n" {
				t.Fatalf("failed Clear changed projection: %q, %v", got, err)
			}
			if got, err := os.ReadFile(history); err != nil || string(got) != string(before) {
				t.Fatalf("failed Clear changed original history: %q, %v", got, err)
			}
		})
	}
}

func TestHumanClearRejectsForeignSessionBeforeDeletingAnyProjection(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const profile, owner = "human-clear", "human-clear__chat"
	worker := owner + "__worker"
	markers := []string{writeHumanProjectionMarker(t, profile, owner), writeHumanProjectionMarker(t, profile, worker)}
	if err := clearHumanProjections(profile, owner, []string{owner, worker, "other-owner"}); err == nil {
		t.Fatal("foreign session was accepted by Human Clear")
	}
	for _, marker := range markers {
		if _, err := os.Stat(marker); err != nil {
			t.Fatal("identity validation performed a partial Clear")
		}
	}
}
