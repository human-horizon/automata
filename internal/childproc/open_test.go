package childproc

import "testing"

func TestOpenPathCommand(t *testing.T) {
	tests := []struct {
		goos string
		want string
	}{
		{goos: "darwin", want: "open"},
		{goos: "linux", want: "xdg-open"},
		{goos: "windows", want: "cmd"},
	}
	for _, test := range tests {
		t.Run(test.goos, func(t *testing.T) {
			command, err := openPathCommand(test.goos, "/tmp/example")
			if err != nil {
				t.Fatal(err)
			}
			if got := command.Args[0]; got != test.want {
				t.Fatalf("launcher = %q, want %q", got, test.want)
			}
		})
	}
}

func TestOpenPathCommandRejectsUnsupportedOS(t *testing.T) {
	if _, err := openPathCommand("plan9", "/tmp/example"); err == nil {
		t.Fatal("unsupported OS was accepted")
	}
}
