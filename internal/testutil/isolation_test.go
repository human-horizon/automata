package testutil

import (
	"fmt"
	"os"
	"testing"
)

func TestRunMainRestoresEnvironmentAndCleansHome(t *testing.T) {
	for _, test := range []struct {
		name    string
		present bool
		empty   bool
	}{
		{name: "unset"},
		{name: "empty", present: true, empty: true},
		{name: "explicit", present: true},
	} {
		for _, exitCode := range []int{0, 7} {
			t.Run(fmt.Sprintf("%s/exit-%d", test.name, exitCode), func(t *testing.T) {
				names := []string{"HOME", "AI_DATA_HOME", "AUTOMATA_HOME", "AI_PROFILE", "AUTOMATA_PROFILE"}
				values := make(map[string]string, len(names))
				for _, name := range names {
					value := ""
					if !test.empty {
						value = "caller-" + name
					}
					values[name] = value
					t.Setenv(name, value)
					if !test.present {
						if err := os.Unsetenv(name); err != nil {
							t.Fatal(err)
						}
					}
				}

				var temporaryHome string
				called := false
				got := runMain(func() int {
					called = true
					temporaryHome = os.Getenv("HOME")
					if temporaryHome == "" || temporaryHome == values["HOME"] {
						t.Fatalf("HOME was not isolated: %q", temporaryHome)
					}
					info, err := os.Stat(temporaryHome)
					if err != nil {
						t.Fatal(err)
					}
					if !info.IsDir() || info.Mode().Perm() != 0o700 {
						t.Fatalf("temporary HOME is not a private directory: %v", info.Mode())
					}
					for _, name := range names[1:] {
						if value, exists := os.LookupEnv(name); exists {
							t.Errorf("%s was not removed: %q", name, value)
						}
					}
					return exitCode
				})
				if !called || got != exitCode {
					t.Fatalf("runMain: called=%v exit=%d, want %d", called, got, exitCode)
				}
				for _, name := range names {
					value, exists := os.LookupEnv(name)
					if exists != test.present || exists && value != values[name] {
						t.Errorf("%s was not restored: value=%q present=%v", name, value, exists)
					}
				}
				if _, err := os.Stat(temporaryHome); !os.IsNotExist(err) {
					t.Errorf("temporary HOME was not removed: %v", err)
				}
			})
		}
	}
}
