package tree

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/HumanHorizon/automata/internal/paths"
	"github.com/HumanHorizon/automata/internal/slug"
	"github.com/google/uuid"
	warp "github.com/starframe-dev/warp"
)

// ItemType distinguishes the kinds of items that can appear in the tree.
type ItemType int

const (
	// ChatItem is a pi session backed by just-pi.
	ChatItem ItemType = iota
	// TerminalItem is a plain shell terminal.
	TerminalItem
	// FolderItem is a container for other items.
	FolderItem
)

// Item represents an element in the tree: a folder, a chat, or a terminal.
type Item struct {
	Name       string
	IsFolder   bool
	IsTerminal bool
	Archived   bool
	Children   []*Item
	Expanded   bool
	ID         string
	parent     *Item

	// CWD is the last known working directory for a terminal item.
	CWD string
	// CommandHistory holds the last commands entered in a terminal item.
	CommandHistory []string

	// BoundPath optionally anchors a folder to a real filesystem directory.
	// When set, folder previews show its contents and chats opened from the
	// folder (or its children) are spawned with --cwd <BoundPath>.
	BoundPath string `json:"bound_path,omitempty"`
	// BoundStale is computed at load time: true when BoundPath is set but
	// the directory no longer exists. Not persisted; recomputed on save.
	BoundStale bool `json:"-"`
}

// flatIndexAt returns the flat index displayed at the given screen row, or -1
// if the row is a header, archive separator, or out of bounds.
func (t *Tree) flatIndexAt(screenY int) int {
	if len(t.rowToFlat) == 0 {
		// View hasn't run yet (e.g. unit tests). Assume a single-line header
		// with no toolbar and no archive separators.
		idx := screenY - 1
		if idx < 0 || idx >= len(t.flat) {
			return -1
		}
		return idx
	}
	if screenY < 0 || screenY >= len(t.rowToFlat) {
		return -1
	}
	return t.rowToFlat[screenY]
}

// menuAction is a function that executes a context menu action.
type menuAction func()

// menuItem is an item in the context menu.
type menuItem struct {
	Name   string
	Action menuAction
}

// ItemSelectedMsg is emitted when any tree item is selected.
type ItemSelectedMsg struct {
	Item *Item
}

// ChatSelectedMsg is emitted when a chat or terminal (non-folder) item is selected.
// Deprecated: kept for compatibility; use ItemSelectedMsg.
type ChatSelectedMsg struct {
	Item *Item
}

// StopSessionMsg is emitted when the user clicks the stop button on an active
// chat or terminal item.
type StopSessionMsg struct {
	Item *Item
}

// FolderSelectedMsg is emitted when a folder item is selected (clicked).
type FolderSelectedMsg struct {
	Item *Item
}

// TreeCollapsedMsg is emitted when the tree panel is collapsed or expanded.
type TreeCollapsedMsg struct {
	Collapsed bool
}

// Tree is the tree widget. It implements warp.Panel.
type Tree struct {
	root     []*Item
	selected int // Index in the visible flat list
	scroll   int // Scroll offset
	flat     []*Item
	height   int // Last known view height
	width    int // Last known view width

	// Context menu (popover)
	popover *warp.Popover

	// Modal (input or confirmation) — directly managed by Tree
	modal       *warp.Modal
	modalActive bool

	// Modal input (for creating/renaming)
	inputMode   bool
	inputPrompt string
	inputValue  string
	inputCursor int               // rune cursor position within inputValue
	inputDone   func(name string) // called on Enter with the typed name

	// Delete confirmation mode
	confirmMode bool
	confirmItem *Item
	confirmYes  func()

	// Hover and inline action icons
	hoverIdx   int
	actionIcon string

	// Last processed motion timestamp. Used to throttle the all-motion
	// event flood (~one event per cursor pixel) down to ~30 FPS so the
	// update loop stays light even while the user is hovering.
	lastMotionAt time.Time

	// Drag & drop
	dragMode      bool
	dragItem      *Item
	dragTargetIdx int
	dragStartX    int
	dragStartY    int
	dragStarted   bool

	// Click-focus state: tracks the row index selected BEFORE the most recent
	// press, used to distinguish a first click (focus only) from a second
	// click on the already-focused folder (toggle).
	prevSelectedIdx int

	// rowToFlat maps screen row (0 = header) to the flat index displayed at
	// that row. -1 means the row is an archive separator or empty padding.
	rowToFlat []int

	// Collapsed indicates whether the left panel is collapsed to a narrow bar.
	Collapsed bool

	// Panel widths in characters (fixed, not fractional).
	treeWidth int
	planWidth int

	// Callback when a chat or terminal (non-folder) is selected
	onSelectChat func(*Item)

	// Callback when a folder is selected (clicked)
	onSelectFolder func(*Item)

	// Callback when the user requests to stop an active session
	onStopSession func(*Item)

	// Callback fired after an item is moved to a new parent. The arguments are
	// the bare session ID (no profile prefix) of the item before and after the
	// move. They are equal when the move did not change the session identity
	// (e.g. re-ordering siblings within the same parent).
	profile     string
	onItemMoved func(item *Item, oldSessionID, newSessionID string)

	// ActiveSessions holds the set of currently running session IDs (keyed by
	// the same identifier used to create the emulator).
	activeSessions map[string]struct{}

	// statusBadges maps sessionKey → single-emoji status indicator shown next
	// to chat names in the tree. Empty string means no badge. Driven from
	// outside by SetStatusBadges (typically populated by polling status.json
	// every couple of seconds).
	statusBadges map[string]string

	// NoColor disables all ANSI color output. When true, the tree renders in
	// plain text with no lipgloss styling. Automatically set when the
	// NO_COLOR environment variable is present.
	NoColor bool

	// Theme controls the color scheme. One of "auto" (default, detect from
	// terminal), "dark", "light", or "mono" (same as NoColor).
	Theme string

	// Profile name for state isolation (e.g. "ai" → ~/.automata/ai/state.json)
	Profile string
}

