package tree

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/HumanHorizon/automata/internal/paths"
	"github.com/charmbracelet/lipgloss"
)

func TestLoadStateAcceptsSupportedVersions(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	for _, test := range []struct {
		name string
		data string
	}{
		{name: "missing version", data: `{"items":[{"name":"chat","is_folder":false}]}`},
		{name: "version one", data: `{"version":1,"items":[{"name":"chat","is_folder":false}]}`},
		{name: "version two", data: `{"version":2,"items":[{"name":"chat","is_folder":false}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			profile := test.name
			path := paths.StatePath(profile)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(test.data), 0o644); err != nil {
				t.Fatal(err)
			}
			tr := New()
			tr.Profile = profile
			if err := tr.LoadState(); err != nil {
				t.Fatalf("LoadState: %v", err)
			}
			if len(tr.Root()) != 1 || tr.Root()[0].Name != "chat" {
				t.Fatalf("loaded tree = %#v, want one chat", tr.Root())
			}
		})
	}
}

func TestLoadStateRejectsInvalidSnapshotWithoutReplacingTreeOrFile(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	tr := New()
	tr.Profile = "invalid-state"
	tr.AddChat("preserved")
	preserved := tr.Root()[0]

	path := paths.StatePath(tr.Profile)
	invalid := []byte(`{"version":2,"items":[{"name":"Foo Bar","is_folder":false},{"name":"foo_bar","is_folder":false}]}`)
	if err := os.WriteFile(path, invalid, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := tr.LoadState(); err == nil {
		t.Fatal("LoadState accepted duplicate canonical siblings")
	}
	if len(tr.Root()) != 1 || tr.Root()[0] != preserved {
		t.Fatal("invalid snapshot replaced the current in-memory tree")
	}
	if err := tr.SaveState(); err == nil {
		t.Fatal("SaveState should remain blocked after invalid state load")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(invalid) {
		t.Fatalf("invalid state file changed: %s", got)
	}
}

func TestLoadStateMigrationFailureDoesNotReplaceInMemoryTree(t *testing.T) {
	home, dataHome := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AI_DATA_HOME", dataHome)
	legacyDir := filepath.Join(home, ".automata")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "state.json"), []byte(`{"version":2,"items":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	profileDir := paths.ProfileDir("")
	if err := os.MkdirAll(filepath.Join(profileDir, migrationMarkerName), 0o755); err != nil {
		t.Fatal(err)
	}
	path := paths.StatePath("")
	validDiskState := []byte(`{"version":2,"items":[{"name":"disk","is_folder":false}]}`)
	if err := os.WriteFile(path, validDiskState, 0o644); err != nil {
		t.Fatal(err)
	}

	tr := New()
	memoryItem := &Item{Name: "memory"}
	tr.root = []*Item{memoryItem}
	tr.rebuildFlat()
	if err := tr.LoadState(); err == nil {
		t.Fatal("LoadState accepted the migration failure")
	}
	if len(tr.Root()) != 1 || tr.Root()[0] != memoryItem {
		t.Fatal("migration failure replaced the current in-memory tree")
	}
	if tr.StateLoadError() == nil {
		t.Fatal("migration failure was not retained as a save barrier")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(validDiskState) {
		t.Fatalf("canonical snapshot changed: state=%s err=%v", got, err)
	}
}

func TestLoadStateRejectsFutureVersionAndInvalidHierarchy(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	cases := []struct {
		name string
		data string
	}{
		{name: "future version", data: `{"version":3,"items":[]}`},
		{name: "folder and terminal", data: `{"version":2,"items":[{"name":"node","is_folder":true,"is_terminal":true}]}`},
		{name: "leaf children", data: `{"version":2,"items":[{"name":"node","is_folder":false,"items":[{"name":"child","is_folder":false}]}]}`},
		{name: "nil item", data: `{"version":2,"items":[null]}`},
		{name: "empty canonical name", data: `{"version":2,"items":[{"name":" !!! ","is_folder":false}]}`},
	}
	for index, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			profile := string(rune('a' + index))
			path := paths.StatePath(profile)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			tr := New()
			tr.Profile = profile
			tr.AddChat("before")
			before := tr.Root()[0]
			if err := os.WriteFile(path, []byte(test.data), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := tr.LoadState(); err == nil {
				t.Fatal("LoadState accepted invalid snapshot")
			}
			if len(tr.Root()) != 1 || tr.Root()[0] != before {
				t.Fatal("failed load changed the existing tree")
			}
		})
	}
}

func TestSaveStateReportsDirectorySyncFailureAsCommitted(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	tr := New()
	tr.Profile = "committed-warning"
	tr.AddChat("persisted")

	oldSync := syncStateDir
	t.Cleanup(func() { syncStateDir = oldSync })
	syncStateDir = func(string) error { return errors.New("sync failed") }
	err := tr.SaveState()
	var committed *CommittedStateError
	if !errors.As(err, &committed) {
		t.Fatalf("SaveState error = %v, want CommittedStateError", err)
	}
	data, readErr := os.ReadFile(paths.StatePath(tr.Profile))
	if readErr != nil {
		t.Fatalf("committed state missing: %v", readErr)
	}
	var state TreeState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("committed state is invalid JSON: %v", err)
	}
	if len(state.Items) != 1 || state.Items[0].Name != "persisted" {
		t.Fatalf("committed state = %#v", state.Items)
	}
}

func TestCreateRejectsCanonicalSiblingCollisionsAcrossTypes(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	tr := New()
	if _, err := tr.CreateFolder("Foo Bar"); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	if _, err := tr.CreateChat("foo_bar"); err == nil {
		t.Fatal("CreateChat accepted a canonical root sibling collision")
	}
	if len(tr.Root()) != 1 || tr.Root()[0].Name != "Foo Bar" {
		t.Fatalf("root after rejected collision = %#v", tr.Root())
	}

	parent, err := tr.CreateFolder("children")
	if err != nil {
		t.Fatalf("CreateFolder children: %v", err)
	}
	if _, err := tr.CreateChildTerminal(parent, "Task One"); err != nil {
		t.Fatalf("CreateChildTerminal: %v", err)
	}
	if _, err := tr.CreateChildChat(parent, "task-one"); err == nil {
		t.Fatal("CreateChildChat accepted a canonical child collision")
	}
	if len(parent.Children) != 1 {
		t.Fatalf("child count after rejected collision = %d, want 1", len(parent.Children))
	}
	if _, err := tr.CreateChat(" !!! "); err == nil {
		t.Fatal("CreateChat accepted an empty canonical name")
	}
}

func TestMoveItemRejectsSiblingCollisionBeforePreflight(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	tr := New()
	source, err := tr.CreateFolder("source")
	if err != nil {
		t.Fatal(err)
	}
	target, err := tr.CreateFolder("target")
	if err != nil {
		t.Fatal(err)
	}
	moving, err := tr.CreateChildChat(source, "Foo Bar")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tr.CreateChildTerminal(target, "foo_bar"); err != nil {
		t.Fatal(err)
	}
	preflightCalls := 0
	tr.SetOnBeforeItemMoved(func(*Item, *Item) (func() error, error) {
		preflightCalls++
		return nil, nil
	})
	if err := tr.MoveItemChecked(moving, target); err == nil {
		t.Fatal("MoveItemChecked accepted a canonical destination collision")
	}
	if preflightCalls != 0 {
		t.Fatalf("move preflight calls = %d, want 0", preflightCalls)
	}
	if moving.parent != source || len(source.Children) != 1 || len(target.Children) != 1 {
		t.Fatal("rejected move changed tree membership")
	}
}

func TestMoveSelectedOutRejectsSiblingCollisionBeforePreflight(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	tr := New()
	outer, err := tr.CreateFolder("outer")
	if err != nil {
		t.Fatal(err)
	}
	inner, err := tr.CreateChildFolder(outer, "inner")
	if err != nil {
		t.Fatal(err)
	}
	moving, err := tr.CreateChildChat(inner, "Foo Bar")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tr.CreateChildTerminal(outer, "foo_bar"); err != nil {
		t.Fatal(err)
	}
	preflightCalls := 0
	tr.SetOnBeforeItemMoved(func(*Item, *Item) (func() error, error) {
		preflightCalls++
		return nil, nil
	})
	tr.rebuildFlat()
	tr.reselectItem(moving)
	moved, err := tr.MoveSelectedOutChecked()
	if err == nil || moved {
		t.Fatalf("MoveSelectedOutChecked = (%v, %v), want rejected collision", moved, err)
	}
	if preflightCalls != 0 {
		t.Fatalf("move preflight calls = %d, want 0", preflightCalls)
	}
	if moving.parent != inner || len(inner.Children) != 1 || len(outer.Children) != 2 {
		t.Fatal("rejected move changed tree membership")
	}
}

func TestCreateRollbackAndCommittedWarning(t *testing.T) {
	t.Run("precommit failure rolls back", func(t *testing.T) {
		t.Setenv("AI_DATA_HOME", t.TempDir())
		tr := New()
		tr.SetSaveStateFunc(func() error { return errors.New("disk unavailable") })
		item, err := tr.CreateChat("new")
		if err == nil || item != nil {
			t.Fatalf("CreateChat = (%v, %v), want precommit failure", item, err)
		}
		if len(tr.Root()) != 0 {
			t.Fatalf("root after failed create = %#v", tr.Root())
		}
	})

	t.Run("directory sync warning keeps committed item", func(t *testing.T) {
		t.Setenv("AI_DATA_HOME", t.TempDir())
		tr := New()
		oldSync := syncStateDir
		t.Cleanup(func() { syncStateDir = oldSync })
		syncStateDir = func(string) error { return errors.New("directory sync failed") }
		item, err := tr.CreateChat("committed")
		var committed *CommittedStateError
		if !errors.As(err, &committed) || item == nil {
			t.Fatalf("CreateChat = (%v, %v), want committed warning and item", item, err)
		}
		if len(tr.Root()) != 1 || tr.Root()[0] != item {
			t.Fatal("committed create was rolled back")
		}
	})
}

func TestStructuralMutationsRollbackOnSaveFailure(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*testing.T) (*Tree, func() bool)
	}{
		{
			name: "move up",
			setup: func(t *testing.T) (*Tree, func() bool) {
				tr := New()
				tr.AddChat("a")
				tr.AddChat("b")
				tr.rebuildFlat()
				tr.selected = 1
				return tr, func() bool { return tr.root[0].Name == "a" && tr.root[1].Name == "b" && tr.SelectedItem().Name == "b" }
			},
		},
		{
			name: "move down",
			setup: func(t *testing.T) (*Tree, func() bool) {
				tr := New()
				tr.AddChat("a")
				tr.AddChat("b")
				tr.rebuildFlat()
				tr.selected = 0
				return tr, func() bool { return tr.root[0].Name == "a" && tr.root[1].Name == "b" && tr.SelectedItem().Name == "a" }
			},
		},
		{
			name: "move item",
			setup: func(t *testing.T) (*Tree, func() bool) {
				tr := New()
				tr.AddFolder("source")
				tr.AddFolder("target")
				source, target := tr.root[0], tr.root[1]
				tr.addChildChat(source, "moving")
				moving := source.Children[0]
				return tr, func() bool { return moving.parent == source && len(source.Children) == 1 && len(target.Children) == 0 }
			},
		},
		{
			name: "rename",
			setup: func(t *testing.T) (*Tree, func() bool) {
				tr := New()
				tr.AddChat("before")
				item := tr.root[0]
				return tr, func() bool { return item.Name == "before" }
			},
		},
		{
			name: "sort root",
			setup: func(t *testing.T) (*Tree, func() bool) {
				tr := New()
				tr.AddChat("z")
				tr.AddChat("a")
				return tr, func() bool { return tr.root[0].Name == "z" && tr.root[1].Name == "a" }
			},
		},
		{
			name: "sort children",
			setup: func(t *testing.T) (*Tree, func() bool) {
				tr := New()
				tr.AddFolder("parent")
				parent := tr.root[0]
				tr.addChildChat(parent, "z")
				tr.addChildChat(parent, "a")
				return tr, func() bool { return parent.Children[0].Name == "z" && parent.Children[1].Name == "a" }
			},
		},
		{
			name: "archive",
			setup: func(t *testing.T) (*Tree, func() bool) {
				tr := New()
				tr.AddFolder("folder")
				tr.AddChat("chat")
				folder := tr.root[0]
				return tr, func() bool { return !folder.Archived && tr.root[0] == folder && tr.root[1].Name == "chat" }
			},
		},
		{
			name: "bind",
			setup: func(t *testing.T) (*Tree, func() bool) {
				tr := New()
				tr.AddFolder("folder")
				folder := tr.root[0]
				return tr, func() bool { return folder.BoundPath == "" }
			},
		},
		{
			name: "unbind",
			setup: func(t *testing.T) (*Tree, func() bool) {
				tr := New()
				tr.AddFolder("folder")
				folder := tr.root[0]
				folder.SetBoundPath(t.TempDir())
				oldPath := folder.BoundPath
				return tr, func() bool { return folder.BoundPath == oldPath }
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("AI_DATA_HOME", t.TempDir())
			tr, unchanged := test.setup(t)
			tr.SetSaveStateFunc(func() error { return errors.New("injected save failure") })
			switch test.name {
			case "move item":
				if err := tr.MoveItemChecked(tr.root[0].Children[0], tr.root[1]); err == nil {
					t.Fatal("MoveItemChecked succeeded despite save failure")
				}
			case "rename":
				if err := tr.RenameItem(tr.root[0], "after"); err == nil {
					t.Fatal("RenameItem succeeded despite save failure")
				}
			case "move up":
				if tr.MoveSelectedUp() {
					t.Fatal("MoveSelectedUp reported commit after save failure")
				}
			case "move down":
				if tr.MoveSelectedDown() {
					t.Fatal("MoveSelectedDown reported commit after save failure")
				}
			case "sort root":
				tr.sortRootByName()
			case "sort children":
				tr.sortChildrenAlphabetically(tr.root[0])
			case "archive":
				tr.toggleArchive(tr.root[0])
			case "bind":
				if err := tr.bindFolderChecked(tr.root[0], t.TempDir()); err == nil {
					t.Fatal("bindFolderChecked succeeded despite save failure")
				}
			case "unbind":
				if err := tr.unbindFolderChecked(tr.root[0]); err == nil {
					t.Fatal("unbindFolderChecked succeeded despite save failure")
				}
			}
			if !unchanged() {
				t.Fatalf("%s did not restore its original tree state", test.name)
			}
		})
	}
}

