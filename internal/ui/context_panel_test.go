package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestContextPanelEmptyDomain verifies the placeholder for a missing domain.
func TestContextPanelEmptyDomain(t *testing.T) {
	cp := NewContextPanel("")
	cp.SetDomain("")
	out := cp.View(40, 10)
	if out == "" {
		t.Fatalf("expected non-empty placeholder view")
	}
	if !strings.Contains(out, "No domain context") {
		t.Fatalf("expected 'No domain context' placeholder, got:\n%s", out)
	}
}

// TestContextPanelNoNotes verifies the placeholder when domain has no notes.
func TestContextPanelNoNotes(t *testing.T) {
	cp := NewContextPanel("test-profile")
	cp.SetDomain("my-domain")
	out := cp.View(40, 10)
	if !strings.Contains(out, "No notes") {
		t.Fatalf("expected 'No notes' placeholder, got:\n%s", out)
	}
}

// TestContextPanelRendersNotes verifies domain notes are rendered.
func TestContextPanelRendersNotes(t *testing.T) {
	profile := "ctx-test-profile"
	domain := "ctx-test-domain"

	// Set up a temporary data home so we don't touch real notes.
	tmpDir := t.TempDir()
	os.Setenv("AI_DATA_HOME", tmpDir)
	os.Setenv("AI_PROFILE", profile)
	defer os.Unsetenv("AI_DATA_HOME")
	defer os.Unsetenv("AI_PROFILE")

	domainDir := filepath.Join(tmpDir, "profiles", "ctx-test-profile", "domains", domain)
	if err := os.MkdirAll(domainDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	notesPath := filepath.Join(domainDir, "notes.json")
	notes := `[{"title":"Project notes","notes":["first note","second note"]}]`
	if err := os.WriteFile(notesPath, []byte(notes), 0644); err != nil {
		t.Fatalf("write notes: %v", err)
	}

	cp := NewContextPanel(profile)
	cp.SetDomain(domain)
	out := cp.View(40, 20)
	if !strings.Contains(out, "Project notes") {
		t.Fatalf("expected note title, got:\n%s", out)
	}
	if !strings.Contains(out, "first note") {
		t.Fatalf("expected first note, got:\n%s", out)
	}
}

// TestContextPanelUpdateWindowSize verifies the panel stores dimensions.
func TestContextPanelUpdateWindowSize(t *testing.T) {
	cp := NewContextPanel("")
	cp.Update(tea.WindowSizeMsg{Width: 60, Height: 30})
	if cp.width != 60 || cp.height != 30 {
		t.Fatalf("size not stored: w=%d h=%d", cp.width, cp.height)
	}
}

func TestContextPanelScrollClamp(t *testing.T) {
	cp := NewContextPanel("")
	cp.SetDomain("test-domain")
	cp.width = 40
	cp.height = 5 // tab bar takes 1, so 4 body lines visible

	// Simulate scrolling way past the end.
	cp.scrollOffset = 1000
	_ = cp.View(40, 5)

	if cp.scrollOffset != 0 {
		// If there is no body content (empty domain), offset resets to 0.
		// If there IS content, offset should clamp to maxOffset = len(lines)-4.
		// Either way it must not be 1000.
		t.Errorf("scrollOffset not clamped: got %d", cp.scrollOffset)
	}
}