func generateID() string {
	return uuid.New().String()
}

// New creates an empty Tree.
func New() *Tree {
	return &Tree{
		selected:        -1,
		hoverIdx:        -1,
		height:          24, // Default until first render
		prevSelectedIdx: -1,
	}
}

// AddFolder adds a root-level folder and returns the tree for chaining.
func (t *Tree) AddFolder(name string) *Tree {
	item := &Item{
		Name:     name,
		IsFolder: true,
		Children: nil,
		Expanded: true,
		ID:       generateID(),
	}
	t.root = append(t.root, item)
	t.rebuildFlat()
	t.autoSave()
	return t
}

// AddChat adds a root-level chat and returns the tree for chaining.
func (t *Tree) AddChat(name string) *Tree {
	item := &Item{
		Name: name,
		ID:   generateID(),
	}
	t.root = append(t.root, item)
	t.rebuildFlat()
	t.autoSave()
	return t
}

// AddTerminal adds a root-level terminal and returns the tree for chaining.
func (t *Tree) AddTerminal(name string) *Tree {
	item := &Item{
		Name:       name,
		IsTerminal: true,
		ID:         generateID(),
	}
	t.root = append(t.root, item)
	t.rebuildFlat()
	t.autoSave()
	return t
}

// Folder finds a root-level folder by name. Returns nil if not found.
func (t *Tree) Folder(name string) *Item {
	for _, item := range t.root {
		if item.Name == name && item.IsFolder {
			return item
		}
	}
	return nil
}

// ItemAt returns the visible item at the given flat index, or nil.
func (t *Tree) ItemAt(idx int) *Item {
	if idx < 0 || idx >= len(t.flat) {
		return nil
	}
	return t.flat[idx]
}

// SelectedItem returns the currently selected visible item, or nil.
func (t *Tree) SelectedItem() *Item {
	if t.selected < 0 || t.selected >= len(t.flat) {
		return nil
	}
	return t.flat[t.selected]
}

// rebuildFlat rebuilds the flat visible list from the tree.
func (t *Tree) rebuildFlat() {
	t.flat = nil
	for _, item := range t.root {
		t.flatten(item, nil, 0)
	}
	t.clampSelection()
}

func (t *Tree) flatten(item *Item, parent *Item, depth int) {
	item.parent = parent
	t.flat = append(t.flat, item)

	if item.IsFolder && item.Expanded {
		for _, child := range item.Children {
			t.flatten(child, item, depth+1)
		}
	}
}

func (t *Tree) clampSelection() {
	if t.selected >= len(t.flat) {
		t.selected = len(t.flat) - 1
	}
	if len(t.flat) == 0 {
		t.selected = -1
	}
}

// ToggleFolder toggles the expanded state of a folder item.
func (t *Tree) ToggleFolder(item *Item) {
	if !item.IsFolder {
		return
	}
	item.Expanded = !item.Expanded
	t.rebuildFlat()
}

// MoveSelectedUp moves the selected item one position up among its siblings.
// Returns true if a move happened. The selected item stays selected after
// the swap (its flat index may change).
func (t *Tree) MoveSelectedUp() bool {
	sel := t.SelectedItem()
	if sel == nil {
		return false
	}
	parent := sel.parent
	siblings := t.root
	if parent != nil {
		siblings = parent.Children
	}
	idx := -1
	for i, s := range siblings {
		if s == sel {
			idx = i
			break
		}
	}
	if idx <= 0 {
		return false
	}
	siblings[idx-1], siblings[idx] = siblings[idx], siblings[idx-1]
	t.rebuildFlat()
	t.reselectItem(sel)
	t.autoSave()
	return true
}

