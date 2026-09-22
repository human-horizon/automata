package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HumanHorizon/automata/internal/paths"
)

func writeNotesFixture(t *testing.T, _ string, profile, domain, content string) {
	t.Helper()
	domainPath := paths.DomainDir(profile, domain)
	if err := os.MkdirAll(domainPath, 0o755); err != nil {
		t.Fatalf("mkdir domain: %v", err)
	}
	if err := os.WriteFile(filepath.Join(domainPath, "notes.json"), []byte(content), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}
}

func TestWriteNotesPreservesSectionsAndInvalidatesCache(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	profile := "write-profile"
	domain := "write-domain"
	first := []NoteSummary{{
		Title:    "Project",
		Sections: []NoteSection{{Title: "Decision", Content: "Keep this"}},
	}}

	if err := Write(profile, domain, first); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(paths.DomainDir(profile, domain), "notes.json"))
	if err != nil {
		t.Fatalf("read written notes: %v", err)
	}
	if !strings.HasSuffix(string(raw), "\n") {
		t.Fatalf("written notes should end with newline, got %q", raw)
	}

	reader := NewCachedReader()
	data, err := reader.Read(profile, domain)
	if err != nil {
		t.Fatalf("read first notes: %v", err)
	}
	if got := data.Notes[0].Sections[0].Content; got != "Keep this" {
		t.Fatalf("first section content = %q, want Keep this", got)
	}

	second := []NoteSummary{{Title: "Updated", Sections: []NoteSection{{Content: "New content"}}}}
	if err := Write(profile, domain, second); err != nil {
		t.Fatalf("Write replacement returned error: %v", err)
	}
	reader.Invalidate(profile, domain)
	data, err = reader.Read(profile, domain)
	if err != nil {
		t.Fatalf("read replacement notes: %v", err)
	}
	if len(data.Notes) != 1 || data.Notes[0].Title != "Updated" {
		t.Fatalf("replacement notes = %+v", data.Notes)
	}
}

func TestReadSectionsPreservesOrderAndMarkdown(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	writeNotesFixture(t, dataHome, "profile", "domain", `[
		{"title":"Project","sections":[
			{"title":"Decision","content":"**Use sections**"},
			{"title":"Code","content":"\u0060\u0060\u0060go\nfmt.Println(\"ok\")\n\u0060\u0060\u0060"}
		]}
	]`)

	data, err := Read("profile", "domain")
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if len(data.Notes) != 1 {
		t.Fatalf("expected one note, got %d", len(data.Notes))
	}
	if len(data.Notes[0].Sections) != 2 {
		t.Fatalf("expected two sections, got %d", len(data.Notes[0].Sections))
	}
	if got := data.Notes[0].Sections[0].Title; got != "Decision" {
		t.Fatalf("first section title = %q, want Decision", got)
	}
	if got := data.Notes[0].Sections[1].Content; got != "```go\nfmt.Println(\"ok\")\n```" {
		t.Fatalf("second section content = %q", got)
	}
}

func TestReadLegacyNotesCreatesMarkdownSection(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	writeNotesFixture(t, dataHome, "profile", "domain", `[
		{"title":"Legacy","notes":["first note","second note"]}
	]`)

	data, err := Read("profile", "domain")
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if len(data.Notes) != 1 || len(data.Notes[0].Sections) != 1 {
		t.Fatalf("expected one normalized legacy section, got %+v", data.Notes)
	}
	if got := data.Notes[0].Sections[0].Content; got != "- first note\n- second note" {
		t.Fatalf("legacy section content = %q", got)
	}
	if len(data.Notes[0].Notes) != 2 {
		t.Fatalf("legacy notes were not preserved, got %d", len(data.Notes[0].Notes))
	}
}

func TestCachedReaderReadsSections(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	writeNotesFixture(t, dataHome, "profile", "domain", `[
		{"title":"Cached","sections":[{"title":"One","content":"Content"}]}
	]`)

	reader := NewCachedReader()
	data, err := reader.Read("profile", "domain")
	if err != nil {
		t.Fatalf("first Read returned error: %v", err)
	}
	cached, err := reader.Read("profile", "domain")
	if err != nil {
		t.Fatalf("cached Read returned error: %v", err)
	}
	if data != cached {
		t.Fatalf("expected unchanged notes to use cached data")
	}
	if got := cached.Notes[0].Sections[0].Content; got != "Content" {
		t.Fatalf("cached section content = %q", got)
	}
}

func TestReadUsesCanonicalUnicodeProfileAndIgnoresLegacyPath(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	t.Setenv("AI_PROFILE", "environment-profile")
	const profile = "Проект Ω"
	const domain = "memory"

	writeNotesFixture(t, dataHome, profile, domain, `[{"title":"Canonical"}]`)

	legacyDir := filepath.Join(dataHome, "profiles", profile, "domains", domain)
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatalf("mkdir legacy domain: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "notes.json"), []byte(`[{"title":"Legacy"}]`), 0o644); err != nil {
		t.Fatalf("write legacy notes: %v", err)
	}

	data, err := Read(profile, domain)
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if len(data.Notes) != 1 || data.Notes[0].Title != "Canonical" {
		t.Fatalf("expected canonical notes, got %+v", data.Notes)
	}

	cached, err := NewCachedReader().Read(profile, domain)
	if err != nil {
		t.Fatalf("CachedReader.Read returned error: %v", err)
	}
	if len(cached.Notes) != 1 || cached.Notes[0].Title != "Canonical" {
		t.Fatalf("expected CachedReader to return canonical notes, got %+v", cached.Notes)
	}

	wantDir := filepath.Join(dataHome, "profiles", paths.ProfileSlug(profile), "domains", domain)
	if got := paths.DomainDir(profile, domain); got != wantDir {
		t.Fatalf("canonical domain path = %q, want %q", got, wantDir)
	}
}

func TestReadEmptyProfileUsesAIProfileOrDefault(t *testing.T) {
	tests := []struct {
		name           string
		aiProfile      string
		fixtureProfile string
		title          string
	}{
		{name: "AI_PROFILE", aiProfile: "configured-profile", fixtureProfile: "configured-profile", title: "From environment"},
		{name: "default", fixtureProfile: "", title: "From default"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dataHome := t.TempDir()
			t.Setenv("AI_DATA_HOME", dataHome)
			t.Setenv("AI_PROFILE", tt.aiProfile)
			const domain = "memory"
			writeNotesFixture(t, dataHome, tt.fixtureProfile, domain, `[{"title":"`+tt.title+`"}]`)

			data, err := Read("", domain)
			if err != nil {
				t.Fatalf("Read returned error: %v", err)
			}
			if len(data.Notes) != 1 || data.Notes[0].Title != tt.title {
				t.Fatalf("expected %q notes, got %+v", tt.title, data.Notes)
			}
		})
	}
}
