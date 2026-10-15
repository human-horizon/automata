package notesactualize

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/HumanHorizon/automata/internal/ai-knowledge/memory"
	"github.com/HumanHorizon/automata/internal/paths"
)

func newTestService(t *testing.T, runner ModelRunner) *Service {
	t.Helper()
	return NewService(runner)
}

type sequenceRunner struct {
	responses []string
	prompts   []string
	options   []RunOptions
	onRun     func(int) error
}

func (r *sequenceRunner) Run(_ context.Context, prompt string, options RunOptions) (string, error) {
	index := len(r.prompts)
	r.prompts = append(r.prompts, prompt)
	r.options = append(r.options, options)
	if r.onRun != nil {
		if err := r.onRun(index); err != nil {
			return "", err
		}
	}
	if index >= len(r.responses) {
		return "", fmt.Errorf("unexpected model call %d", index+1)
	}
	return r.responses[index], nil
}

func TestActualizeConsolidatesOnceAndPreservesProjectScopedOutput(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const profile, domain = "actualize-profile", "actualize-profile__repo"
	initial := []memory.NoteSummary{{Title: "History", Notes: []string{"Old commit", "Current API is stable"}}}
	if err := memory.Write(profile, domain, initial); err != nil {
		t.Fatal(err)
	}
	before, err := memory.Capture(profile, domain)
	if err != nil {
		t.Fatal(err)
	}
	runner := &sequenceRunner{responses: []string{
		`[{"title":"API contract","sections":[{"title":"Current","content":"Current API is stable"}]}]`,
	}}
	service := newTestService(t, runner)
	options := RunOptions{Model: "provider/model", ThinkingLevel: "xhigh"}
	result, err := service.ActualizeWithOptions(context.Background(), profile, domain, options)
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.options) != 1 || runner.options[0] != options {
		t.Fatalf("runner options = %#v, want one call with selected options", runner.options)
	}
	if !result.Committed || result.BeforeCount != 1 || result.AfterCount != 1 || result.UndoID == "" {
		t.Fatalf("actualize result = %+v", result)
	}
	if len(runner.prompts) != 1 || !strings.Contains(runner.prompts[0], "Old commit") || !strings.Contains(runner.prompts[0], "Current API is stable") {
		t.Fatalf("actualize did not send all source notes in exactly one prompt: %#v", runner.prompts)
	}
	for _, requirement := range []string{"decisions and reasons", "constraints", "validation results", "unfinished tasks", "Do not invent facts"} {
		if !strings.Contains(runner.prompts[0], requirement) {
			t.Errorf("consolidation prompt omitted preservation requirement %q", requirement)
		}
	}
	after, err := memory.Capture(profile, domain)
	if err != nil {
		t.Fatal(err)
	}
	if after.Fingerprint == before.Fingerprint || after.Notes[0].Title != "API contract" {
		t.Fatalf("notes were not replaced with validated result: %+v", after)
	}
}

func TestActualizeConsolidatesEntireInputInOneModelCall(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const profile, domain = "group-profile", "group-profile__repo"
	notes := []memory.NoteSummary{
		{Title: "A", Sections: []memory.NoteSection{{Content: strings.Repeat("alpha ", 20)}}},
		{Title: "B", Sections: []memory.NoteSection{{Content: strings.Repeat("beta ", 20)}}},
		{Title: "C", Sections: []memory.NoteSection{{Content: strings.Repeat("gamma ", 20)}}},
	}
	if err := memory.Write(profile, domain, notes); err != nil {
		t.Fatal(err)
	}
	runner := &sequenceRunner{responses: []string{`[{"title":"Combined","sections":[{"content":"alpha, beta and gamma"}]}]`}}
	service := newTestService(t, runner)
	service.MaxSourceBytes = 4096
	service.MaxNoteBytes = 4096
	service.MaxResultBytes = 8192
	result, err := service.Actualize(context.Background(), profile, domain)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Committed || len(runner.prompts) != 1 {
		t.Fatalf("expected a single consolidation request; result=%+v calls=%d", result, len(runner.prompts))
	}
	for _, title := range []string{"A", "B", "C"} {
		if !strings.Contains(runner.prompts[0], `"title":"`+title+`"`) {
			t.Fatalf("single consolidation prompt omitted source %q: %s", title, runner.prompts[0])
		}
	}
}

