package tree

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	apptheme "github.com/HumanHorizon/automata/internal/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestTreeTextStylesAvoidPinkAndPurple(t *testing.T) {
	palette := apptheme.Default()
	forbidden := []string{palette.Pink, palette.PinkMuted, palette.Purple, palette.PurpleMuted}
	styles := newTreeStyles(palette)
	items := []struct {
		name  string
		style lipgloss.Style
	}{
		{"header", styles.headerStyle},
		{"title", styles.titleStyle},
		{"collapse", styles.collapseStyle},
		{"folder", styles.folderStyle},
		{"chat", styles.chatStyle},
		{"archived", styles.archivedStyle},
		{"selected", styles.selectedStyle},
		{"status", styles.statusStyle},
		{"active", styles.activeStyle},
		{"idle", styles.idleStyle},
		{"branch", styles.branchStyle},
		{"empty", styles.emptyStyle},
		{"scrollbar", styles.scrollbarStyle},
		{"modalBorder", styles.modalBorderStyle},
		{"modalTitle", styles.modalTitleStyle},
		{"modalInput", styles.modalInputStyle},
		{"modalHint", styles.modalHintStyle},
		{"modalClose", styles.modalCloseStyle},
		{"dim", styles.dimStyle},
		{"actionIcon", styles.actionIconStyle},
		{"actionIconHover", styles.actionIconHoverStyle},
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

func TestSettingsButtonOpensThemeMenu(t *testing.T) {
	tree := New()
	tree.View(40, 24)

	tree.handleMouse(tea.MouseMsg{
		X:      8,
		Y:      23,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})

	if tree.popover == nil {
		t.Fatal("Settings click did not open a popover")
	}
	if len(tree.popover.Items) != len(apptheme.All()) {
		t.Fatalf("theme menu has %d items, want %d", len(tree.popover.Items), len(apptheme.All()))
	}
	if !strings.Contains(tree.popover.Items[0].Name, "Dinosaur Earth") {
		t.Fatalf("first theme menu item = %q", tree.popover.Items[0].Name)
	}
}

func TestSettingsButtonUsesApplicationCallbackWhenConfigured(t *testing.T) {
	tree := New()
	tree.View(40, 24)
	opened := false
	tree.SetOnOpenSettings(func() { opened = true })

	tree.handleMouse(tea.MouseMsg{
		X:      8,
		Y:      23,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})

	if !opened {
		t.Fatal("Settings callback was not invoked")
	}
	if tree.popover != nil {
		t.Fatal("Tree opened a local Settings popover despite application callback")
	}
}

func TestThemePersistsAndUnknownThemeFallsBack(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)

	original := New()
	original.Profile = "theme-test"
	if !original.SetTheme(apptheme.DinosaurEarthSunnyGrassyID) {
		t.Fatal("setting the default theme should succeed")
	}

	loaded := New()
	loaded.Profile = original.Profile
	if err := loaded.LoadState(); err != nil {
		t.Fatalf("load theme state: %v", err)
	}
	if loaded.ThemeID() != apptheme.DinosaurEarthSunnyGrassyID {
		t.Fatalf("loaded theme = %q", loaded.ThemeID())
	}

	statePath := filepath.Join(dataHome, "profiles", "theme-test", "state.json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	var state TreeState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	state.Theme = "missing-theme"
	data, err = json.Marshal(state)
	if err != nil {
		t.Fatalf("encode state: %v", err)
	}
	if err := os.WriteFile(statePath, data, 0o644); err != nil {
		t.Fatalf("write unknown theme state: %v", err)
	}

	fallback := New()
	fallback.Profile = original.Profile
	if err := fallback.LoadState(); err != nil {
		t.Fatalf("load fallback state: %v", err)
	}
	if fallback.ThemeID() != apptheme.Default().ID {
		t.Fatalf("unknown theme fallback = %q, want %q", fallback.ThemeID(), apptheme.Default().ID)
	}
}
