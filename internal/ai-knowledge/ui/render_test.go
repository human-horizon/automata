package ui

import (
	"strings"
	"testing"

	"github.com/HumanHorizon/automata/internal/ai-knowledge/context"
	"github.com/HumanHorizon/automata/internal/ai-knowledge/jobs"
)

func TestActionIcon(t *testing.T) {
	cases := []struct {
		action, desc, want string
	}{
		{"thinking", "", "~ thinking"},
		{"read", "/tmp/foo.go", "R /tmp/foo.go"},
		{"write", "/tmp/foo.go", "W /tmp/foo.go"},
		{"grep", "TODO", "G TODO"},
		{"run", "go test", "> go test"},
		{"idle", "", ""},
		{"stop", "", "X "},
		{"unknown", "x", "unknown x"},
		{"", "", ""},
	}
	for _, c := range cases {
		got := actionIcon(c.action, c.desc)
		if got != c.want {
			t.Errorf("actionIcon(%q,%q) = %q, want %q", c.action, c.desc, got, c.want)
		}
	}
}

func TestViewStatusThinking(t *testing.T) {
	ctx := &context.Data{Status: &context.Status{Action: "thinking"}}
	out := View(40, 10, ctx, nil, "")
	if !strings.Contains(out, "thinking") {
		t.Fatalf("expected thinking, got:\n%s", out)
	}
}

func TestViewStatusReadPath(t *testing.T) {
	ctx := &context.Data{Status: &context.Status{Action: "read", Description: "/Users/a/foo.go"}}
	out := View(60, 15, ctx, nil, "")
	if !strings.Contains(out, "foo.go") {
		t.Fatalf("expected path, got:\n%s", out)
	}
}

func TestViewEmpty(t *testing.T) {
	out := View(40, 10, nil, nil, "")
	for _, want := range []string{"(no status)", "(no plans)", "(no running jobs)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestViewJobs(t *testing.T) {
	js := []jobs.Job{{ID: "j1", Command: "go test ./...", Running: true}}
	out := View(60, 15, nil, js, "")
	if !strings.Contains(out, "go test") {
		t.Fatalf("expected command, got:\n%s", out)
	}
	if !strings.Contains(out, "●") {
		t.Fatalf("expected ● mark, got:\n%s", out)
	}
}

func TestViewPlans(t *testing.T) {
	ctx := &context.Data{
		Plans: map[string][]context.PlanStep{
			"Build": {
				{Text: "Write tests", Done: true},
				{Text: "Ship it", Done: false},
			},
		},
	}
	out := View(60, 15, ctx, nil, "")
	if !strings.Contains(out, "Write tests") {
		t.Fatalf("missing done step, got:\n%s", out)
	}
	if !strings.Contains(out, "Ship it") {
		t.Fatalf("missing open step, got:\n%s", out)
	}
	if !strings.Contains(out, "[x]") {
		t.Fatalf("missing [x] for done, got:\n%s", out)
	}
	if !strings.Contains(out, "[ ]") {
		t.Fatalf("missing [ ] for open, got:\n%s", out)
	}
}

func TestViewHeightClamp(t *testing.T) {
	out := View(40, 3, nil, nil, "")
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
}
