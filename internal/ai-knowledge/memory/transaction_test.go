package memory

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/HumanHorizon/automata/internal/paths"
)

func TestNotesFileLockSubprocessHelper(t *testing.T) {
	notesPath := os.Getenv("AUTOMATA_NOTES_PATH")
	markerPath := os.Getenv("AUTOMATA_NOTES_LOCK_MARKER")
	if notesPath == "" || markerPath == "" {
		return
	}
	if err := withNotesFileLock(notesPath, func() error {
		return os.WriteFile(markerPath, []byte("acquired"), 0o600)
	}); err != nil {
		t.Fatal(err)
	}
}

func TestGoNotesLockBlocksPiProcess(t *testing.T) {
	piLockModule := os.Getenv("AUTOMATA_PI_FILE_LOCK_MODULE")
	if piLockModule == "" {
		t.Skip("set AUTOMATA_PI_FILE_LOCK_MODULE to run the Pi↔Go integration check")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is unavailable")
	}
	directory := t.TempDir()
	notesPath := filepath.Join(directory, "notes.json")
	lockPath := notesPath + ".lock"
	markerPath := filepath.Join(directory, "pi-acquired")
	scriptPath := filepath.Join(directory, "pi-lock.mjs")
	script := `import { writeFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
const { withFileLock } = await import(pathToFileURL(process.env.AUTOMATA_PI_FILE_LOCK_MODULE).href);
withFileLock(process.env.AUTOMATA_NOTES_LOCK_PATH, () => writeFileSync(process.env.AUTOMATA_NOTES_LOCK_MARKER, "acquired"));
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	var command *exec.Cmd
	err = withNotesFileLock(notesPath, func() error {
		command = exec.Command(node, "--experimental-strip-types", scriptPath)
		command.Env = append(os.Environ(),
			"AUTOMATA_PI_FILE_LOCK_MODULE="+piLockModule,
			"AUTOMATA_NOTES_LOCK_PATH="+lockPath,
			"AUTOMATA_NOTES_LOCK_MARKER="+markerPath,
		)
		if err := command.Start(); err != nil {
			return err
		}
		time.Sleep(150 * time.Millisecond)
		if _, err := os.Stat(markerPath); !errors.Is(err, os.ErrNotExist) {
			return errors.New("Pi process entered while the Go Notes lock was held")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(markerPath); err != nil || string(content) != "acquired" {
		t.Fatalf("Pi process failed to acquire released Go lock: content=%q err=%v", content, err)
	}
}

func TestPiNotesMutationConflictsWithActualizeAndUndo(t *testing.T) {
	piLockModule := os.Getenv("AUTOMATA_PI_FILE_LOCK_MODULE")
	if piLockModule == "" {
		t.Skip("set AUTOMATA_PI_FILE_LOCK_MODULE to run the Pi↔Go integration check")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is unavailable")
	}
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	const profile, domain = "pi-go-profile", "pi-go-profile__notes"
	if err := Write(profile, domain, []NoteSummary{{Title: "Initial"}}); err != nil {
		t.Fatal(err)
	}
	original, err := Capture(profile, domain)
	if err != nil {
		t.Fatal(err)
	}
	notesPath := notesPath(profile, domain)
	mutatePi := func(title string) {
		t.Helper()
		directory := t.TempDir()
		scriptPath := filepath.Join(directory, "pi-note-mutate.mjs")
		script := `import { readFileSync, writeFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
const { withFileLock } = await import(pathToFileURL(process.env.AUTOMATA_PI_FILE_LOCK_MODULE).href);
const path = process.env.AUTOMATA_NOTES_PATH;
withFileLock(path + ".lock", () => {
  const notes = JSON.parse(readFileSync(path, "utf8"));
  notes.push({ title: process.env.AUTOMATA_PI_NOTE_TITLE });
  writeFileSync(path, JSON.stringify(notes) + "\n");
});
`
		if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
			t.Fatal(err)
		}
		command := exec.Command(node, "--experimental-strip-types", scriptPath)
		command.Env = append(os.Environ(),
			"AUTOMATA_PI_FILE_LOCK_MODULE="+piLockModule,
			"AUTOMATA_NOTES_PATH="+notesPath,
			"AUTOMATA_PI_NOTE_TITLE="+title,
		)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("Pi note mutation failed: %v; output=%s", err, output)
		}
	}

	mutatePi("Pi during actualize")
	committed, err := WriteIfUnchanged(original, []NoteSummary{{Title: "Consolidated"}}, func([]byte, bool, string) (func() error, error) {
		return nil, nil
	})
	if err == nil || committed {
		t.Fatalf("actualize overwrote concurrent Pi note: committed=%v err=%v", committed, err)
	}
	current, err := Capture(profile, domain)
	if err != nil {
		t.Fatal(err)
	}
	if len(current.Notes) != 2 || current.Notes[1].Title != "Pi during actualize" {
		t.Fatalf("Pi mutation was lost during actualize: %+v", current.Notes)
	}

	committed, err = WriteIfUnchanged(current, []NoteSummary{{Title: "Actualized"}}, func([]byte, bool, string) (func() error, error) {
		return nil, nil
	})
	if err != nil || !committed {
		t.Fatalf("actualize commit failed: committed=%v err=%v", committed, err)
	}
	actualized, err := Capture(profile, domain)
	if err != nil {
		t.Fatal(err)
	}
	mutatePi("Pi during undo")
	restored, err := RestoreIfCurrent(profile, domain, actualized.Fingerprint, current)
	if err == nil || restored {
		t.Fatalf("undo overwrote concurrent Pi note: restored=%v err=%v", restored, err)
	}
	final, err := Capture(profile, domain)
	if err != nil {
		t.Fatal(err)
	}
	if len(final.Notes) != 2 || final.Notes[1].Title != "Pi during undo" {
		t.Fatalf("Pi mutation was lost during undo: %+v", final.Notes)
	}
}

func TestNotesFileLockSerializesIndependentProcess(t *testing.T) {
	notesPath := filepath.Join(t.TempDir(), "notes.json")
	markerPath := notesPath + ".marker"
	var command *exec.Cmd
	err := withNotesFileLock(notesPath, func() error {
		command = exec.Command(os.Args[0], "-test.run=^TestNotesFileLockSubprocessHelper$")
		command.Env = append(os.Environ(), "AUTOMATA_NOTES_PATH="+notesPath, "AUTOMATA_NOTES_LOCK_MARKER="+markerPath)
		if err := command.Start(); err != nil {
			return err
		}
		time.Sleep(100 * time.Millisecond)
		if _, err := os.Stat(markerPath); !errors.Is(err, os.ErrNotExist) {
			return errors.New("independent process entered while the Notes lock was held")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(markerPath); err != nil || string(content) != "acquired" {
		t.Fatalf("subprocess did not acquire lock after release: content=%q err=%v", content, err)
	}
}

func TestCapturePreservesExactSnapshotAndLegacyNotes(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const profile, domain = "snapshot-profile", "snapshot-domain"
	path := filepath.Join(paths.DomainDir(profile, domain), "notes.json")
	original := []byte("[{\"title\":\"Legacy\",\"notes\":[\"retain\"]}]\n")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}

	snapshot, err := Capture(profile, domain)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Exists || string(snapshot.Raw) != string(original) || snapshot.Fingerprint != Fingerprint(original, true) {
		t.Fatalf("captured snapshot = %+v", snapshot)
	}
	if len(snapshot.Notes) != 1 || len(snapshot.Notes[0].Sections) != 1 {
		t.Fatalf("legacy notes were not normalized: %+v", snapshot.Notes)
	}
}

func TestWriteIfUnchangedCreatesPreparedDataAndCommitsAtomically(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const profile, domain = "commit-profile", "commit-domain"
	if err := Write(profile, domain, []NoteSummary{{Title: "Original"}}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Capture(profile, domain)
	if err != nil {
		t.Fatal(err)
	}
	rollbackCalled := false
	committed, err := WriteIfUnchanged(snapshot, []NoteSummary{{Title: "Updated"}}, func(raw []byte, exists bool, resultFingerprint string) (func() error, error) {
		if !exists || string(raw) != string(snapshot.Raw) {
			t.Fatalf("prepare got exists=%v raw=%q", exists, raw)
		}
		if resultFingerprint != Fingerprint([]byte("[\n  {\n    \"title\": \"Updated\"\n  }\n]\n"), true) {
			t.Fatalf("unexpected result fingerprint: %q", resultFingerprint)
		}
		return func() error { rollbackCalled = true; return nil }, nil
	})
	if err != nil || !committed {
		t.Fatalf("WriteIfUnchanged() = committed %v, error %v", committed, err)
	}
	if rollbackCalled {
		t.Fatal("successful commit called rollback")
	}
	updated, err := Capture(profile, domain)
	if err != nil || len(updated.Notes) != 1 || updated.Notes[0].Title != "Updated" {
		t.Fatalf("updated snapshot = %+v, error %v", updated, err)
	}
}

func TestWriteIfUnchangedRejectsConcurrentFileChange(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const profile, domain = "conflict-profile", "conflict-domain"
	if err := Write(profile, domain, []NoteSummary{{Title: "Before"}}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Capture(profile, domain)
	if err != nil {
		t.Fatal(err)
	}
	if err := Write(profile, domain, []NoteSummary{{Title: "External update"}}); err != nil {
		t.Fatal(err)
	}
	prepareCalled := false
	committed, err := WriteIfUnchanged(snapshot, []NoteSummary{{Title: "Stale result"}}, func([]byte, bool, string) (func() error, error) {
		prepareCalled = true
		return nil, nil
	})
	if err == nil || committed || prepareCalled {
		t.Fatalf("conflict result = committed %v, error %v, prepare called %v", committed, err, prepareCalled)
	}
	current, err := Capture(profile, domain)
	if err != nil || current.Notes[0].Title != "External update" {
		t.Fatalf("concurrent notes were overwritten: %+v, error %v", current, err)
	}
}

func TestWriteIfUnchangedRollsBackPreparedDataOnWriteFailure(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const profile, domain = "rollback-profile", "rollback-domain"
	if err := Write(profile, domain, []NoteSummary{{Title: "Original"}}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Capture(profile, domain)
	if err != nil {
		t.Fatal(err)
	}
	rollbackCalled := false
	committed, err := WriteIfUnchanged(snapshot, []NoteSummary{{Title: "Updated"}}, func([]byte, bool, string) (func() error, error) {
		return func() error { rollbackCalled = true; return nil }, errors.New("backup failed")
	})
	if err == nil || committed || !rollbackCalled {
		t.Fatalf("prepare failure = committed %v, error %v, rollback %v", committed, err, rollbackCalled)
	}
	current, err := Capture(profile, domain)
	if err != nil || current.Fingerprint != snapshot.Fingerprint {
		t.Fatalf("prepare failure changed notes: %+v, error %v", current, err)
	}
}

func TestMemoryWriteAndConditionalWriteShareDomainLock(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const profile, domain = "lock-profile", "lock-domain"
	if err := Write(profile, domain, []NoteSummary{{Title: "Original"}}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Capture(profile, domain)
	if err != nil {
		t.Fatal(err)
	}
	prepared := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		_, err := WriteIfUnchanged(snapshot, []NoteSummary{{Title: "Actualized"}}, func([]byte, bool, string) (func() error, error) {
			close(prepared)
			<-release
			return nil, nil
		})
		finished <- err
	}()
	<-prepared
	writeDone := make(chan error, 1)
	go func() {
		writeDone <- Write(profile, domain, []NoteSummary{{Title: "Clipboard edit"}})
	}()
	select {
	case err := <-writeDone:
		t.Fatalf("concurrent in-process write did not wait for transaction: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	current, err := Capture(profile, domain)
	if err != nil || current.Notes[0].Title != "Clipboard edit" {
		t.Fatalf("serialized write result = %+v, error %v", current, err)
	}
}

func TestRestoreIfCurrentPreservesExactBytesAndRejectsStaleState(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const profile, domain = "restore-profile", "restore-domain"
	originalRaw := []byte("[{\"title\": \"Original\"}]\n")
	if err := Write(profile, domain, []NoteSummary{{Title: "Actualized"}}); err != nil {
		t.Fatal(err)
	}
	current, err := Capture(profile, domain)
	if err != nil {
		t.Fatal(err)
	}
	original := Snapshot{Profile: profile, Domain: domain, Raw: originalRaw, Exists: true, Fingerprint: Fingerprint(originalRaw, true)}
	committed, err := RestoreIfCurrent(profile, domain, current.Fingerprint, original)
	if err != nil || !committed {
		t.Fatalf("RestoreIfCurrent() = %v, %v", committed, err)
	}
	raw, err := os.ReadFile(filepath.Join(paths.DomainDir(profile, domain), "notes.json"))
	if err != nil || string(raw) != string(originalRaw) {
		t.Fatalf("restored raw = %q, error %v", raw, err)
	}
	if _, err := RestoreIfCurrent(profile, domain, current.Fingerprint, original); err == nil {
		t.Fatal("stale undo succeeded after restoration")
	}
}
