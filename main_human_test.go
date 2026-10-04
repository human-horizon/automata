package main

import (
	"strings"
	"testing"

	"github.com/HumanHorizon/automata/internal/paths"
	"github.com/HumanHorizon/automata/internal/tree"
)

func TestHumanPiLaunchExportsCanonicalDataHome(t *testing.T) {
	for _, override := range []bool{false, true} {
		name := "path"
		if override {
			name = "override"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("AI_DATA_HOME", root)
			binary := installFakePi(t)
			t.Setenv("PI_CMD", "")
			if override {
				t.Setenv("PI_CMD", binary)
			}
			app := &App{profile: "Проект Ω"}
			cmd, args, env := app.piLaunch("chat")
			if cmd != binary || len(args) != 2 || args[1] != "chat" {
				t.Fatalf("launch = %q %v", cmd, args)
			}
			for _, value := range []string{
				"AI_DATA_HOME=" + root,
				"AUTOMATA_PROFILE=" + paths.ProfileSlug(app.profile),
				"AUTOMATA_HUMAN_EXPORT=1",
			} {
				if !containsString(env, value) {
					t.Fatalf("missing %q in %v", value, env)
				}
			}
		})
	}
}

func TestHumanEnvironmentIsNotAttachedToShellTerminal(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	app := &App{tree: tree.New()}
	app.tree.AddTerminal("term")
	em := app.createChatEmulator("term")
	if em == nil {
		t.Fatal("terminal emulator missing")
	}
	for _, env := range em.StartEnv() {
		if strings.HasPrefix(env, "AUTOMATA_HUMAN_EXPORT=") {
			t.Fatalf("shell received Human Pi environment: %v", em.StartEnv())
		}
	}
}
