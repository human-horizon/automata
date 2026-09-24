package paths

import (
	"errors"
	"os"
	"testing"
)

func createLegacySessionDir(t *testing.T, profile, sessionID string) {
	t.Helper()
	if err := os.MkdirAll(SessionDir(profile, sessionID), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestResolveLegacySessionProfileFindsExistingProfile(t *testing.T) {
	tests := []struct {
		name      string
		profile   string
		sessionID string
		env       string
	}{
		{name: "default familiar", profile: "", sessionID: "chat__expert"},
		{name: "named regular session", profile: "Getic", sessionID: "getic__chat", env: "wrong-profile"},
		{name: "legacy environment session", profile: "legacy", sessionID: "unprefixed", env: "legacy"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("AI_DATA_HOME", t.TempDir())
			t.Setenv("AI_PROFILE", test.env)
			createLegacySessionDir(t, test.profile, test.sessionID)

			got, err := ResolveLegacySessionProfile(test.sessionID, LegacySessionProfileReadOnly)
			if err != nil {
				t.Fatal(err)
			}
			if want := ProfileSlug(test.profile); got != want {
				t.Fatalf("resolved profile = %q, want %q", got, want)
			}
		})
	}
}

func TestResolveLegacySessionProfileFallbackOrder(t *testing.T) {
	tests := []struct {
		name      string
		sessionID string
		env       string
		want      string
	}{
		{name: "session prefix", sessionID: "named__chat", env: "legacy", want: "named"},
		{name: "environment", sessionID: "unprefixed", env: "legacy", want: "legacy"},
		{name: "default", sessionID: "unprefixed", want: "default"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("AI_DATA_HOME", t.TempDir())
			t.Setenv("AI_PROFILE", test.env)
			got, err := ResolveLegacySessionProfile(test.sessionID, LegacySessionProfileReadOnly)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("resolved profile = %q, want %q", got, test.want)
			}
		})
	}
}

func TestResolveLegacySessionProfileAmbiguityPolicy(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	t.Setenv("AI_PROFILE", "")
	const sessionID = "foo__bar"
	createLegacySessionDir(t, "foo", sessionID)
	createLegacySessionDir(t, "", sessionID)

	readProfile, err := ResolveLegacySessionProfile(sessionID, LegacySessionProfileReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	if readProfile != "foo" {
		t.Fatalf("read-only profile = %q, want prefix-priority profile foo", readProfile)
	}

	if _, err := ResolveLegacySessionProfile(sessionID, LegacySessionProfileDestructive); !errors.Is(err, ErrAmbiguousLegacySessionProfile) {
		t.Fatalf("destructive resolution error = %v, want ErrAmbiguousLegacySessionProfile", err)
	}
}

func TestResolveLegacySessionProfileRejectsInvalidMode(t *testing.T) {
	if _, err := ResolveLegacySessionProfile("session", LegacySessionProfileMode(99)); err == nil {
		t.Fatal("invalid mode unexpectedly resolved a profile")
	}
}

func TestResolveLegacySessionProfileRejectsUnsafeSessionID(t *testing.T) {
	for _, sessionID := range []string{"", "../outside", "session\\other"} {
		if _, err := ResolveLegacySessionProfile(sessionID, LegacySessionProfileReadOnly); err == nil {
			t.Errorf("unsafe session ID %q unexpectedly resolved", sessionID)
		}
	}
}