// MoveSelectedDown moves the selected item one position down among its
// siblings. Returns true if a move happened.
func (t *Tree) MoveSelectedDown() bool {
	sel := t.SelectedItem()
	if sel == nil {
		return false
	}
	parent := sel.parent
	siblings := t.root
	if parent != nil {
		siblings = parent.Children
	}
	idx := -1
	for i, s := range siblings {
		if s == sel {
			idx = i
			break
		}
	}
	if idx < 0 || idx >= len(siblings)-1 {
		return false
	}
	siblings[idx], siblings[idx+1] = siblings[idx+1], siblings[idx]
	t.rebuildFlat()
	t.reselectItem(sel)
	t.autoSave()
	return true
}

// MoveSelectedOut moves the selected element one level up: out of its
// current parent into the parent's parent (or into the root if the parent
// has no parent). Returns true if a move happened.
//
// If the parent is the last child in its grandparent (i.e. there is no
// "next sibling" position), the element is still detached from its parent
// and placed at the end of the grandparent list — this is a no-op for the
// user's view but still considered a successful out-move.
//
// If the selected element is already at the root level (no parent), this
// returns false.
func (t *Tree) MoveSelectedOut() bool {
	sel := t.SelectedItem()
	if sel == nil || sel.parent == nil {
		return false
	}
	parent := sel.parent
	grandparent := parent.parent

	// Remove sel from parent.Children.
	remaining := make([]*Item, 0, len(parent.Children))
	for _, child := range parent.Children {
		if child != sel {
			remaining = append(remaining, child)
		}
	}
	parent.Children = remaining

	// Decide target list and insertion anchor.
	var siblings []*Item
	var anchor *Item
	if grandparent == nil {
		siblings = t.root
	} else {
		siblings = grandparent.Children
	}
	anchor = parent

	// Append sel after anchor in siblings.
	inserted := false
	for i, s := range siblings {
		if s == anchor {
			siblings = append(siblings[:i+1], append([]*Item{sel}, siblings[i+1:]...)...)
			sel.parent = grandparent
			inserted = true
			break
		}
	}
	if !inserted {
		// Anchor lost (shouldn't happen) — restore parent state.
		parent.Children = append(parent.Children, sel)
		sel.parent = parent
		return false
	}
	if grandparent == nil {
		t.root = siblings
	} else {
		grandparent.Children = siblings
	}

	// Archive flag for folders, mirroring moveItem.
	if sel.IsFolder {
		firstArchived := -1
		for i, s := range siblings {
			if s.IsFolder && s.Archived {
				firstArchived = i
				break
			}
		}
		myIdx := -1
		for i, s := range siblings {
			if s == sel {
				myIdx = i
				break
			}
		}
		if firstArchived >= 0 && myIdx >= firstArchived {
			sel.Archived = true
		} else {
			sel.Archived = false
		}
	}

	t.rebuildFlat()
	t.reselectItem(sel)
	t.autoSave()
	return true
}

// reselectItem sets t.selected to the flat index of `item`, if visible.
func (t *Tree) reselectItem(item *Item) {
	for i, f := range t.flat {
		if f == item {
			t.selected = i
			return
		}
	}
}

// SelectNext moves selection down.
func (t *Tree) SelectNext() {
	if len(t.flat) == 0 {
		return
	}
	if t.selected < 0 {
		t.selected = 0
	} else if t.selected < len(t.flat)-1 {
		t.selected++
	}
	t.ensureVisible()
}

// SelectPrev moves selection up.
func (t *Tree) SelectPrev() {
	if len(t.flat) == 0 {
		return
	}
	if t.selected < 0 {
		t.selected = 0
	} else if t.selected > 0 {
		t.selected--
	}
	t.ensureVisible()
}

// ensureVisible makes sure the selected item is within the visible scroll area.
func (t *Tree) ensureVisible() {
	if t.selected < 0 {
		return
	}
	if t.selected < t.scroll {
		t.scroll = t.selected
	}
	// Reserve 1 line for header (toolbar is now in the header).
	contentHeight := t.height - 1
	if contentHeight < 1 {
		contentHeight = 1
	}
	if t.selected >= t.scroll+contentHeight {
		t.scroll = t.selected - contentHeight + 1
		t.clampScroll(contentHeight)
	}
}

// AddChild adds a child item to a folder.
func (f *Item) AddChild(child *Item) {
	f.Children = append(f.Children, child)
	child.parent = f
}

// Depth returns the nesting depth of this item.
func (f *Item) Depth() int {
	d := 0
	p := f.parent
	for p != nil {
		d++
		p = p.parent
	}
	return d
}

// Path returns folder names from the root of the tree to this item's parent.
func (f *Item) Path() []string {
	var names []string
	p := f.parent
	for p != nil {
		names = append([]string{p.Name}, names...)
		p = p.parent
	}
	return names
}

