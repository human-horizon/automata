package ui

import (
	"strings"
	"testing"

	"github.com/HumanHorizon/automata/internal/ai-knowledge/context"
	"github.com/HumanHorizon/automata/internal/ai-knowledge/jobs"
	apptheme "github.com/HumanHorizon/automata/internal/theme"
	"github.com/charmbracelet/lipgloss"
)

func TestKnowledgeTextStylesAvoidPinkAndPurple(t *testing.T) {
	palette := apptheme.Default()
	forbidden := []string{palette.Pink, palette.PinkMuted, palette.Purple, palette.PurpleMuted}
	styles := newRenderStyles(palette)
	items := []struct {
		name  string
		style lipgloss.Style
	}{
		{"title", styles.title},
		{"section", styles.section},
		{"item", styles.item},
		{"done", styles.done},
		{"empty", styles.empty},
		{"path", styles.path},
	}

	for _, item := range items {
		foreground, ok := item.style.GetForeground().(lipgloss.Color)
		if !ok {
			continue
		}
		for _, color := range forbidden {
			if string(foreground) == color {
				t.Fatalf("%s uses accent text color %q", item.name, color)
			}
		}
	}
}

func TestActionIcon(t *testing.T) {
	cases := []struct {
		action, desc, want string
	}{
		{"thinking", "", "● thinking"},
		{"read", "/tmp/foo.go", "● read"},
		{"write", "/tmp/foo.go", "● write"},
		{"grep", "TODO", "● grep"},
		{"find", "name=foo", "● find"},
		{"analyze", "x", "● analyze"},
		{"wait", "x", "● wait"},
		{"job", "build", "● job"},
		{"run", "go test", "● run"},
		{"status", "x", "● status"},
		{"active", "x", "● active"},
		{"stop", "", "● stopped"},
		{"idle", "", ""},
		{"", "", ""},
		{"unknown", "ignored", "● unknown"},
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

// TestViewStatusReadPath keeps the existing read+description contract: a
// file path must surface under the bullet. Used as the positive baseline
// for the "only read/write show the file" rule below.
func TestViewStatusReadPath(t *testing.T) {
	ctx := &context.Data{Status: &context.Status{Action: "read", Description: "/Users/a/foo.go"}}
	out := View(60, 15, ctx, nil, "")
	if !strings.Contains(out, "foo.go") {
		t.Fatalf("expected path, got:\n%s", out)
	}
}

// TestViewStatusReadShowsPath is the positive half of the rule — read keeps
// the description visible.
func TestViewStatusReadShowsPath(t *testing.T) {
	ctx := &context.Data{Status: &context.Status{Action: "read", Description: "/Users/a/foo.go"}}
	out := View(60, 15, ctx, nil, "")
	if !strings.Contains(out, "foo.go") {
		t.Fatalf("expected path for read, got:\n%s", out)
	}
}

// TestViewStatusWriteShowsPath is the second positive case — write also
// surfaces the description (typically the destination file).
func TestViewStatusWriteShowsPath(t *testing.T) {
	ctx := &context.Data{Status: &context.Status{Action: "write", Description: "/Users/a/out.go"}}
	out := View(60, 15, ctx, nil, "")
	if !strings.Contains(out, "out.go") {
		t.Fatalf("expected path for write, got:\n%s", out)
	}
}

// TestViewStatusThinkingOmitsDescription proves thinking does NOT surface
// the description line, even if a stale one is present on disk.
func TestViewStatusThinkingOmitsDescription(t *testing.T) {
	ctx := &context.Data{Status: &context.Status{Action: "thinking", Description: "/Users/a/stale.go"}}
	out := View(60, 15, ctx, nil, "")
	if strings.Contains(out, "stale.go") {
		t.Fatalf("did not expect description for action=thinking, got:\n%s", out)
	}
}

// TestViewStatusIdleOmitsDescription ensures that when the action is idle,
// any leftover description from a previous substatus is NOT rendered as a
// separate line — idle in the right panel means "nothing to show", and the
// description would only confuse the user.
func TestViewStatusIdleOmitsDescription(t *testing.T) {
	ctx := &context.Data{Status: &context.Status{Action: "idle", Description: "/Users/a/stale.go"}}
	out := View(60, 15, ctx, nil, "")
	if strings.Contains(out, "stale.go") {
		t.Fatalf("did not expect stale description for action=idle, got:\n%s", out)
	}
}

// TestViewStatusRunShowsCommand is the third positive case — run surfaces
// the command being executed (the description field), so the user can see
// "● run" plus the actual command line.
func TestViewStatusRunShowsCommand(t *testing.T) {
	ctx := &context.Data{Status: &context.Status{Action: "run", Description: "go test ./..."}}
	out := View(60, 15, ctx, nil, "")
	if !strings.Contains(out, "go test") {
		t.Fatalf("expected command for run, got:\n%s", out)
	}
}

// TestViewStatusOtherActionsOmitDescription covers the rest of the
// substatus family (find, grep, job, analyze, wait, status, active,
// stop) — the file path should be hidden for all of them. We spot-check a
// couple to keep the test cheap.
func TestViewStatusOtherActionsOmitDescription(t *testing.T) {
	for _, action := range []string{"find", "grep", "job", "analyze", "stop"} {
		t.Run(action, func(t *testing.T) {
			ctx := &context.Data{Status: &context.Status{Action: action, Description: "/Users/a/x.go"}}
			out := View(60, 15, ctx, nil, "")
			if strings.Contains(out, "x.go") {
				t.Errorf("did not expect description for action=%s, got:\n%s", action, out)
			}
		})
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

func TestContentWithThemeKeepsAllLinesAndCollapsesSections(t *testing.T) {
	steps := make([]context.PlanStep, 12)
	for index := range steps {
		steps[index] = context.PlanStep{Text: "step"}
	}
	ctx := &context.Data{Plans: map[string][]context.PlanStep{"Build": steps}}
	jobsList := []jobs.Job{{ID: "job", Command: "run command"}}

	content := ContentWithTheme(80, ctx, jobsList, "", apptheme.Default(), CollapseState{})
	if got := len(strings.Split(content, "\n")); got <= 5 {
		t.Fatalf("content was clipped before scrolling: got %d lines", got)
	}
	collapsed := ContentWithTheme(80, ctx, jobsList, "", apptheme.Default(), CollapseState{Plans: true, Jobs: true})
	if strings.Contains(collapsed, "step") || strings.Contains(collapsed, "run command") {
		t.Fatalf("collapsed content still contains section entries: %s", collapsed)
	}
	for _, want := range []string{"Plans", "Jobs", "▸"} {
		if !strings.Contains(collapsed, want) {
			t.Errorf("collapsed section header missing %q: %s", want, collapsed)
		}
	}
}

func TestWrapPrefixedHandlesWideRuneInNarrowViewport(t *testing.T) {
	lines := wrapPrefixed("x ", "界", 1)
	if len(lines) == 0 || !strings.Contains(strings.Join(lines, ""), "界") {
		t.Fatalf("wide rune was lost in narrow viewport: %#v", lines)
	}
}

func TestWrapPrefixedHangsUnderItemTextWithinTerminalWidth(t *testing.T) {
	const width = 18
	lines := wrapPrefixed("    [ ] ", "αβγδ epsilon zeta", width)
	if len(lines) < 2 {
		t.Fatalf("expected wrapped lines, got %q", lines)
	}
	for index, line := range lines {
		if got := lipgloss.Width(line); got > width {
			t.Errorf("line %d width = %d, limit %d: %q", index, got, width, line)
		}
		if index > 0 && !strings.HasPrefix(line, "        ") {
			t.Errorf("continuation does not align under step text: %q", line)
		}
	}
}