func TestActualizeIsIdempotentForAlreadyConsolidatedNotes(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const profile, domain = "idempotent-profile", "idempotent-profile__repo"
	if err := memory.Write(profile, domain, []memory.NoteSummary{{Title: "Old", Notes: []string{"duplicate"}}, {Title: "Current", Notes: []string{"keep"}}}); err != nil {
		t.Fatal(err)
	}
	canonical := `[{"title":"Project","sections":[{"title":"Current","content":"keep"}]}]`
	runner := &sequenceRunner{responses: []string{canonical, canonical}}
	service := newTestService(t, runner)
	if _, err := service.Actualize(context.Background(), profile, domain); err != nil {
		t.Fatal(err)
	}
	first, err := memory.Capture(profile, domain)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Actualize(context.Background(), profile, domain); err != nil {
		t.Fatal(err)
	}
	second, err := memory.Capture(profile, domain)
	if err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint != second.Fingerprint || len(second.Notes) != 1 {
		t.Fatalf("repeat actualization changed consolidated data: first=%+v second=%+v", first, second)
	}
}

func TestActualizeRejectsInputOverSingleRequestLimitBeforeModelCall(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const profile, domain = "oversized-input-profile", "oversized-input-profile__repo"
	original := []memory.NoteSummary{{Title: "Keep", Notes: []string{"unchanged"}}}
	if err := memory.Write(profile, domain, original); err != nil {
		t.Fatal(err)
	}
	before, err := memory.Capture(profile, domain)
	if err != nil {
		t.Fatal(err)
	}
	runner := &sequenceRunner{}
	service := newTestService(t, runner)
	service.MaxSourceBytes = 1
	if _, err := service.Actualize(context.Background(), profile, domain); err == nil || !strings.Contains(err.Error(), "single-request limit") {
		t.Fatalf("expected source limit error, got %v", err)
	}
	if len(runner.prompts) != 0 {
		t.Fatalf("oversized input reached the model: %d calls", len(runner.prompts))
	}
	after, err := memory.Capture(profile, domain)
	if err != nil || after.Fingerprint != before.Fingerprint {
		t.Fatalf("oversize rejection changed source Notes: %+v, %v", after, err)
	}
}

func TestActualizeRejectsMalformedOutputWithoutChangingNotes(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const profile, domain = "invalid-output-profile", "invalid-output-profile__repo"
	if err := memory.Write(profile, domain, []memory.NoteSummary{{Title: "Keep", Notes: []string{"unchanged"}}}); err != nil {
		t.Fatal(err)
	}
	before, err := memory.Capture(profile, domain)
	if err != nil {
		t.Fatal(err)
	}
	service := newTestService(t, &sequenceRunner{responses: []string{"```json\n[]\n```"}})
	if _, err := service.Actualize(context.Background(), profile, domain); err == nil {
		t.Fatal("invalid model response was accepted")
	}
	after, err := memory.Capture(profile, domain)
	if err != nil || after.Fingerprint != before.Fingerprint {
		t.Fatalf("invalid response changed notes: %+v, error %v", after, err)
	}
}

func TestActualizeCommitsSchemaValidOutputWithoutSemanticReview(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const profile, domain = "audit-profile", "audit-profile__repo"
	if err := memory.Write(profile, domain, []memory.NoteSummary{{Title: "Source", Notes: []string{"keep the active blocker"}}}); err != nil {
		t.Fatal(err)
	}
	before, err := memory.Capture(profile, domain)
	if err != nil {
		t.Fatal(err)
	}
	runner := &sequenceRunner{responses: []string{
		`[{"title":"Source","sections":[{"content":"summary omitted a source detail"}]}]`,
	}}
	result, err := newTestService(t, runner).Actualize(context.Background(), profile, domain)
	if err != nil || !result.Committed || len(runner.prompts) != 1 {
		t.Fatalf("schema-valid output should commit after one model call: result=%+v calls=%d error=%v", result, len(runner.prompts), err)
	}
	after, err := memory.Capture(profile, domain)
	if err != nil || after.Fingerprint == before.Fingerprint || strings.Contains(after.Notes[0].Sections[0].Content, "active blocker") {
		t.Fatalf("schema-valid summary was not committed as specified: %+v, error %v", after, err)
	}
}

