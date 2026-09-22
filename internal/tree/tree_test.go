package tree

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/HumanHorizon/automata/internal/paths"
	tea "github.com/charmbracelet/bubbletea"
)

func TestNew(t *testing.T) {
	tr := New()
	if tr == nil {
		t.Fatal("expected non-nil tree")
	}
	if len(tr.root) != 0 {
		t.Errorf("expected empty root, got %d items", len(tr.root))
	}
}

func TestAddFolder(t *testing.T) {
	tr := New()
	tr.AddFolder("test")
	if len(tr.root) != 1 {
		t.Fatalf("expected 1 root item, got %d", len(tr.root))
	}
	if !tr.root[0].IsFolder {
		t.Error("expected IsFolder=true")
	}
	if tr.root[0].Name != "test" {
		t.Errorf("expected name 'test', got %q", tr.root[0].Name)
	}
}

func TestAddChat(t *testing.T) {
	tr := New()
	tr.AddChat("chat1")
	if len(tr.root) != 1 {
		t.Fatalf("expected 1 root item, got %d", len(tr.root))
	}
	if tr.root[0].IsFolder {
		t.Error("expected IsFolder=false")
	}
}

func TestFlatList(t *testing.T) {
	tr := New()
	tr.AddFolder("folder")
	tr.AddChat("chat")
	tr.rebuildFlat()

	if len(tr.flat) != 2 {
		t.Errorf("expected 2 visible items, got %d", len(tr.flat))
	}
}

func TestFlatListWithChildren(t *testing.T) {
	tr := New()
	tr.AddFolder("parent")
	parent := tr.root[0]
	parent.AddChild(&Item{Name: "child", IsFolder: false})
	parent.Expanded = true
	tr.rebuildFlat()

	if len(tr.flat) != 2 {
		t.Errorf("expected 2 visible items (parent + child), got %d", len(tr.flat))
	}

	// Check parent tracking
	child := tr.flat[1]
	if child.parent != parent {
		t.Error("expected child.parent to be parent")
	}
	if child.Depth() != 1 {
		t.Errorf("expected child depth=1, got %d", child.Depth())
	}
}

func TestToggleFolder(t *testing.T) {
	tr := New()
	tr.AddFolder("folder")
	f := tr.root[0]
	f.AddChild(&Item{Name: "child"})

	tr.rebuildFlat()
	if len(tr.flat) != 2 {
		t.Errorf("expected 2 items before toggle, got %d", len(tr.flat))
	}

	tr.ToggleFolder(f)
	if f.Expanded {
		t.Error("expected expanded=false after toggle")
	}
	if len(tr.flat) != 1 {
		t.Errorf("expected 1 item after collapse, got %d", len(tr.flat))
	}
}

func TestSelectNextPrev(t *testing.T) {
	tr := New()
	tr.AddFolder("a")
	tr.AddFolder("b")
	tr.AddFolder("c")

	tr.selected = -1
	tr.SelectNext()
	if tr.selected != 0 {
		t.Errorf("expected selected=0 after next from -1, got %d", tr.selected)
	}

	tr.SelectNext()
	if tr.selected != 1 {
		t.Errorf("expected selected=1 after next, got %d", tr.selected)
	}

	tr.SelectNext()
	if tr.selected != 2 {
		t.Errorf("expected selected=2 after second next, got %d", tr.selected)
	}

	// Should not go past end
	tr.SelectNext()
	if tr.selected != 2 {
		t.Errorf("expected selected=2 at end, got %d", tr.selected)
	}

	tr.SelectPrev()
	if tr.selected != 1 {
		t.Errorf("expected selected=1 after prev, got %d", tr.selected)
	}
}

func TestSelectPrevFromMinusOne(t *testing.T) {
	tr := New()
	tr.AddFolder("a")
	tr.selected = -1
	tr.SelectPrev()
	if tr.selected != 0 {
		t.Errorf("expected selected=0 after prev from -1, got %d", tr.selected)
	}
}

func TestView(t *testing.T) {
	tr := New()
	tr.AddFolder("folder")
	tr.AddChat("chat")

	view := tr.View(40, 10)
	if view == "" {
		t.Error("expected non-empty view")
	}
}

func TestIconFolder(t *testing.T) {
	f := &Item{Name: "test", IsFolder: true, Expanded: true}
	if f.Icon() != "- 📂" {
		t.Errorf("expanded folder: expected - 📂, got %s", f.Icon())
	}

	f.Expanded = false
	if f.Icon() != "+ 📁" {
		t.Errorf("collapsed folder: expected + 📁, got %s", f.Icon())
	}
}

func TestIconChat(t *testing.T) {
	c := &Item{Name: "chat", IsFolder: false}
	if c.Icon() != "  💬" {
		t.Errorf("expected   💬, got %s", c.Icon())
	}
}

