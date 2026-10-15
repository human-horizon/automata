package memory

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HumanHorizon/automata/internal/paths"
)

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
