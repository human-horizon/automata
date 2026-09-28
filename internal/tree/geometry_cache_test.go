package tree

import "testing"

func TestRebuildFlatCachesBranchInfo(t *testing.T) {
	tr := New()
	folder, err := tr.CreateFolder("folder")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tr.CreateChildChat(folder, "chat"); err != nil {
		t.Fatal(err)
	}
	if len(tr.branchInfo) != len(tr.flat) {
		t.Fatalf("branch cache length = %d, flat = %d", len(tr.branchInfo), len(tr.flat))
	}
	if len(tr.branchInfo) < 2 || len(tr.branchInfo[1].continuationMask) != 1 {
		t.Fatalf("child branch info not cached: %#v", tr.branchInfo)
	}
}

func TestActionAtReusesCachedBranchInfo(t *testing.T) {
	tr := New()
	tr.width = 80
	tr.height = 24
	if _, err := tr.CreateChat("chat"); err != nil {
		t.Fatal(err)
	}
	tr.selected = 0
	tr.hoverIdx = 0
	tr.rowToFlat = []int{-1, 0}
	cache := tr.branchInfo
	if len(cache) != 1 {
		t.Fatalf("initial branch cache = %d", len(cache))
	}
	_ = tr.actionAt(0, 1)
	if len(tr.branchInfo) != 1 {
		t.Fatalf("actionAt invalidated branch cache: %d", len(tr.branchInfo))
	}
}
