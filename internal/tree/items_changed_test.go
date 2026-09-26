package tree

import (
	"errors"
	"testing"
)

func TestItemsChangedCallbackRunsOnlyAfterCommittedMutations(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	tr := New()
	changes := 0
	tr.SetOnItemsChanged(func() { changes++ })

	chat, err := tr.CreateChat("Chat")
	if err != nil {
		t.Fatal(err)
	}
	folder, err := tr.CreateFolder("Projects")
	if err != nil {
		t.Fatal(err)
	}
	child, err := tr.CreateChildChat(folder, "Child")
	if err != nil {
		t.Fatal(err)
	}
	if changes != 3 {
		t.Fatalf("committed create callbacks = %d, want 3", changes)
	}

	if err := tr.RenameItem(child, "Renamed"); err != nil {
		t.Fatal(err)
	}
	if err := tr.MoveItemChecked(child, chat); err != nil {
		t.Fatal(err)
	}
	if err := tr.DeleteItem(child); err != nil {
		t.Fatal(err)
	}
	if changes != 6 {
		t.Fatalf("committed rename/move/delete callbacks = %d, want 6 total", changes)
	}

	tr.SetSaveStateFunc(func() error { return errors.New("injected pre-commit save failure") })
	if _, err := tr.CreateChat("Uncommitted"); err == nil {
		t.Fatal("injected save failure unexpectedly committed a chat")
	}
	if changes != 6 {
		t.Fatalf("failed mutation fired callback: count = %d, want 6", changes)
	}
}
