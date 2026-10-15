package notesactualize

import (
	"context"
	"testing"

	"github.com/HumanHorizon/automata/internal/ai-knowledge/memory"
)

type blockingRunner struct {
	started   chan struct{}
	release   chan struct{}
	responses []string
	calls     int
}

func (r *blockingRunner) Run(ctx context.Context, _ string, _ RunOptions) (string, error) {
	index := r.calls
	r.calls++
	if index == 0 {
		close(r.started)
		select {
		case <-r.release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return r.responses[index], nil
}

func TestServicePreventsConcurrentActualizeForSameDomain(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	const profile, domain = "guard-profile", "guard-profile__repo"
	if err := memory.Write(profile, domain, []memory.NoteSummary{{Title: "Source", Notes: []string{"fact"}}}); err != nil {
		t.Fatal(err)
	}
	runner := &blockingRunner{
		started: make(chan struct{}), release: make(chan struct{}),
		responses: []string{`[{"title":"Result","sections":[{"content":"fact"}]}]`},
	}
	service := NewService(runner)
	firstResult := make(chan error, 1)
	go func() {
		_, err := service.Actualize(context.Background(), profile, domain)
		firstResult <- err
	}()
	<-runner.started
	if _, err := service.Actualize(context.Background(), profile, domain); err == nil {
		t.Fatal("second same-domain actualization was accepted")
	}
	close(runner.release)
	if err := <-firstResult; err != nil {
		t.Fatal(err)
	}
	if runner.calls != 1 {
		t.Fatalf("expected exactly one model call, got %d", runner.calls)
	}
}
