package main

import (
	"errors"
	"io"
	"path/filepath"
	"testing"

	"github.com/HumanHorizon/automata/internal/scrollback"
)

func TestResolveStartupConfigRejectsInvalidValuesBeforeHomeResolution(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())

	for _, test := range []struct {
		name    string
		profile string
		piTag   string
	}{
		{name: "empty profile slug", profile: "!!!", piTag: "just"},
		{name: "reserved default profile", profile: "default", piTag: "just"},
		{name: "case-folded default profile", profile: "Default", piTag: "just"},
		{name: "slug-normalized default profile", profile: "___ Default ___", piTag: "just"},
		{name: "pi path traversal", profile: "valid", piTag: "../outside"},
		{name: "pi separator", profile: "valid", piTag: "agent/name"},
		{name: "pi whitespace", profile: "valid", piTag: "agent name"},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := false
			_, err := resolveStartupConfig(test.profile, test.piTag, func() (string, error) {
				called = true
				return "/home/test", nil
			})
			if err == nil {
				t.Fatal("invalid startup values were accepted")
			}
			if called {
				t.Fatal("home resolution ran before input validation")
			}
		})
	}
}

func TestParseCLIFlagsScrollbackDefaultsZeroAndNegative(t *testing.T) {
	defaults, err := parseCLIFlags(nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if defaults.scrollbackLines != scrollback.DefaultLines {
		t.Fatalf("default scrollback = %d, want %d", defaults.scrollbackLines, scrollback.DefaultLines)
	}
	if defaults.debugLog != "" {
		t.Fatalf("default debug log path = %q, want disabled", defaults.debugLog)
	}

	unlimited, err := parseCLIFlags([]string{"--scrollback-lines=0"}, io.Discard)
	if err != nil {
		t.Fatalf("parse unlimited scrollback: %v", err)
	}
	if unlimited.scrollbackLines != 0 {
		t.Fatalf("zero scrollback = %d, want unlimited", unlimited.scrollbackLines)
	}
	if _, err := parseCLIFlags([]string{"--scrollback-lines=-1"}, io.Discard); err == nil {
		t.Fatal("negative scrollback limit was accepted")
	}
}

func TestResolveStartupConfigPreservesProfileAndBuildsSafePiPath(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	config, err := resolveStartupConfig("Профиль Ω", "agent_2-x", func() (string, error) {
		return filepath.Join(string(filepath.Separator), "home", "anya"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.profile != "Профиль Ω" {
		t.Fatalf("profile = %q, want original profile", config.profile)
	}
	want := filepath.Join(string(filepath.Separator), "home", "anya", ".ai", "agent_2-x", "pi")
	if config.piAgentDir != want {
		t.Fatalf("Pi directory = %q, want %q", config.piAgentDir, want)
	}
}

func TestResolveStartupConfigRequiresHomeWhenNeeded(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	_, err := resolveStartupConfig("valid", "just", func() (string, error) {
		return "", errors.New("home unavailable")
	})
	if err == nil {
		t.Fatal("missing home directory was accepted for a Pi tag")
	}
}

func TestMouseModeSequenceEnablesAndDisablesAllUsedModes(t *testing.T) {
	wantEnabled := "\x1b[?1000h\x1b[?1002h\x1b[?1003h\x1b[?1006h"
	if got := mouseModeSequence(true); got != wantEnabled {
		t.Fatalf("enabled mouse sequence = %q, want %q", got, wantEnabled)
	}
	wantDisabled := "\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l"
	if got := mouseModeSequence(false); got != wantDisabled {
		t.Fatalf("disabled mouse sequence = %q, want %q", got, wantDisabled)
	}
}

func TestValidPiTag(t *testing.T) {
	for _, test := range []struct {
		tag   string
		valid bool
	}{
		{tag: "just", valid: true},
		{tag: "agent_2-x", valid: true},
		{tag: "", valid: false},
		{tag: ".", valid: false},
		{tag: "..", valid: false},
		{tag: "../outside", valid: false},
		{tag: "agent/name", valid: false},
		{tag: "agent\\name", valid: false},
		{tag: "agent name", valid: false},
	} {
		t.Run(test.tag, func(t *testing.T) {
			if got := validPiTag(test.tag); got != test.valid {
				t.Fatalf("validPiTag(%q) = %v, want %v", test.tag, got, test.valid)
			}
		})
	}
}
