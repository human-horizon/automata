package tree

import (
	"strings"
	"testing"
)

// TestStatusBadgeFromMap drives the badge from a hand-built map. The Tree
// only stores/labels whatever SetStatusBadges is given; render.go is what
// actually places the marker on screen and is exercised by the render tests.
func TestStatusBadgeFromMap(t *testing.T) {
	tr := New()
	tr.Profile = "ai"
	tr.AddChat("build").AddChat("docs")

	// Resolve keys from the tree itself so the test never goes out of sync
	// with the SessionName encoding logic.
	keyBuild := tr.SessionKeyOf(tr.AllItems()[0])
	keyDocs := tr.SessionKeyOf(tr.AllItems()[1])
	tr.SetStatusBadges(map[string]string{
		keyBuild: "🧠",
		keyDocs:  "📖",
	})

	if got := tr.StatusBadge(tr.Folder("")); got != "" {
		t.Fatalf("nil-folder should have no badge, got %q", got)
	}
	items := tr.AllItems()
	if len(items) == 0 {
		t.Fatalf("AllItems empty")
	}
	var buildItem, docsItem *Item
	for _, it := range items {
		if it.IsFolder {
			continue
		}
		if it.Name == "build" {
			buildItem = it
		}
		if it.Name == "docs" {
			docsItem = it
		}
	}
	if buildItem == nil || docsItem == nil {
		t.Fatalf("expected both items, got %+v", items)
	}
	if got := tr.StatusBadge(buildItem); got != "🧠" {
		t.Errorf("build badge = %q, want 🧠", got)
	}
	if got := tr.StatusBadge(docsItem); got != "📖" {
		t.Errorf("docs badge = %q, want 📖", got)
	}
}

// TestStatusBadgeEmpty verifies that no badges means no emoji in render.
func TestStatusBadgeEmpty(t *testing.T) {
	tr := New()
	tr.AddChat("solo")
	solo := tr.AllItems()[0]
	if got := tr.StatusBadge(solo); got != "" {
		t.Errorf("expected empty badge, got %q", got)
	}
	out := tr.renderItemLine(solo, 80, false, false, branchInfo{}, 0)
	if strings.Contains(out, "🧠") || strings.Contains(out, "📖") {
		t.Fatalf("rendered an unexpected badge: %q", out)
	}
}

// TestStatusBadgeRendersInline confirms the marker appears between the chat
// name and any trailing action icons when SetStatusBadges is populated.
// The badge key is a single-glyph code (R, W, A, etc.) — see
// status.Emoji() — and the tree renders the full word ("read", "write",
// "analyze", ...).
func TestStatusBadgeRendersInline(t *testing.T) {
	tr := New()
	tr.AddChat("readme")
	solo := tr.AllItems()[0]
	tr.SetStatusBadges(map[string]string{tr.SessionKeyOf(solo): "R"})

	out := tr.renderItemLine(solo, 80, false, false, branchInfo{}, 0)
	if !strings.Contains(out, "● read") {
		t.Fatalf("expected ● read in rendered line, got %q", out)
	}
	if !strings.Contains(out, "readme") {
		t.Fatalf("expected chat name in rendered line, got %q", out)
	}
}

// TestRootAllItems exercises the new public helpers.
func TestRootAllItems(t *testing.T) {
	tr := New()
	tr.AddFolder("Project A")
	tr.AddChat("chat-1")
	a := tr.Folder("Project A")
	tr.AddChatToSelected("nested")
	if a == nil {
		t.Fatal("Project A folder missing")
	}

	root := tr.Root()
	if len(root) < 2 { // at least folder + chat-1
		t.Fatalf("Root(): got %d items, want >= 2", len(root))
	}
	all := tr.AllItems()
	if len(all) < 3 { // folder + nested chat + chat-1
		t.Fatalf("AllItems(): got %d items, want >= 3", len(all))
	}
}