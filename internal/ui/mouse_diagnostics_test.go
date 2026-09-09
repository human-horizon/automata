package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMouseHandlersDoNotWriteFixedDiagnostics guards the removal of the
// synchronous fixed-path mouse diagnostics from both handlers.
func TestMouseHandlersDoNotWriteFixedDiagnostics(t *testing.T) {
	tests := []struct {
		name string
		file string
		path string
	}{
		{name: "context", file: "context_panel.go", path: "/tmp/context-mouse.log"},
		{name: "container", file: "container.go", path: "/tmp/container-mouse.log"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sourcePath := filepath.Join("..", "..", "internal", "ui", tt.file)
			source, err := os.ReadFile(sourcePath)
			if err != nil {
				t.Fatalf("read %s: %v", tt.file, err)
			}
			if strings.Contains(string(source), tt.path) {
				t.Fatalf("%s still references fixed mouse diagnostics path %q", tt.file, tt.path)
			}
		})
	}
}