func TestCommittedRenameAndMoveWarningsDoNotRollback(t *testing.T) {
	t.Run("rename", func(t *testing.T) {
		t.Setenv("AI_DATA_HOME", t.TempDir())
		tr := New()
		item, err := tr.CreateChat("before")
		if err != nil {
			t.Fatal(err)
		}
		rollbackCalls, committedCalls := 0, 0
		tr.SetOnBeforeRename(func(*Item, string) (func() error, error) {
			return func() error { rollbackCalls++; return nil }, nil
		})
		tr.SetOnRenameCommitted(func(*Item, string, string) { committedCalls++ })
		oldSync := syncStateDir
		t.Cleanup(func() { syncStateDir = oldSync })
		syncStateDir = func(string) error { return errors.New("directory sync failed") }
		if err := tr.RenameItem(item, "after"); err != nil {
			t.Fatalf("RenameItem: committed warning should not be returned as failure: %v", err)
		}
		if item.Name != "after" || rollbackCalls != 0 || committedCalls != 1 {
			t.Fatalf("rename state: name=%q rollback=%d committed=%d", item.Name, rollbackCalls, committedCalls)
		}
		if tr.LastActionError() == nil {
			t.Fatal("post-commit warning is not observable")
		}
	})

	t.Run("move", func(t *testing.T) {
		t.Setenv("AI_DATA_HOME", t.TempDir())
		tr := New()
		source, err := tr.CreateFolder("source")
		if err != nil {
			t.Fatal(err)
		}
		target, err := tr.CreateFolder("target")
		if err != nil {
			t.Fatal(err)
		}
		moving, err := tr.CreateChildChat(source, "chat")
		if err != nil {
			t.Fatal(err)
		}
		rollbackCalls, committedCalls := 0, 0
		tr.SetOnBeforeItemMoved(func(*Item, *Item) (func() error, error) {
			return func() error { rollbackCalls++; return nil }, nil
		})
		tr.SetOnItemMoved(func(*Item, string, string) { committedCalls++ })
		oldSync := syncStateDir
		t.Cleanup(func() { syncStateDir = oldSync })
		syncStateDir = func(string) error { return errors.New("directory sync failed") }
		err = tr.MoveItemChecked(moving, target)
		var committed *CommittedStateError
		if !errors.As(err, &committed) {
			t.Fatalf("MoveItemChecked error = %v, want committed warning", err)
		}
		if moving.parent != target || rollbackCalls != 0 || committedCalls != 1 {
			t.Fatalf("move state: parent=%p rollback=%d committed=%d", moving.parent, rollbackCalls, committedCalls)
		}
		if tr.LastActionError() == nil {
			t.Fatal("post-commit warning is not observable")
		}
	})
}