func TestActualizeCancellationAfterConsolidationLeavesNotesAndBackupsUntouched(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const profile, domain = "cancel-profile", "cancel-profile__repo"
	if err := memory.Write(profile, domain, []memory.NoteSummary{{Title: "Source", Notes: []string{"keep"}}}); err != nil {
		t.Fatal(err)
	}
	before, err := memory.Capture(profile, domain)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := &sequenceRunner{
		responses: []string{`[{"title":"Result","sections":[{"content":"keep"}]}]`},
		onRun: func(index int) error {
			if index == 0 {
				cancel()
			}
			return nil
		},
	}
	if _, err := newTestService(t, runner).Actualize(ctx, profile, domain); err == nil {
		t.Fatal("canceled actualization committed")
	}
	after, err := memory.Capture(profile, domain)
	if err != nil || after.Fingerprint != before.Fingerprint {
		t.Fatalf("canceled actualization changed notes: %+v, error %v", after, err)
	}
	if _, err := os.Stat(filepath.Join(paths.DomainDir(profile, domain), backupDirectoryName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled actualization left a backup: %v", err)
	}
}

func TestActualizeConflictAfterAILeavesConcurrentEditUntouched(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const profile, domain = "conflict-profile", "conflict-profile__repo"
	if err := memory.Write(profile, domain, []memory.NoteSummary{{Title: "Original", Notes: []string{"old"}}}); err != nil {
		t.Fatal(err)
	}
	runner := &sequenceRunner{
		responses: []string{`[{"title":"Stale","sections":[{"content":"old"}]}]`},
		onRun: func(index int) error {
			if index == 0 {
				return memory.Write(profile, domain, []memory.NoteSummary{{Title: "Concurrent", Notes: []string{"newer"}}})
			}
			return nil
		},
	}
	if _, err := newTestService(t, runner).Actualize(context.Background(), profile, domain); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("expected stale snapshot conflict, got %v", err)
	}
	current, err := memory.Capture(profile, domain)
	if err != nil || current.Notes[0].Title != "Concurrent" {
		t.Fatalf("concurrent edit was lost: %+v, error %v", current, err)
	}
	backupRoot := filepath.Join(paths.DomainDir(profile, domain), backupDirectoryName)
	if _, err := os.Stat(backupRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("conflict created an unnecessary backup: %v", err)
	}
}

func TestActualizeBackupIsPrivateUndoIsExactAndRetentionIsBounded(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const profile, domain = "undo-profile", "undo-profile__repo"
	original := []byte("[{\"title\": \"Original\", \"notes\": [\"legacy bytes\"]}]\n")
	dir := paths.DomainDir(profile, domain)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.json"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	var expectedUndo []byte
	for index := range 6 {
		if index == 5 {
			snapshot, err := memory.Capture(profile, domain)
			if err != nil {
				t.Fatal(err)
			}
			expectedUndo = append([]byte(nil), snapshot.Raw...)
		}
		runner := &sequenceRunner{responses: []string{`[{"title":"Current","sections":[{"content":"canonical"}]}]`}}
		result, err := newTestService(t, runner).Actualize(context.Background(), profile, domain)
		if err != nil || !result.Committed {
			t.Fatalf("actualize = %+v, error %v", result, err)
		}
	}
	store := NewBackupStore()
	ids, err := store.backupIDs(profile, domain)
	if err != nil || len(ids) != backupRetention {
		t.Fatalf("retained backups = %d, error %v", len(ids), err)
	}
	backupDir := filepath.Join(store.root(profile, domain), ids[0])
	info, err := os.Stat(backupDir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("backup directory mode = %v, error %v", info, err)
	}
	backupInfo, err := os.Stat(filepath.Join(backupDir, "original.json"))
	if err != nil || backupInfo.Mode().Perm() != 0o600 {
		t.Fatalf("backup file mode = %v, error %v", backupInfo, err)
	}
	if _, err := store.UndoLast(profile, domain); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(filepath.Join(dir, "notes.json"))
	if err != nil || string(restored) != string(expectedUndo) {
		t.Fatalf("Undo did not restore exact pre-actualization bytes: %q, error %v", restored, err)
	}
}

