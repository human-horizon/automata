// Package testutil exposes helpers shared by Automata tests.
package testutil

import (
	"fmt"
	"os"
	"testing"
)

// RunMain isolates HOME and removes inherited storage/profile overrides for
// the lifetime of a test binary. Tests may still set their own HOME or explicit
// data root without writing to the caller's workspace.
//
//	func TestMain(m *testing.M) { testutil.RunMain(m) }
func RunMain(m *testing.M) {
	os.Exit(runMain(m.Run))
}

func runMain(run func() int) (code int) {
	environment := []struct {
		name    string
		value   string
		present bool
	}{
		{name: "HOME"},
		{name: "AI_DATA_HOME"},
		{name: "AUTOMATA_HOME"},
		{name: "AI_PROFILE"},
		{name: "AUTOMATA_PROFILE"},
	}
	for index := range environment {
		variable := &environment[index]
		variable.value, variable.present = os.LookupEnv(variable.name)
	}

	temporaryHome, err := os.MkdirTemp("", "automata-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "testutil: create temp HOME:", err)
		return 1
	}
	defer func() {
		for _, variable := range environment {
			var err error
			if variable.present {
				err = os.Setenv(variable.name, variable.value)
			} else {
				err = os.Unsetenv(variable.name)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "testutil: restore %s: %v\n", variable.name, err)
				if code == 0 {
					code = 1
				}
			}
		}
		if err := os.RemoveAll(temporaryHome); err != nil {
			fmt.Fprintln(os.Stderr, "testutil: remove temp HOME:", err)
			if code == 0 {
				code = 1
			}
		}
	}()

	for _, variable := range environment {
		var err error
		if variable.name == "HOME" {
			err = os.Setenv(variable.name, temporaryHome)
		} else {
			err = os.Unsetenv(variable.name)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "testutil: isolate %s: %v\n", variable.name, err)
			return 1
		}
	}
	return run()
}