// Domain returns the dot-separated domain path for this item. The profile is
// separated from the folder path with "__" to match the session ID format used
// by just-pi. Folders are domains themselves; chats and terminals inherit their
// parent folder's domain.
// Examples:
//
//	profile "human-horizon", folder "Projects/HumanHorizon/Automata" → "human-horizon__projects.humanhorizon.automata"
//	profile "human-horizon", chat in root "chat1" → "human-horizon__"
//	no profile, folder "Projects" → "projects"
func (f *Item) Domain(profile string) string {
	var parts []string
	p := f
	for p != nil {
		if p.IsFolder {
			parts = append([]string{slug.Slug(p.Name)}, parts...)
		}
		p = p.parent
	}
	profilePart := paths.ProfileSlug(profile)
	if len(parts) == 0 {
		return profilePart
	}
	return profilePart + "__" + strings.Join(parts, ".")
}

// Icon returns the display icon for the item.
func (f *Item) Icon() string {
	if f.IsFolder {
		if f.Expanded {
			return "- 📂"
		}
		return "+ 📁"
	}
	if f.IsTerminal {
		return "  >_"
	}
	return "  💬"
}

// indent returns the indentation string for the item's depth.
func indent(depth int) string {
	return strings.Repeat("  ", depth)
}

// AddFolderToSelected adds a folder to the selected item if it's a folder,
// otherwise adds to the root.
func (t *Tree) AddFolderToSelected(name string) {
	if sel := t.SelectedItem(); sel != nil && sel.IsFolder {
		t.addChildFolder(sel, name)
		return
	}
	t.AddFolder(name)
}

// AddChatToSelected adds a chat to the selected item if it's a folder,
// otherwise adds to the root.
func (t *Tree) AddChatToSelected(name string) {
	if sel := t.SelectedItem(); sel != nil && sel.IsFolder {
		t.addChildChat(sel, name)
		return
	}
	t.AddChat(name)
}

// AddTerminalToSelected adds a terminal to the selected item if it's a folder,
// otherwise adds to the root.
func (t *Tree) AddTerminalToSelected(name string) {
	if sel := t.SelectedItem(); sel != nil && sel.IsFolder {
		t.addChildTerminal(sel, name)
		return
	}
	t.AddTerminal(name)
}

// --- Helpers ---

// bindFolder anchors a folder to a real filesystem path. An empty path
// clears the binding. The path is normalised via filepath.Clean and then
// stat-ed so BoundStale is up to date.
func (t *Tree) bindFolder(item *Item, path string) {
	if !item.IsFolder {
		return
	}
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" {
		t.unbindFolder(item)
		return
	}
	item.SetBoundPath(path)
	t.rebuildFlat()
	t.autoSave()
}

// unbindFolder removes the filesystem anchor from a folder.
func (t *Tree) unbindFolder(item *Item) {
	if !item.IsFolder {
		return
	}
	item.SetBoundPath("")
	t.rebuildFlat()
	t.autoSave()
}

// revealInFinder opens the bound directory in the system file manager.
// Silently no-ops when the path is missing.
func revealInFinder(path string) {
	if path == "" {
		return
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		return
	}
	_ = exec.Command("open", path).Start()
}

func (t *Tree) addChildFolder(parent *Item, name string) {
	child := &Item{Name: name, IsFolder: true, Expanded: true, ID: generateID()}
	parent.AddChild(child)
	parent.Expanded = true
	t.rebuildFlat()
	t.autoSave()
}

func (t *Tree) addChildChat(parent *Item, name string) {
	child := &Item{Name: name, ID: generateID()}
	parent.AddChild(child)
	parent.Expanded = true
	t.rebuildFlat()
	t.autoSave()
}

func (t *Tree) addChildTerminal(parent *Item, name string) {
	child := &Item{Name: name, IsTerminal: true, ID: generateID()}
	parent.AddChild(child)
	parent.Expanded = true
	t.rebuildFlat()
	t.autoSave()
}

func (t *Tree) renameItem(item *Item, name string) {
	if name != "" {
		item.Name = name
		t.rebuildFlat()
		t.autoSave()
	}
}

func (t *Tree) deleteItem(item *Item) {
	if item.parent != nil {
		parent := item.parent
		for i, child := range parent.Children {
			if child == item {
				parent.Children = append(parent.Children[:i], parent.Children[i+1:]...)
				break
			}
		}
	} else {
		for i, root := range t.root {
			if root == item {
				t.root = append(t.root[:i], t.root[i+1:]...)
				break
			}
		}
	}
	t.rebuildFlat()
	t.autoSave()
}