func TestItemPath(t *testing.T) {
	rootChat := &Item{Name: "RootChat", IsFolder: false}
	if got := rootChat.Path(); len(got) != 0 {
		t.Errorf("root chat Path() = %v, want empty", got)
	}

	folder := &Item{Name: "Проекты", IsFolder: true}
	childFolder := &Item{Name: "Frontend", IsFolder: true}
	folder.AddChild(childFolder)
	chat := &Item{Name: "My Chat", IsFolder: false}
	childFolder.AddChild(chat)

	expected := []string{"Проекты", "Frontend"}
	got := chat.Path()
	if len(got) != len(expected) {
		t.Fatalf("Path() length = %d, want %d", len(got), len(expected))
	}
	for i, v := range expected {
		if got[i] != v {
			t.Errorf("Path()[%d] = %q, want %q", i, got[i], v)
		}
	}
}

func TestDepth(t *testing.T) {
	root := &Item{Name: "root", IsFolder: true}
	child := &Item{Name: "child"}
	root.AddChild(child)

	if root.Depth() != 0 {
		t.Errorf("root depth: expected 0, got %d", root.Depth())
	}
	if child.Depth() != 1 {
		t.Errorf("child depth: expected 1, got %d", child.Depth())
	}
}

func TestDeleteItem(t *testing.T) {
	tr := New()
	tr.AddFolder("parent")
	parent := tr.root[0]
	parent.AddChild(&Item{Name: "child"})
	parent.Expanded = true
	tr.rebuildFlat()

	if len(tr.flat) != 2 {
		t.Fatalf("expected 2 items, got %d", len(tr.flat))
	}

	tr.deleteItem(parent.Children[0])
	if len(parent.Children) != 0 {
		t.Errorf("expected 0 children, got %d", len(parent.Children))
	}
	if len(tr.flat) != 1 {
		t.Errorf("expected 1 flat item after delete, got %d", len(tr.flat))
	}
}

func TestRenameItem(t *testing.T) {
	tr := New()
	tr.AddFolder("old")
	if err := tr.renameItem(tr.root[0], "new"); err != nil {
		t.Fatalf("renameItem: %v", err)
	}
	if tr.root[0].Name != "new" {
		t.Errorf("expected name 'new', got %q", tr.root[0].Name)
	}
}

func TestRenameRejectsSlugConflict(t *testing.T) {
	tr := New()
	tr.AddChat("Alpha")
	tr.AddChat("other")

	if err := tr.renameItem(tr.root[1], " alpha "); err == nil {
		t.Fatal("expected slug conflict")
	}
	if tr.root[1].Name != "other" {
		t.Fatalf("conflicting rename changed name to %q", tr.root[1].Name)
	}
}

func TestRenameCallbackRunsBeforeTreeMutation(t *testing.T) {
	tr := New()
	tr.AddChat("old")
	called := false
	tr.SetOnRename(func(item *Item, newName string) error {
		called = true
		if item.Name != "old" {
			t.Errorf("callback saw mutated name %q", item.Name)
		}
		if newName != "new" {
			t.Errorf("callback saw name %q", newName)
		}
		return nil
	})

	if err := tr.renameItem(tr.root[0], "new"); err != nil {
		t.Fatalf("renameItem: %v", err)
	}
	if !called || tr.root[0].Name != "new" {
		t.Fatalf("rename callback or mutation missing: called=%v name=%q", called, tr.root[0].Name)
	}
}

func TestRenameErrorKeepsModalOpen(t *testing.T) {
	tr := New()
	tr.AddChat("old")
	tr.SetOnRename(func(*Item, string) error {
		return fmt.Errorf("migration failed")
	})
	tr.startRename(tr.root[0])
	tr.inputValue = "new"
	tr.confirmInput()

	if tr.root[0].Name != "old" || !tr.modalActive || tr.inputError != "migration failed" {
		t.Fatalf("rename error changed state: name=%q modal=%v error=%q", tr.root[0].Name, tr.modalActive, tr.inputError)
	}
}

func TestF2StartsRenameForSelectedItem(t *testing.T) {
	tr := New()
	tr.AddTerminal("terminal")
	tr.selected = 0

	tr.handleKey(tea.KeyMsg{Type: tea.KeyF2})

	if !tr.inputMode || tr.inputValue != "terminal" {
		t.Fatalf("F2 did not open prefilled rename modal: mode=%v value=%q", tr.inputMode, tr.inputValue)
	}
}

func TestInputMode(t *testing.T) {
	tr := New()
	called := false
	tr.startInput("Test:", func(name string) {
		called = true
		if name != "hello" {
			t.Errorf("expected 'hello', got %q", name)
		}
	})

	if !tr.inputMode {
		t.Error("expected inputMode=true")
	}

	tr.inputValue = "hello"
	tr.confirmInput()

	if !called {
		t.Error("expected callback to be called")
	}
	if tr.inputMode {
		t.Error("expected inputMode=false after confirm")
	}
}

func TestDomainUsesSessionPathConvention(t *testing.T) {
	tr := New()
	tr.AddFolder("Projects")
	folder := tr.Root()[0]
	if got := folder.Domain(""); got != "projects" {
		t.Fatalf("default domain = %q, want projects", got)
	}
	if got := folder.Domain("Проект Ω"); got != "proekt-ω__projects" {
		t.Fatalf("unicode profile domain = %q, want proekt-ω__projects", got)
	}
}