func TestCosmeticFolderToggleRemainsBestEffort(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	tr := New()
	folder, err := tr.CreateFolder("folder")
	if err != nil {
		t.Fatal(err)
	}
	folder.Expanded = false
	tr.SetSaveStateFunc(func() error { return errors.New("save failure") })
	tr.ToggleFolder(folder)
	if !folder.Expanded {
		t.Fatal("cosmetic folder expansion was rolled back after best-effort save failure")
	}
}

func TestDeleteRunsCleanupOnlyAfterCommittedTreeState(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	tr := New()
	tr.AddChat("delete-me")
	item := tr.Root()[0]
	var order []string
	tr.SetOnBeforeDelete(func(*Item) error {
		order = append(order, "preflight")
		return nil
	})
	tr.SetSaveStateFunc(func() error {
		order = append(order, "save")
		if len(tr.Root()) != 0 {
			t.Fatal("tree was not staged before SaveState")
		}
		return nil
	})
	tr.SetOnDeleteCommitted(func(*Item) error {
		order = append(order, "cleanup")
		return nil
	})
	tr.deleteItem(item)
	if got := strings.Join(order, ","); got != "preflight,save,cleanup" {
		t.Fatalf("delete order = %q", got)
	}
	if len(tr.Root()) != 0 {
		t.Fatal("deleted item remains after successful commit")
	}
}