func (t *Tree) moveItem(item, target *Item) {
	if item == target || target == nil {
		return
	}

	// Capture the session ID before mutating the tree so the callback can
	// detect cross-parent moves that change the just-pi session id.
	oldID := t.sessionIDOf(item)

	// Determine target parent and insertion index before removing item,
	// because removal shifts indices.
	var targetParent *Item
	var targetIndex int
	if target.IsFolder {
		targetParent = target
		targetIndex = len(target.Children)
	} else {
		targetParent = target.parent
		if targetParent != nil {
			for i, child := range targetParent.Children {
				if child == target {
					targetIndex = i + 1
					break
				}
			}
		} else {
			for i, root := range t.root {
				if root == target {
					targetIndex = i + 1
					break
				}
			}
		}
	}

	// Remove item from current parent. If item was in the same parent before
	// the target index, decrement targetIndex to account for the removal.
	if item.parent != nil {
		parent := item.parent
		for i, child := range parent.Children {
			if child == item {
				if parent == targetParent && i < targetIndex {
					targetIndex--
				}
				parent.Children = append(parent.Children[:i], parent.Children[i+1:]...)
				break
			}
		}
	} else {
		for i, root := range t.root {
			if root == item {
				if targetParent == nil && i < targetIndex {
					targetIndex--
				}
				t.root = append(t.root[:i], t.root[i+1:]...)
				break
			}
		}
	}

	// Decide archived status for folders. Chats and terminals are never archived.
	if item.IsFolder {
		if targetParent != nil {
			firstArchived := -1
			for i, child := range targetParent.Children {
				if child.IsFolder && child.Archived {
					firstArchived = i
					break
				}
			}
			if firstArchived >= 0 && targetIndex >= firstArchived {
				item.Archived = true
			} else {
				item.Archived = false
			}
		} else {
			// Root level: archived if dropped after the first archived root folder.
			firstArchived := -1
			for i, root := range t.root {
				if root.IsFolder && root.Archived {
					firstArchived = i
					break
				}
			}
			if firstArchived >= 0 && targetIndex >= firstArchived {
				item.Archived = true
			} else {
				item.Archived = false
			}
		}
	}

	// Insert into target parent.
	if targetParent != nil {
		targetParent.Children = append(targetParent.Children[:targetIndex], append([]*Item{item}, targetParent.Children[targetIndex:]...)...)
		item.parent = targetParent
	} else {
		t.root = append(t.root[:targetIndex], append([]*Item{item}, t.root[targetIndex:]...)...)
		item.parent = nil
	}

	t.rebuildFlat()
	t.autoSave()

	newID := t.sessionIDOf(item)
	if newID != oldID && t.onItemMoved != nil {
		t.onItemMoved(item, oldID, newID)
	}
}

// --- Context menu ---

// buildContextMenuItems returns the list of actions to show in the context
// menu for the given selection. Extracted so it can be unit-tested without
// requiring a mouse event.
func (t *Tree) buildContextMenuItems(sel *Item) []warp.PopoverItem {
	var items []warp.PopoverItem
	if sel != nil && sel.IsFolder {
		archiveLabel := "Archive"
		if sel.Archived {
			archiveLabel = "Unarchive"
		}
		items = []warp.PopoverItem{
			{Name: "New Folder", Action: func() { t.startInput("Folder name:", func(name string) { t.addChildFolder(sel, name) }) }},
			{Name: "New Chat", Action: func() { t.startInput("Chat name:", func(name string) { t.addChildChat(sel, name) }) }},
			{Name: "New Terminal", Action: func() { t.startInput("Terminal name:", func(name string) { t.addChildTerminal(sel, name) }) }},
			{Name: "Rename", Action: func() { t.startInput("Rename:", func(name string) { t.renameItem(sel, name) }) }},
			{Name: archiveLabel, Action: func() { t.toggleArchive(sel) }},
			{Name: "Move up", Action: func() { t.MoveSelectedUp() }},
			{Name: "Move down", Action: func() { t.MoveSelectedDown() }},
			{Name: "Move out", Action: func() { t.MoveSelectedOut() }},
		}
		// Bind/Reveal/Unbind only make sense for anchored folders.
		if sel.BoundPath != "" {
			items = append(items, []warp.PopoverItem{
				{Name: "Reveal in Finder", Action: func() { revealInFinder(sel.BoundPath) }},
				{Name: "Unbind", Action: func() { t.unbindFolder(sel) }},
			}...)
		} else {
			items = append(items, []warp.PopoverItem{
				{Name: "Bind to folder…", Action: func() {
					t.startInput("Bind to path:", func(path string) { t.bindFolder(sel, path) })
				}},
			}...)
		}
		items = append(items, []warp.PopoverItem{
			{Name: "Delete", Action: func() { t.startConfirm(sel) }},
		}...)
	} else if sel != nil && !sel.IsFolder {
		deleteLabel := "Delete"
		if sel.IsTerminal {
			deleteLabel = "Delete terminal"
		} else {
			deleteLabel = "Delete chat"
		}
		items = []warp.PopoverItem{
			{Name: "Rename", Action: func() { t.startInput("Rename:", func(name string) { t.renameItem(sel, name) }) }},
			{Name: "Move up", Action: func() { t.MoveSelectedUp() }},
			{Name: "Move down", Action: func() { t.MoveSelectedDown() }},
			{Name: deleteLabel, Action: func() { t.startConfirm(sel) }},
		}
	} else {
		items = []warp.PopoverItem{
			{Name: "New Folder", Action: func() { t.startInput("Folder name:", func(name string) { t.AddFolder(name) }) }},
			{Name: "New Chat", Action: func() { t.startInput("Chat name:", func(name string) { t.AddChat(name) }) }},
			{Name: "New Terminal", Action: func() { t.startInput("Terminal name:", func(name string) { t.AddTerminal(name) }) }},
		}
	}
	return items
}

