// Manual TUI check for Automata using cue-tty.
// Exercises the full mouse-driven UI (no keyboard shortcuts for navigation).
// Element coordinates are queried from warp's HTTP element tree, so tests
// do not depend on hardcoded cell coordinates.
// Run: go run ./e2e/manual from the project root.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/starframe-dev/cue-tty/pkg/cue"
	"github.com/starframe-dev/warp"
)

func projectRoot() string {
	_, file, _, _ := runtime.Caller(0)
	root, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
	if err != nil {
		panic(err)
	}
	return root
}

func buildAutomataBinary(root string) (string, func(), error) {
	if binary := os.Getenv("AUTOMATA_BIN"); binary != "" {
		return binary, func() {}, nil
	}
	dir, err := os.MkdirTemp("", "automata-manual-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() {
		if err := os.RemoveAll(dir); err != nil {
			fmt.Fprintf(os.Stderr, "remove manual build dir: %v\n", err)
		}
	}
	binary := filepath.Join(dir, "automata")
	cmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", binary, ".")
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("build Automata: %w\n%s", err, output)
	}
	return binary, cleanup, nil
}

func main() {
	root := projectRoot()
	binary, cleanupBinary, err := buildAutomataBinary(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer cleanupBinary()

	// Request an element-tree HTTP server from Automata and wait for it to
	// write the listening port to a temp file.
	portFile := filepath.Join(os.TempDir(), fmt.Sprintf("automata-port-%d", os.Getpid()))
	defer func() {
		if err := os.Remove(portFile); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "remove port file: %v\n", err)
		}
	}()

	app, err := cue.Launch(binary,
		cue.WithDir(root),
		cue.WithSize(80, 24),
		cue.WithEnv("TERM=xterm-256color", "AUTOMATA_ELEMENTS_PORT_FILE="+portFile),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "launch automata: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		if err := app.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "close automata: %v\n", err)
		}
	}()

	page := app.Page()
	page.WaitStable(1 * time.Second)

	var httpPort string
	for i := 0; i < 100; i++ {
		if data, err := os.ReadFile(portFile); err == nil && len(data) > 0 {
			httpPort = strings.TrimSpace(string(data))
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if httpPort == "" {
		fmt.Fprintf(os.Stderr, "AUTOMATA_ELEMENTS_PORT_FILE not written\n")
		os.Exit(1)
	}
	elemsURL := "http://127.0.0.1:" + httpPort + "/elements"

	find := func(role, name, action string) (warp.Element, bool) {
		resp, err := http.Get(elemsURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "get elements: %v\n", err)
			os.Exit(1)
		}
		body, readErr := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		if readErr != nil {
			fmt.Fprintf(os.Stderr, "read elements response: %v\n", readErr)
			os.Exit(1)
		}
		if closeErr != nil {
			fmt.Fprintf(os.Stderr, "close elements response: %v\n", closeErr)
			os.Exit(1)
		}
		var elems []warp.Element
		if err := json.Unmarshal(body, &elems); err != nil {
			fmt.Fprintf(os.Stderr, "decode elements: %v\n", err)
			os.Exit(1)
		}
		return warp.FindElement(elems, role, name, action)
	}

	must := func(action string, err error) {
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", action, err)
			os.Exit(1)
		}
	}

	click := func(role, name, action string) {
		var el warp.Element
		var ok bool
		for i := 0; i < 50; i++ {
			el, ok = find(role, name, action)
			if ok {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !ok {
			fmt.Fprintf(os.Stderr, "element not found: role=%q name=%q action=%q\n", role, name, action)
			os.Exit(1)
		}
		x, y := el.Bounds.Center()
		must("mouse click", page.MouseClick(x, y))
	}

	fmt.Println("=== Step 1: Initial render ===")
	printScreen(page)

	fmt.Println("\n=== Step 2: Create folder 'Work' via header +Folder button ===")
	click("button", "+Folder", "add-folder")
	page.WaitStable(300 * time.Millisecond)
	printScreen(page)
	must("type folder name", page.Type("Work"))
	page.WaitStable(100 * time.Millisecond)
	printScreen(page)
	click("button", "[Create]", "create")
	page.WaitStable(800 * time.Millisecond)
	printScreen(page)

	fmt.Println("\n=== Step 3: Select 'Work' folder by clicking it ===")
	click("folder", "Work", "")
	page.WaitStable(100 * time.Millisecond)
	printScreen(page)

	fmt.Println("\n=== Step 4: Create chat 'notes' inside 'Work' via header +Chat button ===")
	click("button", "+Chat", "add-chat")
	page.WaitStable(300 * time.Millisecond)
	printScreen(page)
	must("type chat name", page.Type("notes"))
	page.WaitStable(100 * time.Millisecond)
	printScreen(page)
	click("button", "[Create]", "create")
	page.WaitStable(800 * time.Millisecond)
	printScreen(page)

	fmt.Println("\n=== Step 5: Open 'notes' chat and echo session ID ===")
	click("chat", "notes", "")
	page.WaitStable(1 * time.Second)
	printScreen(page)
	must("type session command", page.Type(`echo "$AUTOMATA_SESSION_ID"`))
	page.WaitStable(300 * time.Millisecond)
	must("execute session command", page.Press("Enter"))
	page.WaitStable(1 * time.Second)
	printScreen(page)

	fmt.Println("\n=== Step 6: Return focus to tree by clicking 'Work' ===")
	click("folder", "Work", "")
	page.WaitStable(300 * time.Millisecond)
	printScreen(page)

	fmt.Println("\n=== Step 7: Rename 'notes' to 'todo' via hover ✎ action ===")
	click("action", "rename:notes", "rename")
	page.WaitStable(300 * time.Millisecond)
	printScreen(page)
	must("type renamed chat name", page.Type("todo"))
	page.WaitStable(100 * time.Millisecond)
	printScreen(page)
	click("button", "[Create]", "create")
	page.WaitStable(800 * time.Millisecond)
	printScreen(page)

	fmt.Println("\n=== Step 8: Delete 'todo' via hover ✕ action and confirm ===")
	click("action", "delete:todo", "delete")
	page.WaitStable(300 * time.Millisecond)
	printScreen(page)
	click("button", "[Del]", "confirm-delete")
	page.WaitStable(800 * time.Millisecond)
	printScreen(page)

	fmt.Println("\n=== Step 9: Drag input modal by title, then cancel ===")
	click("button", "+Folder", "add-folder")
	page.WaitStable(300 * time.Millisecond)
	printScreen(page)
	title, _ := find("title-bar", "Folder name", "")
	if err := page.MouseDown(0, title.Bounds.X, title.Bounds.Y); err != nil {
		fmt.Fprintf(os.Stderr, "mouse down modal title: %v\n", err)
		return
	}
	page.WaitStable(50 * time.Millisecond)
	if err := page.MouseMove(title.Bounds.X, title.Bounds.Y+5); err != nil {
		fmt.Fprintf(os.Stderr, "drag modal title: %v\n", err)
		return
	}
	page.WaitStable(50 * time.Millisecond)
	if err := page.MouseUp(0, title.Bounds.X, title.Bounds.Y+5); err != nil {
		fmt.Fprintf(os.Stderr, "mouse up modal title: %v\n", err)
		return
	}
	page.WaitStable(200 * time.Millisecond)
	printScreen(page)
	// Use the close (✕) button for cancelling the input modal, which is more
	// reliably identified than the [Cancel]/[×] button label across panel widths.
	click("button", "✕", "cancel")
	page.WaitStable(500 * time.Millisecond)
	printScreen(page)

	// Re-create a chat so we can open its delete confirmation.
	click("button", "+Chat", "add-chat")
	page.WaitStable(300 * time.Millisecond)
	must("type temporary chat name", page.Type("temp"))
	page.WaitStable(100 * time.Millisecond)
	click("button", "[Create]", "create")
	page.WaitStable(800 * time.Millisecond)

	fmt.Println("\n=== Step 10: Drag confirm modal by title, then cancel ===")
	click("action", "delete:temp", "delete")
	page.WaitStable(300 * time.Millisecond)
	printScreen(page)
	title, _ = find("title-bar", "Delete chat", "")
	if err := page.MouseDown(0, title.Bounds.X, title.Bounds.Y); err != nil {
		fmt.Fprintf(os.Stderr, "mouse down modal title: %v\n", err)
		return
	}
	page.WaitStable(50 * time.Millisecond)
	if err := page.MouseMove(title.Bounds.X, title.Bounds.Y+5); err != nil {
		fmt.Fprintf(os.Stderr, "drag modal title: %v\n", err)
		return
	}
	page.WaitStable(50 * time.Millisecond)
	if err := page.MouseUp(0, title.Bounds.X, title.Bounds.Y+5); err != nil {
		fmt.Fprintf(os.Stderr, "mouse up modal title: %v\n", err)
		return
	}
	page.WaitStable(200 * time.Millisecond)
	printScreen(page)
	// Use the cancel/esc button by action; the rendered label varies with width.
	click("button", "[Esc]", "cancel")
	page.WaitStable(500 * time.Millisecond)
	printScreen(page)

	fmt.Println("\n=== Step 11: Delete 'temp' chat via hover ✕ action ===")
	el, _ := find("folder", "temp", "")
	// Hover over the temp folder row so action icons appear.
	if err := page.MouseMove(el.Bounds.X+el.Bounds.W/2, el.Bounds.Y); err != nil {
		fmt.Fprintf(os.Stderr, "hover temp chat: %v\n", err)
		return
	}
	page.WaitStable(100 * time.Millisecond)
	click("action", "delete:temp", "delete")
	page.WaitStable(300 * time.Millisecond)
	printScreen(page)
	click("button", "[Del]", "confirm-delete")
	page.WaitStable(800 * time.Millisecond)
	printScreen(page)

	fmt.Println("\n=== Done ===")
}

func printScreen(page *cue.Page) {
	text, err := page.Text()
	if err != nil {
		fmt.Printf("error reading screen: %v\n", err)
		return
	}
	fmt.Println(strings.Repeat("-", 80))
	fmt.Println(text)
	fmt.Println(strings.Repeat("-", 80))
}