func TestAddChildFolder(t *testing.T) {
	tr := New()
	tr.AddFolder("parent")
	parent := tr.root[0]
	tr.addChildFolder(parent, "child")

	if len(parent.Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(parent.Children))
	}
	if !parent.Children[0].IsFolder {
		t.Error("expected child to be a folder")
	}
	if parent.Children[0].parent != parent {
		t.Error("expected child's parent to be parent")
	}
}

func TestMoveItemIntoFolder(t *testing.T) {
	tr := New()
	tr.AddFolder("Work")
	tr.AddChat("Notes")

	folder := tr.root[0]
	chat := tr.root[1]

	tr.moveItem(chat, folder)

	if len(tr.root) != 1 {
		t.Fatalf("expected 1 root item after move, got %d", len(tr.root))
	}
	if len(folder.Children) != 1 {
		t.Fatalf("expected 1 child in folder, got %d", len(folder.Children))
	}
	if folder.Children[0] != chat {
		t.Error("expected chat to be inside folder")
	}
	if chat.parent != folder {
		t.Error("expected chat's parent to be folder")
	}
}

func TestMoveItemAfterChat(t *testing.T) {
	tr := New()
	tr.AddChat("A")
	tr.AddChat("B")
	tr.AddChat("C")

	// Move A after C.
	tr.moveItem(tr.root[0], tr.root[2])

	if len(tr.root) != 3 {
		t.Fatalf("expected 3 root items, got %d", len(tr.root))
	}
	// Order should be: B, C, A
	if tr.root[0].Name != "B" {
		t.Errorf("expected root[0]=B, got %s", tr.root[0].Name)
	}
	if tr.root[1].Name != "C" {
		t.Errorf("expected root[1]=C, got %s", tr.root[1].Name)
	}
	if tr.root[2].Name != "A" {
		t.Errorf("expected root[2]=A, got %s", tr.root[2].Name)
	}
}

func TestMoveItemSelfNoop(t *testing.T) {
	tr := New()
	tr.AddChat("X")
	tr.AddChat("Y")

	// Moving item to itself should be a no-op.
	tr.moveItem(tr.root[0], tr.root[0])

	if len(tr.root) != 2 {
		t.Fatalf("expected 2 root items, got %d", len(tr.root))
	}
	if tr.root[0].Name != "X" {
		t.Errorf("expected root[0]=X, got %s", tr.root[0].Name)
	}
}

func TestMoveItemNilTargetNoop(t *testing.T) {
	tr := New()
	tr.AddChat("X")

	tr.moveItem(tr.root[0], nil)

	if len(tr.root) != 1 {
		t.Fatalf("expected 1 root item, got %d", len(tr.root))
	}
}

func TestMoveItemIntoDescendantNoop(t *testing.T) {
	tr := New()
	tr.AddFolder("outer")
	outer := tr.root[0]
	tr.addChildFolder(outer, "inner")
	inner := outer.Children[0]
	tr.addChildChat(inner, "chat")
	chat := inner.Children[0]

	tr.moveItem(outer, inner)
	tr.moveItem(outer, chat)

	if tr.root[0] != outer || len(outer.Children) != 1 || outer.Children[0] != inner {
		t.Fatalf("descendant move mutated tree: root=%v children=%v", tr.root, outer.Children)
	}
	if inner.Children[0] != chat || chat.parent != inner {
		t.Fatal("descendant move changed child parent")
	}
}

func TestMoveItemBeforeCallbackFailureLeavesTreeUntouched(t *testing.T) {
	tr := New()
	tr.AddChat("chat")
	tr.AddFolder("folder")
	chat := tr.root[0]
	folder := tr.root[1]
	called := false
	tr.SetOnBeforeItemMoved(func(_ *Item, _ *Item) (func() error, error) {
		called = true
		return nil, errors.New("migration failed")
	})

	tr.moveItem(chat, folder)

	if !called {
		t.Fatal("expected pre-move callback")
	}
	if chat.parent != nil || tr.root[0] != chat || len(folder.Children) != 0 {
		t.Fatal("tree changed after rejected migration")
	}
}

func TestMoveSelectedDownBasic(t *testing.T) {
	tr := New()
	tr.AddChat("A")
	tr.AddChat("B")
	tr.AddChat("C")
	// Select A (index 0), move down.
	tr.selected = 0
	if !tr.MoveSelectedDown() {
		t.Fatal("MoveSelectedDown returned false")
	}
	if tr.root[0].Name != "B" || tr.root[1].Name != "A" || tr.root[2].Name != "C" {
		t.Fatalf("order = %q, want [B A C]", names(tr.root))
	}
	// Selection stays on moved item.
	if tr.SelectedItem() == nil || tr.SelectedItem().Name != "A" {
		t.Fatalf("selected = %v, want A", tr.SelectedItem())
	}
}