func (t *Tree) showContextMenu(x, y int) {
	sel := t.ItemAt(t.selected)
	items := t.buildContextMenuItems(sel)

	t.popover = &warp.Popover{
		Items:   items,
		X:       x,
		Y:       y,
		OnClose: func() { t.popover = nil },
	}
}

// toggleArchive flips the archived state of a folder and reorders it within its
// parent so that archived folders stay below the separator line. Chats and
// terminals cannot be archived.
func (t *Tree) toggleArchive(item *Item) {
	if !item.IsFolder {
		return
	}
	item.Archived = !item.Archived
	if item.parent == nil {
		// Root level: sort root items so non-archived folders come first, then
		// archived folders; chats and terminals keep their relative order at the
		// top.
		var active, archived []*Item
		for _, root := range t.root {
			if root == item {
				continue
			}
			if root.IsFolder && root.Archived {
				archived = append(archived, root)
			} else {
				active = append(active, root)
			}
		}
		if item.Archived {
			archived = append(archived, item)
		} else {
			active = append(active, item)
		}
		t.root = append(active, archived...)
	} else {
		parent := item.parent
		children := parent.Children
		// Separate active items (folders, chats, terminals) and archived folders.
		var active, archived []*Item
		for _, child := range children {
			if child == item {
				continue
			}
			if child.IsFolder && child.Archived {
				archived = append(archived, child)
			} else {
				active = append(active, child)
			}
		}
		if item.Archived {
			archived = append(archived, item)
		} else {
			active = append(active, item)
		}
		parent.Children = append(active, archived...)
	}
	t.rebuildFlat()
	t.autoSave()
}

func (t *Tree) showCreateMenu(parent *Item, x, y int) {
	var items []warp.PopoverItem
	if parent != nil {
		items = []warp.PopoverItem{
			{Name: "New Folder", Action: func() { t.startInput("Folder name:", func(name string) { t.addChildFolder(parent, name) }) }},
			{Name: "New Chat", Action: func() { t.startInput("Chat name:", func(name string) { t.addChildChat(parent, name) }) }},
			{Name: "New Terminal", Action: func() { t.startInput("Terminal name:", func(name string) { t.addChildTerminal(parent, name) }) }},
		}
	} else {
		items = []warp.PopoverItem{
			{Name: "New Folder", Action: func() { t.startInput("Folder name:", func(name string) { t.AddFolder(name) }) }},
			{Name: "New Chat", Action: func() { t.startInput("Chat name:", func(name string) { t.AddChat(name) }) }},
			{Name: "New Terminal", Action: func() { t.startInput("Terminal name:", func(name string) { t.AddTerminal(name) }) }},
		}
	}
	t.popover = &warp.Popover{
		Items:   items,
		X:       x,
		Y:       y,
		OnClose: func() { t.popover = nil },
	}
}

// --- Modal management ---

// startConfirm enters delete-confirmation mode.
func (t *Tree) startConfirm(item *Item) {
	t.confirmMode = true
	t.confirmItem = item
	t.confirmYes = func() { t.deleteItem(item) }

	title := "Delete"
	if item.IsFolder {
		title = "Delete folder"
	} else if item.IsTerminal {
		title = "Delete terminal"
	} else {
		title = "Delete chat"
	}
	content := "\"" + item.Name + "\"?"

	t.modal = warp.NewModal(title, content,
		[]warp.ModalButton{
			{Label: "Del", Action: func() {
				if t.confirmYes != nil {
					t.confirmYes()
				}
				t.closeModal()
			}},
			{Label: "Esc", Action: func() {
				t.closeModal()
			}},
		},
		func() { t.closeModal() },
	)
	t.modalActive = true
}

// startInput enters input mode with the given prompt and callback.
func (t *Tree) startInput(prompt string, done func(name string)) {
	t.inputMode = true
	t.inputPrompt = prompt
	t.inputValue = ""
	t.inputCursor = 0
	t.inputDone = done

	t.updateInputModal()
}

// confirmInput confirms the current input.
func (t *Tree) confirmInput() {
	if t.inputDone != nil && t.inputValue != "" {
		t.inputDone(t.inputValue)
	}
	t.closeModal()
}

// inputRunes returns the input value as a rune slice.
func (t *Tree) inputRunes() []rune {
	return []rune(t.inputValue)
}

// inputCursorClamp ensures inputCursor is within [0, len(runes)].
func (t *Tree) inputCursorClamp() {
	runes := t.inputRunes()
	if t.inputCursor < 0 {
		t.inputCursor = 0
	}
	if t.inputCursor > len(runes) {
		t.inputCursor = len(runes)
	}
}