func TestUndoRefusesToOverwriteLaterNotesChanges(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const profile, domain = "undo-conflict-profile", "undo-conflict-profile__repo"
	if err := memory.Write(profile, domain, []memory.NoteSummary{{Title: "Original", Notes: []string{"source"}}}); err != nil {
		t.Fatal(err)
	}
	runner := &sequenceRunner{responses: []string{`[{"title":"Current","sections":[{"content":"canonical"}]}]`}}
	if _, err := newTestService(t, runner).Actualize(context.Background(), profile, domain); err != nil {
		t.Fatal(err)
	}
	if err := memory.Write(profile, domain, []memory.NoteSummary{{Title: "Manual edit", Notes: []string{"keep me"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewBackupStore().UndoLast(profile, domain); err == nil || !strings.Contains(err.Error(), "undo refused") {
		t.Fatalf("expected fail-closed undo, got %v", err)
	}
	current, err := memory.Capture(profile, domain)
	if err != nil || current.Notes[0].Title != "Manual edit" {
		t.Fatalf("manual edit was overwritten: %+v, error %v", current, err)
	}
}

func TestPiRunnerUsesRestrictedNoSessionCLIAndCanonicalEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX fake executable")
	}
	dir := t.TempDir()
	executable := filepath.Join(dir, "fake-pi")
	capture := filepath.Join(dir, "arguments.txt")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$PI_TEST_CAPTURE\"\nprintf '%s' '{\"ok\":true}'\nprintf '%s\\n' 'diagnostic stderr' >&2\n"
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	agentDir := filepath.Join(dir, "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	settings := `{"enabledModels":["provider/model"],"defaultModel":"provider/model","defaultThinkingLevel":"low"}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PI_TEST_CAPTURE", capture)
	t.Setenv("AI_PROFILE", "wrong")
	t.Setenv("AUTOMATA_PROFILE", "wrong")
	t.Setenv("PI_CODING_AGENT_DIR", "wrong")
	runner := PiRunner{Executable: executable, AgentDir: agentDir, Profile: "actual-profile", DataHome: filepath.Join(dir, "data")}
	response, err := runner.Run(context.Background(), "request prompt", RunOptions{Model: "provider/model", ThinkingLevel: "high"})
	if err != nil || response != `{"ok":true}` {
		t.Fatalf("PiRunner.Run() = %q, %v", response, err)
	}
	arguments, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--print", "--no-session", "--no-tools", "--no-mcp", "--no-extensions", "--no-context-files", "--no-skills", "--no-prompt-templates", "--no-approve", "--model", "provider/model", "--thinking", "high", "request prompt"} {
		if !strings.Contains(string(arguments), want) {
			t.Errorf("Pi CLI args missing %q: %s", want, arguments)
		}
	}
	if !strings.Contains(strings.Join(runnerEnvironment(os.Environ(), runner), "\n"), "AI_PROFILE=actual-profile") {
		t.Fatal("runner did not provide the canonical profile")
	}
}

func TestPiRunnerTimeoutAndCancelStopWrapperDescendants(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX fake executables")
	}

	for _, mode := range []string{"timeout", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			agentDir := filepath.Join(dir, "agent")
			if err := os.MkdirAll(agentDir, 0o700); err != nil {
				t.Fatal(err)
			}
			settings := `{"enabledModels":["provider/model"],"defaultModel":"provider/model","defaultThinkingLevel":"low"}`
			if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(settings), 0o600); err != nil {
				t.Fatal(err)
			}
			started := filepath.Join(dir, "child-started")
			finished := filepath.Join(dir, "child-finished")
			t.Setenv("PI_TEST_CHILD_STARTED", started)
			t.Setenv("PI_TEST_CHILD_FINISHED", finished)
			executable := filepath.Join(dir, "fake-just")
			script := `#!/bin/sh
/bin/sh -c 'trap "" TERM; : > "$PI_TEST_CHILD_STARTED"; start=$(date +%s); while [ $(( $(date +%s) - start )) -lt 4 ]; do sleep 0.1; done; : > "$PI_TEST_CHILD_FINISHED"' &
wait
`
			if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}

			timeout := time.Second
			if mode == "cancel" {
				timeout = 10 * time.Second
			}
			runner := PiRunner{Executable: executable, AgentDir: agentDir, Timeout: timeout}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			startedAt := time.Now()
			result := make(chan error, 1)
			go func() {
				_, err := runner.Run(ctx, "test prompt", RunOptions{Model: "provider/model", ThinkingLevel: "low"})
				result <- err
			}()
			waitForFile(t, started, 2*time.Second)
			if mode == "cancel" {
				cancel()
			}
			select {
			case err := <-result:
				if err == nil {
					t.Fatal("canceled runner returned success")
				}
				wantError := "just request canceled"
				if mode == "timeout" {
					wantError = "just request exceeded"
				}
				if !strings.Contains(err.Error(), wantError) {
					t.Fatalf("runner error = %v, want %q", err, wantError)
				}
			case <-time.After(6 * time.Second):
				t.Fatal("runner did not return after timeout/cancel")
			}
			if mode == "timeout" && time.Since(startedAt) < 50*time.Millisecond {
				t.Fatal("runner returned before its timeout")
			}
			if remaining := time.Until(startedAt.Add(4500 * time.Millisecond)); remaining > 0 {
				time.Sleep(remaining)
			}
			if _, err := os.Stat(finished); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("wrapper child survived timeout/cancel and completed its side effect: %v", err)
			}
		})
	}
}

func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", filepath.Base(path))
}

func TestResolveExecutableFindsJustCommand(t *testing.T) {
	var lookedUp []string
	got, err := ResolveExecutable(func(candidate string) (string, error) {
		lookedUp = append(lookedUp, candidate)
		if candidate == "just" {
			return "/tmp/just", nil
		}
		return "", os.ErrNotExist
	})
	if err != nil || got != "/tmp/just" || len(lookedUp) != 1 || lookedUp[0] != "just" {
		t.Fatalf("ResolveExecutable() = %q, %v, looked up %#v", got, err, lookedUp)
	}
}