func TestMoveSelectedDownBottomNoop(t *testing.T) {
	tr := New()
	tr.AddChat("A")
	tr.AddChat("B")
	tr.selected = 1 // B
	if tr.MoveSelectedDown() {
		t.Fatal("expected MoveSelectedDown to no-op at bottom")
	}
	if tr.root[0].Name != "A" || tr.root[1].Name != "B" {
		t.Fatalf("order changed: %q", names(tr.root))
	}
}

func TestMoveSelectedUpBasic(t *testing.T) {
	tr := New()
	tr.AddChat("A")
	tr.AddChat("B")
	tr.AddChat("C")
	tr.selected = 2 // C
	if !tr.MoveSelectedUp() {
		t.Fatal("MoveSelectedUp returned false")
	}
	if tr.root[0].Name != "A" || tr.root[1].Name != "C" || tr.root[2].Name != "B" {
		t.Fatalf("order = %q, want [A C B]", names(tr.root))
	}
}

func TestMoveSelectedUpTopNoop(t *testing.T) {
	tr := New()
	tr.AddChat("A")
	tr.AddChat("B")
	tr.selected = 0 // A
	if tr.MoveSelectedUp() {
		t.Fatal("expected MoveSelectedUp to no-op at top")
	}
}

func TestMoveSelectedAcrossFolders(t *testing.T) {
	tr := New()
	tr.AddFolder("F1")
	tr.AddChat("out1") // root[1]
	tr.AddFolder("F2")
	f1 := tr.root[0]
	out1 := tr.root[1]
	f2 := tr.root[2]
	// Manually set parent for the root items (root items have parent=nil).
	_ = f1
	_ = f2
	_ = out1
	// Add a child to F1 to verify out1 doesn't get nested.
	f1.AddChild(&Item{Name: "inner", IsFolder: false})
	tr.rebuildFlat()
	// Select "out1" (root chat), move it up.
	idxOut := -1
	for i, it := range tr.flat {
		if it.Name == "out1" {
			idxOut = i
			break
		}
	}
	if idxOut < 0 {
		t.Fatal("out1 not found in flat list")
	}
	tr.selected = idxOut
	if !tr.MoveSelectedUp() {
		t.Fatal("MoveSelectedUp should swap root items")
	}
	// out1 was root[1], F1 was root[0] → after swap: root = [out1, F1, F2].
	if tr.root[0].Name != "out1" {
		t.Fatalf("root[0] = %q, want out1", tr.root[0].Name)
	}
	if tr.root[1].Name != "F1" {
		t.Fatalf("root[1] = %q, want F1", tr.root[1].Name)
	}
	if tr.root[2].Name != "F2" {
		t.Fatalf("root[2] = %q, want F2", tr.root[2].Name)
	}
}

// names returns the names of a slice of *Item for error messages.
func names(items []*Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Name
	}
	return out
}

func TestMoveItemIntoFolderTarget(t *testing.T) {
	tr := New()
	tr.AddChat("A")
	tr.AddFolder("F")
	tr.AddChat("B")
	a := tr.root[0]
	f := tr.root[1]
	// Move A into F (folder target → append to children).
	tr.moveItem(a, f)
	if len(f.Children) != 1 || f.Children[0].Name != "A" {
		t.Fatalf("expected F to contain A, got %v", names(f.Children))
	}
	if len(tr.root) != 2 || tr.root[0].Name != "F" {
		t.Fatalf("root order broken: %v", names(tr.root))
	}
}

func TestMoveItemAfterFolderTarget(t *testing.T) {
	tr := New()
	tr.AddFolder("F1")
	tr.AddChat("A")
	f1 := tr.root[0]
	// Move F1 after A (chat target → insert after).
	tr.moveItem(f1, tr.root[1])
	if tr.root[0].Name != "A" || tr.root[1].Name != "F1" {
		t.Fatalf("expected [A F1], got %v", names(tr.root))
	}
}

func TestOnItemMovedAcrossParent(t *testing.T) {
	tr := New()
	tr.AddChat("A")
	tr.AddFolder("F")
	tr.AddChat("B")
	a := tr.root[0]
	f := tr.root[1]

	var got struct {
		oldID string
		newID string
	}
	tr.SetOnItemMoved(func(_ *Item, oldID, newID string) {
		got.oldID = oldID
		got.newID = newID
	})

	// A is at root → moving into F changes its parent, session id must change.
	tr.moveItem(a, f)
	if got.oldID == "" || got.newID == "" {
		t.Fatal("onItemMoved was not called")
	}
	if got.oldID == got.newID {
		t.Fatalf("expected session id change, got %q == %q", got.oldID, got.newID)
	}
	if got.newID != "f.a" {
		t.Fatalf("expected newID=f.a, got %q", got.newID)
	}
}

func TestOnItemMovedSameParentNoCallback(t *testing.T) {
	tr := New()
	tr.AddChat("A")
	tr.AddChat("B")
	tr.AddChat("C")
	called := 0
	tr.SetOnItemMoved(func(_ *Item, _, _ string) { called++ })

	// Move A down (same parent) → session id stays the same.
	tr.moveItem(tr.root[0], tr.root[2])
	if called != 0 {
		t.Fatalf("onItemMoved called %d times for same-parent move", called)
	}
}