// insertInput inserts runes at the current cursor position.
func (t *Tree) insertInput(runes []rune) {
	cur := t.inputRunes()
	t.inputCursorClamp()
	out := make([]rune, 0, len(cur)+len(runes))
	out = append(out, cur[:t.inputCursor]...)
	out = append(out, runes...)
	out = append(out, cur[t.inputCursor:]...)
	t.inputValue = string(out)
	t.inputCursor += len(runes)
}

// deleteInputBefore deletes the rune before the cursor (backspace).
func (t *Tree) deleteInputBefore() {
	runes := t.inputRunes()
	t.inputCursorClamp()
	if t.inputCursor == 0 {
		return
	}
	out := make([]rune, 0, len(runes)-1)
	out = append(out, runes[:t.inputCursor-1]...)
	out = append(out, runes[t.inputCursor:]...)
	t.inputValue = string(out)
	t.inputCursor--
}

// deleteInputAfter deletes the rune after the cursor (delete).
func (t *Tree) deleteInputAfter() {
	runes := t.inputRunes()
	t.inputCursorClamp()
	if t.inputCursor >= len(runes) {
		return
	}
	out := make([]rune, 0, len(runes)-1)
	out = append(out, runes[:t.inputCursor]...)
	out = append(out, runes[t.inputCursor+1:]...)
	t.inputValue = string(out)
}

// Modal returns the active modal, or nil if none.
func (t *Tree) Modal() *warp.Modal {
	if !t.modalActive {
		return nil
	}
	return t.modal
}

// SetOnSelectChat sets the callback invoked when a chat item is selected.
func (t *Tree) SetOnSelectChat(fn func(*Item)) {
	t.onSelectChat = fn
}

// SetOnSelectFolder sets the callback invoked when a folder is selected.
func (t *Tree) SetOnSelectFolder(fn func(*Item)) {
	t.onSelectFolder = fn
}

// SetOnStopSession sets the callback invoked when the user stops a session.
func (t *Tree) SetOnStopSession(fn func(*Item)) {
	t.onStopSession = fn
}

// SetOnItemMoved registers a callback fired after a successful move with
// (item, oldSessionID, newSessionID). The IDs are bare (no profile prefix) and
// stable across renames within the same parent.
func (t *Tree) SetOnItemMoved(fn func(*Item, string, string)) {
	t.onItemMoved = fn
}

// SetProfile attaches a profile slug used to compute stable session IDs in
// the on-item-moved callback. Not required for moves that stay within the
// same parent.
func (t *Tree) SetProfile(profile string) {
	t.profile = profile
}

// sessionIDOf returns the bare session ID for an item (no profile prefix).
func (t *Tree) sessionIDOf(item *Item) string {
	if item == nil {
		return ""
	}
	var parts []string
	for _, p := range item.Path() {
		parts = append(parts, slug.Slug(p))
	}
	parts = append(parts, slug.Slug(item.Name))
	id := strings.Join(parts, ".")
	return id
}

// refreshBoundStale re-evaluates whether the bound filesystem path still
// exists. Stale items still render (with a warning icon) but chats cannot
// be spawned with a non-existent --cwd.
func (item *Item) refreshBoundStale() {
	if item.BoundPath == "" {
		item.BoundStale = false
		return
	}
	info, err := os.Stat(item.BoundPath)
	item.BoundStale = err != nil || !info.IsDir()
}

// SetBoundPath updates the bound filesystem path and refreshes the stale
// flag. The caller is responsible for rebuilding the flat list and saving
// state when needed.
func (item *Item) SetBoundPath(path string) {
	item.BoundPath = path
	item.refreshBoundStale()
}

// EffectiveBoundPath walks the folder ancestors (self first) and returns
// the first non-empty BoundPath. Empty string when nothing in the chain is
// anchored. Used when spawning a chat: --cwd inherits from the nearest
// anchored ancestor.
func (item *Item) EffectiveBoundPath() string {
	for cur := item; cur != nil; cur = cur.parent {
		if cur.BoundPath != "" {
			return cur.BoundPath
		}
	}
	return ""
}

// SetActiveSessions replaces the set of IDs currently considered active and
// persists it to state.json.
func (t *Tree) SetActiveSessions(ids map[string]struct{}) {
	t.activeSessions = ids
	_ = t.SaveState()
}

// IsActiveSession reports whether the given item currently has a running session.
func (t *Tree) IsActiveSession(item *Item) bool {
	if t.activeSessions == nil {
		return false
	}
	id := t.sessionKey(item)
	_, ok := t.activeSessions[id]
	return ok
}

// HasActiveDescendant reports whether a folder contains any chat or terminal
// whose session is currently active, recursively.
func (t *Tree) HasActiveDescendant(item *Item) bool {
	if !item.IsFolder {
		return false
	}
	for _, child := range item.Children {
		if child.IsFolder {
			if t.HasActiveDescendant(child) {
				return true
			}
		} else if t.IsActiveSession(child) {
			return true
		}
	}
	return false
}

