package theme

import "testing"

func TestDefaultThemeIsFirstAvailableTheme(t *testing.T) {
	all := All()
	if len(all) == 0 {
		t.Fatal("expected at least one available theme")
	}
	if got := Default().ID; got != all[0].ID {
		t.Fatalf("default theme %q is not first theme %q", got, all[0].ID)
	}
	if got := all[0].ID; got != DinosaurEarthSunnyGrassyID {
		t.Fatalf("first theme = %q, want %q", got, DinosaurEarthSunnyGrassyID)
	}
}

func TestDefaultThemeKeepsTextColorsNeutral(t *testing.T) {
	palette := Default()
	accentColors := map[string]string{
		"Pink":        palette.Pink,
		"PinkMuted":   palette.PinkMuted,
		"Purple":      palette.Purple,
		"PurpleMuted": palette.PurpleMuted,
	}
	textColors := map[string]string{
		"Text":       palette.Text,
		"TextStrong": palette.TextStrong,
		"TextMuted":  palette.TextMuted,
		"TextDim":    palette.TextDim,
	}

	for textName, textColor := range textColors {
		for accentName, accentColor := range accentColors {
			if textColor == accentColor {
				t.Fatalf("%s reuses chromatic accent %s (%q)", textName, accentName, textColor)
			}
		}
	}
}

func TestResolveUnknownThemeUsesDefault(t *testing.T) {
	if got := Resolve("missing-theme"); got.ID != Default().ID {
		t.Fatalf("unknown theme resolved to %q, want %q", got.ID, Default().ID)
	}
}

func TestAllReturnsIndependentSlice(t *testing.T) {
	all := All()
	all[0].ID = "mutated"
	if got := Default().ID; got == "mutated" {
		t.Fatal("All returned a slice that mutates the registry")
	}
}