func TestBoundPathPersistence(t *testing.T) {
	dir := t.TempDir()
	tr := New()
	tr.AddFolder("project")
	f := tr.root[0]
	f.SetBoundPath(dir)
	if f.BoundStale {
		t.Fatalf("freshly bound dir should not be stale")
	}
	tr.rebuildFlat()
	tr.autoSave()

	// Reload from disk.
	tr2 := New()
	tr2.Profile = tr.Profile
	tr2.Profile = ""
	if err := tr2.LoadState(); err != nil {
		t.Fatal(err)
	}
	if len(tr2.root) != 1 {
		t.Fatalf("expected 1 root after reload, got %d", len(tr2.root))
	}
	if tr2.root[0].BoundPath != dir {
		t.Fatalf("BoundPath lost on reload: got %q", tr2.root[0].BoundPath)
	}
}

func TestBoundPathStaleDetection(t *testing.T) {
	tr := New()
	tr.AddFolder("project")
	f := tr.root[0]
	f.SetBoundPath("/this/path/should/never/exist/in/tests")
	if !f.BoundStale {
		t.Fatal("non-existent path should be marked stale")
	}
}

func TestEffectiveBoundPathAncestor(t *testing.T) {
	tr := New()
	tr.AddFolder("parent")
	parent := tr.root[0]
	tr.addChildFolder(parent, "child")
	child := parent.Children[0]
	parent.SetBoundPath("/tmp")
	if got := child.EffectiveBoundPath(); got != "/tmp" {
		t.Fatalf("child should inherit parent's bound path, got %q", got)
	}
	child.SetBoundPath("/tmp/child")
	if got := child.EffectiveBoundPath(); got != "/tmp/child" {
		t.Fatalf("child's own path should win, got %q", got)
	}
	// Chat in child should also see the child's bound path.
	tr.addChildChat(child, "chat1")
	if len(child.Children) == 0 {
		t.Fatal("chat was not added to child")
	}
	chat := child.Children[0]
	if got := chat.EffectiveBoundPath(); got != "/tmp/child" {
		t.Fatalf("chat should see child bound path, got %q", got)
	}
}

func TestBindUnbindRoundtrip(t *testing.T) {
	tr := New()
	tr.AddFolder("f")
	f := tr.root[0]
	dir := t.TempDir()
	tr.bindFolder(f, dir)
	if f.BoundPath != dir {
		t.Fatalf("bindFolder did not set BoundPath: %q", f.BoundPath)
	}
	if f.BoundStale {
		t.Fatal("freshly bound dir should not be stale")
	}
	tr.unbindFolder(f)
	if f.BoundPath != "" {
		t.Fatalf("unbindFolder did not clear BoundPath: %q", f.BoundPath)
	}
}

func TestMoveSelectedOutFromNestedFolder(t *testing.T) {
	tr := New()
	tr.AddFolder("outer")
	outer := tr.root[0]
	tr.addChildFolder(outer, "inner")
	inner := outer.Children[0]
	tr.AddChat("rootChat")

	// Select inner (which is inside outer).
	for i, f := range tr.flat {
		if f == inner {
			tr.selected = i
			break
		}
	}

	if !tr.MoveSelectedOut() {
		t.Fatal("MoveSelectedOut returned false on nested folder")
	}
	if inner.parent != nil {
		t.Fatalf("expected inner.parent == nil after move out, got %v", inner.parent)
	}
	if len(outer.Children) != 0 {
		t.Fatalf("expected outer to be empty, got %d children", len(outer.Children))
	}
	if len(tr.root) != 3 {
		t.Fatalf("expected 3 root items (outer, rootChat, inner), got %d", len(tr.root))
	}
	// Inner should sit directly after outer in root.
	foundInnerAt := -1
	for i, r := range tr.root {
		if r == inner {
			foundInnerAt = i
			break
		}
	}
	if foundInnerAt != 1 {
		t.Fatalf("expected inner at root[1], got %d", foundInnerAt)
	}
}

func TestMoveSelectedOutFromRootIsNoop(t *testing.T) {
	tr := New()
	tr.AddFolder("f")
	tr.selected = 0
	if tr.MoveSelectedOut() {
		t.Fatal("MoveSelectedOut should no-op when selected item is at root")
	}
	if len(tr.root) != 1 || len(tr.root[0].Children) != 0 {
		t.Fatalf("tree mutated unexpectedly: %+v", tr.root)
	}
}

func TestMoveSelectedOutRunsMigrationBeforeMutation(t *testing.T) {
	tr := New()
	tr.AddFolder("outer")
	outer := tr.root[0]
	tr.addChildFolder(outer, "inner")
	inner := outer.Children[0]
	tr.addChildChat(inner, "chat")
	chat := inner.Children[0]
	tr.rebuildFlat()
	tr.reselectItem(chat)

	called := false
	var gotParent *Item
	tr.SetOnBeforeItemMoved(func(_ *Item, newParent *Item) (func() error, error) {
		called = true
		gotParent = newParent
		return nil, nil
	})
	tr.MoveSelectedOut()

	if !called || gotParent != outer {
		t.Fatalf("pre-move callback parent=%v called=%v, want outer", gotParent, called)
	}
	if chat.parent != outer {
		t.Fatalf("chat parent = %v, want outer", chat.parent)
	}
}