func TestDeletePostCommitWarningKeepsDeletionAndRunsCleanup(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	tr := New()
	item, err := tr.CreateChat("committed-delete")
	if err != nil {
		t.Fatal(err)
	}
	cleanupCalled := false
	tr.SetOnBeforeDelete(func(*Item) error { return nil })
	tr.SetOnDeleteCommitted(func(*Item) error { cleanupCalled = true; return nil })
	oldSync := syncStateDir
	t.Cleanup(func() { syncStateDir = oldSync })
	syncStateDir = func(string) error { return errors.New("directory sync failed") }
	err = tr.DeleteItem(item)
	var committed *CommittedStateError
	if !errors.As(err, &committed) {
		t.Fatalf("DeleteItem error = %v, want committed warning", err)
	}
	if len(tr.Root()) != 0 || !cleanupCalled {
		t.Fatal("post-commit warning rolled back deletion or skipped cleanup")
	}
}

func TestDeleteSaveFailureAbortsPreparedCleanup(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	tr := New()
	tr.AddChat("keep")
	item := tr.Root()[0]
	aborted, committed := false, false
	tr.SetOnBeforeDelete(func(*Item) error { return nil })
	tr.SetOnDeleteAborted(func(*Item) { aborted = true })
	tr.SetOnDeleteCommitted(func(*Item) error { committed = true; return nil })
	tr.SetSaveStateFunc(func() error { return errors.New("injected save failure") })
	tr.deleteItem(item)
	if !aborted || committed {
		t.Fatalf("delete callbacks: aborted=%v committed=%v", aborted, committed)
	}
	if len(tr.Root()) != 1 || tr.Root()[0] != item {
		t.Fatal("failed deletion did not restore the item")
	}
}