// ActiveSessionIDs returns the current active session IDs as a sorted slice.
// The order is deterministic to keep state.json stable.
func (t *Tree) ActiveSessionIDs() []string {
	if len(t.activeSessions) == 0 {
		return nil
	}
	ids := make([]string, 0, len(t.activeSessions))
	for id := range t.activeSessions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// FindItemBySessionID returns the tree item whose full session ID (including
// profile prefix) matches the given id, or nil if not found.
func (t *Tree) FindItemBySessionID(sessionID string) *Item {
	prefix := ""
	if t.Profile != "" {
		prefix = slug.Slug(t.Profile) + "__"
	}
	for _, item := range t.AllItems() {
		if item.IsFolder {
			continue
		}
		id := prefix + t.sessionIDOf(item)
		if id == sessionID {
			return item
		}
	}
	return nil
}

// SetStatusBadges replaces the per-session status emoji used for inline
// indicators in the tree (e.g. "🧠", "📖", "💤"). Pass an empty map to clear.
func (t *Tree) SetStatusBadges(m map[string]string) {
	t.statusBadges = m
}

// StatusBadge returns the emoji badge for an item, or "" if none is set.
// Only chat items (not folders, not terminals) receive a badge.
func (t *Tree) StatusBadge(item *Item) string {
	if item == nil {
		return ""
	}
	if item.IsFolder || item.IsTerminal {
		return ""
	}
	if len(t.statusBadges) == 0 {
		return ""
	}
	return t.statusBadges[t.sessionKey(item)]
}

// TreeWidth returns the fixed tree panel width in characters.
func (t *Tree) TreeWidth() int {
	if t.treeWidth <= 0 {
		return 30
	}
	return t.treeWidth
}

// SetTreeWidth sets the fixed tree panel width.
func (t *Tree) SetTreeWidth(w int) {
	t.treeWidth = w
}

// PlanWidth returns the fixed plan panel width in characters.
func (t *Tree) PlanWidth() int {
	if t.planWidth <= 0 {
		return 40
	}
	return t.planWidth
}

// SetPlanWidth sets the fixed plan panel width.
func (t *Tree) SetPlanWidth(w int) {
	t.planWidth = w
}

// sessionKey returns the same identifier used by SessionManager to key active
// sessions. It mirrors the profile-prefixed session name logic in main.go.
func (t *Tree) sessionKey(item *Item) string {
	key := slug.SessionName(item.Path(), item.Name)
	if t.Profile != "" {
		key = slug.Slug(t.Profile) + "__" + key
	}
	return key
}

// SessionKeyOf is the public form of sessionKey, used by callers outside the
// package (e.g. main.go when correlating an Item with an on-disk status file).
func (t *Tree) SessionKeyOf(item *Item) string { return t.sessionKey(item) }

// Root returns the top-level items of the tree. The slice is the live backing
// array of the tree, so callers must NOT mutate it. Use this to enumerate
// items without traversing via the flat display list.
func (t *Tree) Root() []*Item { return t.root }

// AllItems returns every item in the tree, recursively. Order is depth-first
// pre-order; useful when an external caller (e.g. main.go status polling)
// needs to walk the whole tree without caring about the on-screen layout.
func (t *Tree) AllItems() []*Item {
	var out []*Item
	var walk func(items []*Item)
	walk = func(items []*Item) {
		for _, it := range items {
			if it == nil {
				continue
			}
			out = append(out, it)
			if it.IsFolder {
				walk(it.Children)
			}
		}
	}
	walk(t.root)
	return out
}

// closeModal closes any active modal and clears state.
func (t *Tree) closeModal() {
	t.modalActive = false
	t.modal = nil
	t.inputMode = false
	t.inputPrompt = ""
	t.inputValue = ""
	t.inputCursor = 0
	t.inputDone = nil
	t.confirmMode = false
	t.confirmItem = nil
	t.confirmYes = nil
	t.resetHover()
}

// updateInputModal updates the input modal with the current input value.
func (t *Tree) updateInputModal() {
	runes := t.inputRunes()
	t.inputCursorClamp()
	before := string(runes[:t.inputCursor])
	after := string(runes[t.inputCursor:])
	content := before + "▌" + after
	t.modal = warp.NewModal(t.inputPrompt, content,
		[]warp.ModalButton{
			{Label: "Create", Action: func() { t.confirmInput() }},
			{Label: "Cancel", Action: func() { t.closeModal() }},
		},
		func() { t.closeModal() },
	)
	t.modalActive = true
}

// resetHover clears hover state.
func (t *Tree) resetHover() {
	t.hoverIdx = -1
	t.actionIcon = ""
}

// autoSave persists the tree state to disk.
func (t *Tree) autoSave() {
	if err := t.SaveState(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "Failed to save tree state:", err)
	}
}