func TestSaveStateKeepsPreviousStateWhenAtomicWriteFails(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	tr := New()
	tr.AddChat("before")
	if err := tr.SaveState(); err != nil {
		t.Fatalf("initial SaveState: %v", err)
	}
	path := paths.StatePath("")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read initial state: %v", err)
	}

	originalWrite := writeStateTemp
	t.Cleanup(func() { writeStateTemp = originalWrite })
	writeStateTemp = func(*os.File, []byte) error { return errors.New("injected write failure") }
	tr.AddChat("after")

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read preserved state: %v", err)
	}
	var state TreeState
	if err := json.Unmarshal(got, &state); err != nil {
		t.Fatalf("preserved state is invalid JSON: %v", err)
	}
	if string(got) != string(before) {
		t.Fatalf("state changed after failed atomic write:\nold=%s\nnew=%s", before, got)
	}
}

func TestSaveStateRenameFailureKeepsPreviousState(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	tr := New()
	tr.AddChat("before")
	if err := tr.SaveState(); err != nil {
		t.Fatalf("initial SaveState: %v", err)
	}
	path := paths.StatePath("")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read initial state: %v", err)
	}

	originalRename := renameState
	t.Cleanup(func() { renameState = originalRename })
	renameState = func(_, _ string) error { return errors.New("injected rename failure") }
	tr.AddChat("after")

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read preserved state: %v", err)
	}
	if string(got) != string(before) {
		t.Fatalf("state changed after failed atomic rename:\nold=%s\nnew=%s", before, got)
	}
}

func TestFolderContextMenuIncludesMoveCommands(t *testing.T) {
	tr := New()
	tr.AddFolder("f")
	f := tr.root[0]
	items := tr.buildContextMenuItems(f)
	var foundUp, foundDown, foundOut bool
	for _, m := range items {
		switch m.Name {
		case "Move up":
			foundUp = true
		case "Move down":
			foundDown = true
		case "Move out":
			foundOut = true
		}
	}
	if !foundUp {
		t.Fatal("folder context menu missing Move up")
	}
	if !foundDown {
		t.Fatal("folder context menu missing Move down")
	}
	if !foundOut {
		t.Fatal("folder context menu missing Move out")
	}
}