func TestSetActiveSessionsKeepsCommittedSnapshotAfterDirectorySyncWarning(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	tr := New()
	tr.SetActiveSessionsInMemory(map[string]struct{}{"old": {}})
	oldSync := syncStateDir
	t.Cleanup(func() { syncStateDir = oldSync })
	syncStateDir = func(string) error { return errors.New("directory sync failed") }
	err := tr.SetActiveSessions(map[string]struct{}{"new": {}})
	var committed *CommittedStateError
	if !errors.As(err, &committed) {
		t.Fatalf("SetActiveSessions error = %v, want committed warning", err)
	}
	if !reflect.DeepEqual(tr.ActiveSessionIDs(), []string{"new"}) {
		t.Fatalf("active sessions after committed warning = %v, want [new]", tr.ActiveSessionIDs())
	}
}

func TestTreeFooterShowsPersistenceErrors(t *testing.T) {
	tr := New()
	tr.NoColor = true
	tr.recordActionError(errors.New("persistence failed"))
	footer := tr.renderFooter(50)
	if !strings.Contains(footer, "persistence failed") {
		t.Fatalf("footer %q does not show action error", footer)
	}
	if got := lipgloss.Width(footer); got != 50 {
		t.Fatalf("footer width = %d, want 50", got)
	}
}

func TestCheckedCreateInputKeepsModalOpenOnValidationFailure(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	tr := New()
	tr.AddChat("already-there")
	tr.startCreateInput("Chat name:", tr.CreateChat)
	tr.inputValue = "ALREADY_THERE"
	tr.confirmInput()
	if !tr.inputMode || tr.inputError == "" {
		t.Fatal("validation failure should keep the input modal open with a message")
	}
	if len(tr.Root()) != 1 || tr.Root()[0].Name != "already-there" {
		t.Fatal("rejected create changed the tree")
	}
}
