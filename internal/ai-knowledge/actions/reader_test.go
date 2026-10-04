package actions

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/HumanHorizon/automata/internal/paths"
)

func actionJSON(id, name, command, cwd string) []byte {
	data, err := json.Marshal(Action{ID: id, Name: name, Command: command, CWD: cwd})
	if err != nil {
		panic(err)
	}
	return data
}

func repeatedID(char byte) string {
	return strings.Repeat(string(char), 64)
}

func TestReadFSValidatesAndSortsActions(t *testing.T) {
	firstID := repeatedID('a')
	secondID := repeatedID('b')
	filesystem := fstest.MapFS{
		secondID + ".json": &fstest.MapFile{Data: actionJSON(secondID, "zeta", "open zeta", "/project")},
		firstID + ".json":  &fstest.MapFile{Data: actionJSON(firstID, "Alpha", "open alpha", "/project")},
		"atomic-temp.tmp":  &fstest.MapFile{Data: []byte("partial")},
	}

	got, err := readFS(filesystem)
	if err != nil {
		t.Fatalf("readFS returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("readFS returned %d actions, want 2: %#v", len(got), got)
	}
	if got[0].Name != "Alpha" || got[1].Name != "zeta" {
		t.Fatalf("actions are not sorted by name: %#v", got)
	}
	if got[0].Command != "open alpha" || got[0].CWD != "/project" {
		t.Fatalf("action fields were not preserved: %#v", got[0])
	}
}

func TestReadFSReturnsValidActionsAndReportsMalformedRecords(t *testing.T) {
	validID := repeatedID('a')
	mismatchedID := repeatedID('b')
	malformedID := repeatedID('c')
	symlinkID := repeatedID('d')
	filesystem := fstest.MapFS{
		validID + ".json":      &fstest.MapFile{Data: actionJSON(validID, "Keep", "open .", "/project")},
		mismatchedID + ".json": &fstest.MapFile{Data: actionJSON(validID, "Wrong ID", "open .", "/project")},
		malformedID + ".json":  &fstest.MapFile{Data: []byte("{")},
		symlinkID + ".json":    &fstest.MapFile{Mode: fs.ModeSymlink, Data: []byte("outside.json")},
		"unsafe.json":          &fstest.MapFile{Data: actionJSON("unsafe", "Unsafe", "open .", "/project")},
	}

	got, err := readFS(filesystem)
	if err == nil {
		t.Fatal("malformed action files produced no diagnostic")
	}
	if len(got) != 1 || got[0].Name != "Keep" {
		t.Fatalf("invalid records affected valid actions: %#v", got)
	}
	for _, want := range []string{"does not match its filename", "decode action", "not a regular file", "invalid action filename"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("diagnostic %q does not contain %q", err, want)
		}
	}
}

func TestReadFSRejectsInvalidActionFields(t *testing.T) {
	cases := []struct {
		name   string
		action Action
	}{
		{name: "empty name", action: Action{ID: repeatedID('a'), Command: "open .", CWD: "/project"}},
		{name: "control name", action: Action{ID: repeatedID('b'), Name: "bad\nname", Command: "open .", CWD: "/project"}},
		{name: "empty command", action: Action{ID: repeatedID('c'), Name: "Open", Command: "  ", CWD: "/project"}},
		{name: "NUL command", action: Action{ID: repeatedID('d'), Name: "Open", Command: "echo\x00bad", CWD: "/project"}},
		{name: "relative cwd", action: Action{ID: repeatedID('e'), Name: "Open", Command: "open .", CWD: "project"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			id := testCase.action.ID
			filesystem := fstest.MapFS{id + ".json": &fstest.MapFile{Data: actionJSON(id, testCase.action.Name, testCase.action.Command, testCase.action.CWD)}}
			got, err := readFS(filesystem)
			if err == nil || len(got) != 0 {
				t.Fatalf("invalid action accepted: actions=%#v err=%v", got, err)
			}
		})
	}
}

func TestReadFSMissingDirectoryIsEmpty(t *testing.T) {
	got, err := readFS(fstest.MapFS{})
	if err != nil || len(got) != 0 {
		t.Fatalf("missing action directory = %#v, %v; want empty without error", got, err)
	}
}

func TestDirectoryValidatesDomainAndUsesCanonicalPaths(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	got, err := Directory("Profile Name", "profile-name__project.ω")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(paths.BaseDir(), "profiles", "profile-name", "domains", "profile-name__project.ω", "actions")
	if got != want {
		t.Fatalf("Directory() = %q, want %q", got, want)
	}

	for _, domain := range []string{"", ".", "..", "/tmp", `folder\\child`, "folder/../outside", "bad\x00domain"} {
		if _, err := Directory("profile", domain); err == nil {
			t.Errorf("Directory accepted unsafe domain %q", domain)
		}
	}
}

func TestReadForProfileUsesFolderDomainAndIgnoresNeighborData(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	firstDirectory, err := Directory("profile", "profile__first")
	if err != nil {
		t.Fatal(err)
	}
	secondDirectory, err := Directory("profile", "profile__second")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct {
		directory string
		name      string
		idChar    byte
	}{
		{directory: firstDirectory, name: "First", idChar: 'a'},
		{directory: secondDirectory, name: "Second", idChar: 'b'},
	} {
		if err := os.MkdirAll(entry.directory, 0o700); err != nil {
			t.Fatal(err)
		}
		id := repeatedID(entry.idChar)
		if err := os.WriteFile(filepath.Join(entry.directory, id+".json"), actionJSON(id, entry.name, "open .", dataHome), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := ReadForProfile("profile", "profile__first")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "First" {
		t.Fatalf("ReadForProfile returned neighbor action: %#v", got)
	}
	if _, err := ReadForProfile("profile", "../outside"); err == nil {
		t.Fatal("ReadForProfile accepted a traversal domain")
	}
}

func TestReadForProfileRejectsSymlinkedActionDirectory(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("AI_DATA_HOME", dataHome)
	profile, domain := "profile", "profile__linked"
	directory, err := Directory(profile, domain)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(directory), 0o700); err != nil {
		t.Fatal(err)
	}
	externalDirectory := filepath.Join(dataHome, "external-actions")
	if err := os.Mkdir(externalDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(externalDirectory, directory); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}

	if _, err := ReadForProfile(profile, domain); err == nil {
		t.Fatal("ReadForProfile followed a symlinked actions directory")
	}
}