func TestFolderClickRequiresDoubleClick(t *testing.T) {
	tr := New()
	tr.AddFolder("f") // AddFolder creates an Expanded=true folder
	// Start collapsed to detect toggles.
	tr.root[0].Expanded = false
	tr.rebuildFlat()
	tr.width = 40
	tr.height = 12

	// Y=1 because flatIndexAt without a prior render uses screenY-1 as the
	// flat index (row 0 is the header).
	press := tea.MouseMsg{X: 5, Y: 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
	release := tea.MouseMsg{X: 5, Y: 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease}

	// First click: focus only, no toggle.
	tr.handleLeftPress(press)
	if tr.selected != 0 {
		t.Fatalf("expected selected=0 after first press, got %d", tr.selected)
	}
	if tr.prevSelectedIdx != -1 {
		t.Fatalf("expected prevSelectedIdx=-1 after first press, got %d", tr.prevSelectedIdx)
	}
	tr.handleLeftRelease(release)
	if tr.root[0].Expanded {
		t.Fatal("first click should not toggle folder (got expanded=true)")
	}

	// Second click on the same folder: toggle.
	tr.handleLeftPress(press)
	if tr.prevSelectedIdx != 0 {
		t.Fatalf("expected prevSelectedIdx=0 on second press, got %d", tr.prevSelectedIdx)
	}
	tr.handleLeftRelease(release)
	if !tr.root[0].Expanded {
		t.Fatal("second click should toggle folder (got expanded=false)")
	}

	// Third click on same folder: toggle back off.
	tr.handleLeftPress(press)
	tr.handleLeftRelease(release)
	if tr.root[0].Expanded {
		t.Fatal("third click should toggle folder back off (got expanded=true)")
	}
}

func TestFolderClickOnDifferentRowDoesNotToggle(t *testing.T) {
	tr := New()
	tr.AddFolder("a")
	tr.AddFolder("b")
	tr.rebuildFlat()
	tr.width = 40
	tr.height = 12

	// Click on folder a (row 1, since flatIndexAt uses screenY-1 without a render).
	tr.handleLeftPress(tea.MouseMsg{X: 5, Y: 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	tr.handleLeftRelease(tea.MouseMsg{X: 5, Y: 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	if tr.selected != 0 || tr.root[0].Expanded == false {
		t.Fatalf("after first click on a: selected=%d expanded[0]=%v", tr.selected, tr.root[0].Expanded)
	}
	// Click on folder b (row 2). Should NOT toggle b — only moves focus.
	tr.handleLeftPress(tea.MouseMsg{X: 5, Y: 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	tr.handleLeftRelease(tea.MouseMsg{X: 5, Y: 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	if tr.selected != 1 {
		t.Fatalf("expected selected=1 after click on b, got %d", tr.selected)
	}
	if !tr.root[1].Expanded {
		t.Fatal("click on a different folder should not toggle it")
	}
}

// TestFolderActiveIndicator verifies that a folder containing an active chat
// is reported as having an active descendant.
func TestFolderActiveIndicator(t *testing.T) {
	tree := New()
	tree.AddFolder("folder")
	tree.AddChat("chat")
	f := tree.Folder("folder")
	c := tree.root[1]
	f.Children = append(f.Children, c)

	tree.SetActiveSessions(map[string]struct{}{
		tree.sessionIDOf(c): {},
	})

	if !tree.HasActiveDescendant(f) {
		t.Fatalf("expected folder to have active descendant")
	}

	status := tree.statusString(f)
	if status != "●" {
		t.Fatalf("expected folder status '●', got %q", status)
	}
}

// TestFolderActiveIndicatorNested verifies nested folder detection.
func TestFolderActiveIndicatorNested(t *testing.T) {
	tree := New()
	tree.AddFolder("outer")
	tree.AddFolder("inner")
	tree.AddChat("chat")
	outer := tree.Folder("outer")
	inner := tree.Folder("inner")
	c := tree.root[2]
	inner.Children = append(inner.Children, c)
	outer.Children = append(outer.Children, inner)

	tree.SetActiveSessions(map[string]struct{}{
		tree.sessionIDOf(c): {},
	})

	if !tree.HasActiveDescendant(outer) {
		t.Fatalf("expected outer folder to have active descendant")
	}
	if !tree.HasActiveDescendant(inner) {
		t.Fatalf("expected inner folder to have active descendant")
	}
}

// TestFolderNoActiveDescendant verifies that an empty folder has no indicator.
func TestFolderNoActiveDescendant(t *testing.T) {
	tree := New()
	tree.AddFolder("folder")
	tree.AddChat("chat")
	f := tree.Folder("folder")

	tree.SetActiveSessions(map[string]struct{}{
		tree.sessionIDOf(tree.root[1]): {},
	})

	if tree.HasActiveDescendant(f) {
		t.Fatalf("expected empty folder to have no active descendant")
	}

	status := tree.statusString(f)
	if status != "" {
		t.Fatalf("expected empty folder status, got %q", status)
	}
}

// TestActiveSessionsPersistence verifies that active sessions are saved and
// restored with the tree state.
func TestActiveSessionsPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("AI_DATA_HOME", tmpDir)
	os.Setenv("AI_PROFILE", "active-test")
	defer os.Unsetenv("AI_DATA_HOME")
	defer os.Unsetenv("AI_PROFILE")

	tree := New()
	tree.Profile = "active-test"
	tree.AddChat("chat-one")
	tree.AddChat("chat-two")

	sessions := map[string]struct{}{
		"active-test__chat-one": {},
		"active-test__chat-two": {},
	}
	tree.SetActiveSessions(sessions)

	// Load into a fresh tree and verify active sessions are restored.
	fresh := New()
	fresh.Profile = "active-test"
	if err := fresh.LoadState(); err != nil {
		t.Fatalf("load state: %v", err)
	}
	ids := fresh.ActiveSessionIDs()
	if len(ids) != 2 {
		t.Fatalf("expected 2 active sessions, got %d: %v", len(ids), ids)
	}
	for _, id := range ids {
		if _, ok := sessions[id]; !ok {
			t.Fatalf("unexpected session id %q", id)
		}
	}
}

// TestFindItemBySessionID locates an item by its full session ID.
func TestFindItemBySessionID(t *testing.T) {
	tree := New()
	tree.Profile = "find-test"
	tree.AddFolder("folder")
	tree.AddChat("chat-one")
	chat := tree.root[1]

	found := tree.FindItemBySessionID("find-test__chat-one")
	if found != chat {
		t.Fatalf("expected to find chat item, got %+v", found)
	}

	notFound := tree.FindItemBySessionID("find-test__missing")
	if notFound != nil {
		t.Fatalf("expected nil for missing session, got %+v", notFound)
	}
}

// TestFolderContextMenuIncludesSort is the regression for the new "Sort
// A→Z" entry that lets the user reorder a folder's children alphabetically
// without losing drag-and-drop intent on every reload.
func TestFolderContextMenuIncludesSort(t *testing.T) {
	tr := New()
	tr.AddFolder("f")
	items := tr.buildContextMenuItems(tr.root[0])
	var found bool
	for _, m := range items {
		if m.Name == "Sort A→Z" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("folder context menu missing Sort A→Z")
	}
}

// TestSortChildrenAlphabetically checks the ordering rule and the
// "folders before chats before terminals" invariant. The mix deliberately
// has the wrong initial order to make sure the call actually moves things.
func TestSortChildrenAlphabetically(t *testing.T) {
	tr := New()
	tr.AddFolder("f")
	f := tr.root[0]

	// Build a mixed folder in reverse alphabetical order so every item
	// has to move.
	f.AddChild(&Item{Name: "zulu-chat"})
	f.AddChild(&Item{Name: "alpha-folder", IsFolder: true})
	f.AddChild(&Item{Name: "mike-terminal", IsTerminal: true})
	f.AddChild(&Item{Name: "bravo-chat"})
	f.AddChild(&Item{Name: "yankee-folder", IsFolder: true})

	tr.sortChildrenAlphabetically(f)

	wantNames := []string{"alpha-folder", "yankee-folder", "bravo-chat", "zulu-chat", "mike-terminal"}
	if len(f.Children) != len(wantNames) {
		t.Fatalf("children count = %d, want %d", len(f.Children), len(wantNames))
	}
	for i, want := range wantNames {
		if got := f.Children[i].Name; got != want {
			t.Errorf("children[%d] = %q, want %q", i, got, want)
		}
	}
}

// TestSortChildrenAlphabeticallyIsCaseInsensitive confirms that case
// doesn't break ordering — "Bravo" and "alpha" must end up next to each
// other in the right spot, not split by ASCII case.
func TestSortChildrenAlphabeticallyIsCaseInsensitive(t *testing.T) {
	tr := New()
	tr.AddFolder("f")
	f := tr.root[0]

	f.AddChild(&Item{Name: "Zeta"})
	f.AddChild(&Item{Name: "alpha"})
	f.AddChild(&Item{Name: "Mike"})

	tr.sortChildrenAlphabetically(f)

	wantNames := []string{"alpha", "Mike", "Zeta"}
	for i, want := range wantNames {
		if got := f.Children[i].Name; got != want {
			t.Errorf("children[%d] = %q, want %q", i, got, want)
		}
	}
}

// TestSortChildrenAlphabeticallyNoopForEmptyOrSingle makes sure the
// function doesn't panic or rebuild on trivial inputs.
func TestSortChildrenAlphabeticallyNoopForEmptyOrSingle(t *testing.T) {
	tr := New()
	tr.AddFolder("empty")
	tr.sortChildrenAlphabetically(tr.root[0])
	if len(tr.root[0].Children) != 0 {
		t.Errorf("empty folder should stay empty, got %d children", len(tr.root[0].Children))
	}

	tr.AddFolder("solo")
	tr.root[1].AddChild(&Item{Name: "only"})
	tr.sortChildrenAlphabetically(tr.root[1])
	if len(tr.root[1].Children) != 1 || tr.root[1].Children[0].Name != "only" {
		t.Errorf("single-child folder disturbed: %+v", tr.root[1].Children)
	}
}

// TestRootContextMenuIncludesSort confirms the empty-selection context
// menu exposes both root sorting modes.
func TestRootContextMenuIncludesSort(t *testing.T) {
	tr := New()
	items := tr.buildContextMenuItems(nil)
	found := map[string]bool{}
	for _, m := range items {
		if m.Name == "Sort by name" || m.Name == "Sort by type" {
			found[m.Name] = true
		}
	}
	if !found["Sort by name"] || !found["Sort by type"] {
		t.Fatalf("root context menu missing sort modes: %v", found)
	}
}

// TestSortRootAlphabetically exercises the root-level entry point: a
// deliberately unsorted mix of folders, chats and terminals must end up
// grouped and A→Z, mirroring the children version.
func TestSortRootAlphabetically(t *testing.T) {
	tr := New()
	tr.AddChat("zulu")
	tr.AddFolder("alpha-folder")
	tr.AddTerminal("mike")
	tr.AddChat("bravo")
	tr.AddFolder("yankee-folder")
	// Trivial sanity: only one chat is allowed at root per AddChat, but
	// multiple AddChat calls are supported — each appends. Confirm shape
	// before sort.
	if got := len(tr.root); got != 5 {
		t.Fatalf("setup: root has %d items, want 5", got)
	}

	tr.sortRootAlphabetically()

	wantOrder := []string{"alpha-folder", "yankee-folder", "bravo", "zulu", "mike"}
	if len(tr.root) != len(wantOrder) {
		t.Fatalf("root length = %d, want %d", len(tr.root), len(wantOrder))
	}
	for i, want := range wantOrder {
		if got := tr.root[i].Name; got != want {
			t.Errorf("root[%d] = %q, want %q", i, got, want)
		}
	}
}

// TestSortRootAlphabeticallyNoopForSmall makes sure the root-level helper
// doesn't panic or rebuild when there are 0 or 1 items.
func TestSortRootAlphabeticallyNoopForSmall(t *testing.T) {
	tr := New()
	tr.sortRootAlphabetically() // empty
	tr.AddChat("only")
	tr.sortRootAlphabetically() // single
	if len(tr.root) != 1 || tr.root[0].Name != "only" {
		t.Errorf("root disturbed: %+v", tr.root)
	}
}
