package tree

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/HumanHorizon/automata/internal/paths"
	"github.com/HumanHorizon/automata/internal/slug"
	apptheme "github.com/HumanHorizon/automata/internal/theme"
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

	// Modal (input, confirmation, or help) — directly managed by Tree
	modal       *warp.Modal
	modalActive bool
	helpMode    bool

	// Modal input (for creating/renaming)
	inputMode   bool
	inputPrompt string
	inputValue  string
	inputCursor int                     // rune cursor position within inputValue
	inputDone   func(name string) error // called on Enter with the typed name
	inputError  string                  // validation or operation error shown in the modal

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

	// Delete callbacks split side-effect-free preflight from post-commit cleanup.
	onBeforeDelete    func(*Item) error
	onDeleteCommitted func(*Item) error
	onDeleteAborted   func(*Item)

	// Callback fired after the active theme changes.
	onThemeChange func(string)

	// Callbacks for application-level overlays. When unset, Tree keeps its
	// local overlay fallback for standalone use and unit tests.
	onOpenHelp     func()
	onOpenSettings func()

	// Callback fired before an item changes parent. It may migrate external
	// data and returns a rollback function used if the state save fails.
	onBeforeItemMoved func(item, newParent *Item) (func() error, error)

	// Callback fired after an item is moved to a new parent. The arguments are
	// the bare session ID (no profile prefix) of the item before and after the
	// move. They are equal when the move did not change the session identity
	// (e.g. re-ordering siblings within the same parent).
	profile     string
	onItemMoved func(item *Item, oldSessionID, newSessionID string)

	// onRename runs before the tree item is changed. Returning an error keeps
	// the old name and leaves the input modal open.
	onRename func(item *Item, newName string) error

	// onBeforeRename runs before the tree item is changed and returns an
	// external migration rollback used when Tree.SaveState fails.
	onBeforeRename func(item *Item, newName string) (func() error, error)

	// onRenameCommitted runs only after the renamed tree state is persisted.
	onRenameCommitted func(item *Item, oldName, newName string)

	// saveStateOverride is a test seam for persistence failure ordering.
	saveStateOverride func() error
	stateLoadErr      error
	lastActionError   error

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

	// Theme is the persisted identifier of the active visual theme.
	Theme string

	// Profile name for state isolation (e.g. "ai" → ~/.automata/ai/state.json)
	Profile string

	// saveMu serializes atomic state snapshots and replacements.
	saveMu sync.Mutex
}

func canonicalSiblingKey(name string) string {
	return slug.Slug(strings.TrimSpace(name))
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
		Theme:           apptheme.Default().ID,
	}
}

// AddFolder adds a root-level folder and returns the tree for chaining.
// Use CreateFolder when the caller needs to handle validation or persistence errors.
func (t *Tree) AddFolder(name string) *Tree {
	_, _ = t.CreateFolder(name)
	return t
}

// AddChat adds a root-level chat and returns the tree for chaining.
// Use CreateChat when the caller needs to handle validation or persistence errors.
func (t *Tree) AddChat(name string) *Tree {
	_, _ = t.CreateChat(name)
	return t
}

// AddTerminal adds a root-level terminal and returns the tree for chaining.
// Use CreateTerminal when the caller needs to handle validation or persistence errors.
func (t *Tree) AddTerminal(name string) *Tree {
	_, _ = t.CreateTerminal(name)
	return t
}

// CreateFolder creates a unique root-level folder and persists the new tree.
func (t *Tree) CreateFolder(name string) (*Item, error) {
	return t.createItem(nil, &Item{IsFolder: true, Expanded: true, ID: generateID()}, name)
}

// CreateChat creates a unique root-level chat and persists the new tree.
func (t *Tree) CreateChat(name string) (*Item, error) {
	return t.createItem(nil, &Item{ID: generateID()}, name)
}

// CreateTerminal creates a unique root-level terminal and persists the new tree.
func (t *Tree) CreateTerminal(name string) (*Item, error) {
	return t.createItem(nil, &Item{IsTerminal: true, ID: generateID()}, name)
}

// CreateChildFolder creates a unique folder under parent and persists the new tree.
func (t *Tree) CreateChildFolder(parent *Item, name string) (*Item, error) {
	return t.createItem(parent, &Item{IsFolder: true, Expanded: true, ID: generateID()}, name)
}

// CreateChildChat creates a unique chat under parent and persists the new tree.
func (t *Tree) CreateChildChat(parent *Item, name string) (*Item, error) {
	return t.createItem(parent, &Item{ID: generateID()}, name)
}

// CreateChildTerminal creates a unique terminal under parent and persists the new tree.
func (t *Tree) CreateChildTerminal(parent *Item, name string) (*Item, error) {
	return t.createItem(parent, &Item{IsTerminal: true, ID: generateID()}, name)
}

func (t *Tree) createItem(parent, item *Item, name string) (*Item, error) {
	displayName := strings.TrimSpace(name)
	key := canonicalSiblingKey(displayName)
	if key == "" {
		return nil, t.recordActionError(fmt.Errorf("name must contain a letter or digit"))
	}
	if parent != nil && (!parent.IsFolder || !t.containsItem(parent)) {
		return nil, t.recordActionError(fmt.Errorf("parent must be a folder in this tree"))
	}
	siblings := t.root
	if parent != nil {
		siblings = parent.Children
	}
	if err := validateSiblingIdentity(siblings, key, nil); err != nil {
		return nil, t.recordActionError(err)
	}

	oldRoot := append([]*Item(nil), t.root...)
	var oldChildren []*Item
	oldExpanded := false
	if parent != nil {
		oldChildren = append([]*Item(nil), parent.Children...)
		oldExpanded = parent.Expanded
	}
	oldSelection := t.SelectedItem()
	oldSelectedIndex := t.selected
	oldScroll := t.scroll

	item.Name = displayName
	if parent == nil {
		t.root = append(t.root, item)
	} else {
		item.parent = parent
		parent.Children = append(parent.Children, item)
		parent.Expanded = true
	}
	t.rebuildFlat()
	t.reselectItem(oldSelection)

	if err := t.SaveState(); err != nil {
		if stateCommitWasApplied(err) {
			return item, t.recordActionError(err)
		}
		t.root = oldRoot
		if parent != nil {
			parent.Children = oldChildren
			parent.Expanded = oldExpanded
		}
		item.parent = nil
		t.rebuildFlat()
		t.selected = oldSelectedIndex
		t.scroll = oldScroll
		if oldSelection != nil {
			t.reselectItem(oldSelection)
		}
		return nil, t.recordActionError(err)
	}
	t.lastActionError = nil
	return item, nil
}

func validateSiblingIdentity(siblings []*Item, key string, ignore *Item) error {
	for _, sibling := range siblings {
		if sibling == nil || sibling == ignore {
			continue
		}
		if canonicalSiblingKey(sibling.Name) == key {
			return fmt.Errorf("name conflicts with sibling %q", sibling.Name)
		}
	}
	return nil
}

func (t *Tree) containsItem(target *Item) bool {
	if target == nil {
		return false
	}
	var visit func([]*Item) bool
	visit = func(items []*Item) bool {
		for _, item := range items {
			if item == target {
				return true
			}
			if item != nil && item.IsFolder && visit(item.Children) {
				return true
			}
		}
		return false
	}
	return visit(t.root)
}

// LastActionError returns the most recent Tree operation warning or failure.
func (t *Tree) LastActionError() error { return t.lastActionError }

// StateLoadError returns the startup snapshot error that blocks persistence.
func (t *Tree) StateLoadError() error { return t.stateLoadErr }

func (t *Tree) recordActionError(err error) error {
	t.lastActionError = err
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "automata: tree operation: %v\n", err)
	}
	return err
}

func (t *Tree) persistMutation(rollback func()) error {
	err := t.SaveState()
	if err == nil {
		t.lastActionError = nil
		return nil
	}
	if !stateCommitWasApplied(err) && rollback != nil {
		rollback()
	}
	return t.recordActionError(err)
}

// Reset removes all items from the tree (root and descendants) and rebuilds
// the flat visible list. State on disk is not touched — call SaveState
// afterwards if you want persistence to follow. Useful for tests that need
// to start from an empty tree and for production flows that switch profile.
func (t *Tree) Reset() {
	t.root = nil
	t.rebuildFlat()
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
	t.autoSave()
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
	for i, sibling := range siblings {
		if sibling == sel {
			idx = i
			break
		}
	}
	if idx <= 0 {
		return false
	}
	oldSiblings := append([]*Item(nil), siblings...)
	oldSelection, oldSelectedIndex, oldScroll := t.SelectedItem(), t.selected, t.scroll
	siblings[idx-1], siblings[idx] = siblings[idx], siblings[idx-1]
	t.rebuildFlat()
	t.reselectItem(sel)
	err := t.persistMutation(func() {
		if parent == nil {
			t.root = oldSiblings
		} else {
			parent.Children = oldSiblings
		}
		t.rebuildFlat()
		t.selected = oldSelectedIndex
		t.scroll = oldScroll
		if oldSelection != nil {
			t.reselectItem(oldSelection)
		}
	})
	return err == nil || stateCommitWasApplied(err)
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
	for i, sibling := range siblings {
		if sibling == sel {
			idx = i
			break
		}
	}
	if idx < 0 || idx >= len(siblings)-1 {
		return false
	}
	oldSiblings := append([]*Item(nil), siblings...)
	oldSelection, oldSelectedIndex, oldScroll := t.SelectedItem(), t.selected, t.scroll
	siblings[idx], siblings[idx+1] = siblings[idx+1], siblings[idx]
	t.rebuildFlat()
	t.reselectItem(sel)
	err := t.persistMutation(func() {
		if parent == nil {
			t.root = oldSiblings
		} else {
			parent.Children = oldSiblings
		}
		t.rebuildFlat()
		t.selected = oldSelectedIndex
		t.scroll = oldScroll
		if oldSelection != nil {
			t.reselectItem(oldSelection)
		}
	})
	return err == nil || stateCommitWasApplied(err)
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
	moved, _ := t.MoveSelectedOutChecked()
	return moved
}

// MoveSelectedOutChecked moves the selected item one level up and reports
// identity, preflight, or persistence failures to the caller.
func (t *Tree) MoveSelectedOutChecked() (bool, error) {
	sel := t.SelectedItem()
	if sel == nil || sel.parent == nil {
		return false, nil
	}
	parent := sel.parent
	grandparent := parent.parent
	siblings := t.root
	if grandparent != nil {
		siblings = grandparent.Children
	}
	if err := validateSiblingIdentity(siblings, canonicalSiblingKey(sel.Name), sel); err != nil {
		return false, t.recordActionError(err)
	}

	oldID := t.sessionIDOf(sel)
	newID := t.sessionIDOfWithParent(sel, grandparent)
	var rollback func() error
	if oldID != newID && t.onBeforeItemMoved != nil {
		var err error
		rollback, err = t.onBeforeItemMoved(sel, grandparent)
		if err != nil {
			return false, t.recordActionError(err)
		}
	}

	oldRoot := append([]*Item(nil), t.root...)
	oldParentChildren := append([]*Item(nil), parent.Children...)
	oldTargetChildren := append([]*Item(nil), siblings...)
	oldArchived := sel.Archived
	oldSelectedItem := t.SelectedItem()
	oldSelectedIndex := t.selected
	oldScroll := t.scroll

	remaining := make([]*Item, 0, len(parent.Children))
	for _, child := range parent.Children {
		if child != sel {
			remaining = append(remaining, child)
		}
	}
	parent.Children = remaining

	anchorIndex := -1
	for i, sibling := range siblings {
		if sibling == parent {
			anchorIndex = i
			break
		}
	}
	if anchorIndex < 0 {
		parent.Children = oldParentChildren
		if rollback != nil {
			if rollbackErr := rollback(); rollbackErr != nil {
				return false, t.recordActionError(fmt.Errorf("move preflight failed; rollback: %v", rollbackErr))
			}
		}
		return false, t.recordActionError(fmt.Errorf("move target parent is no longer attached"))
	}
	siblings = append(siblings, nil)
	copy(siblings[anchorIndex+2:], siblings[anchorIndex+1:])
	siblings[anchorIndex+1] = sel
	sel.parent = grandparent
	if grandparent == nil {
		t.root = siblings
	} else {
		grandparent.Children = siblings
	}

	if sel.IsFolder {
		firstArchived, itemIndex := -1, -1
		for i, sibling := range siblings {
			if sibling == sel {
				itemIndex = i
			}
			if firstArchived < 0 && sibling.IsFolder && sibling.Archived {
				firstArchived = i
			}
		}
		sel.Archived = firstArchived >= 0 && itemIndex >= firstArchived
	}

	t.rebuildFlat()
	t.reselectItem(sel)
	if err := t.SaveState(); err != nil {
		if !stateCommitWasApplied(err) {
			t.root = oldRoot
			parent.Children = oldParentChildren
			if grandparent == nil {
				t.root = oldTargetChildren
			} else {
				grandparent.Children = oldTargetChildren
			}
			sel.parent = parent
			sel.Archived = oldArchived
			t.rebuildFlat()
			t.selected = oldSelectedIndex
			t.scroll = oldScroll
			if oldSelectedItem != nil {
				t.reselectItem(oldSelectedItem)
			}
			if rollback != nil {
				if rollbackErr := rollback(); rollbackErr != nil {
					err = fmt.Errorf("%w; rollback moved data: %v", err, rollbackErr)
				}
			}
			return false, t.recordActionError(err)
		}
		t.recordActionError(err)
	} else {
		t.lastActionError = nil
	}
	if oldID != newID && t.onItemMoved != nil {
		t.onItemMoved(sel, oldID, newID)
	}
	return true, nil
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

// SelectFirst moves selection to the first visible item.
func (t *Tree) SelectFirst() {
	if len(t.flat) == 0 {
		return
	}
	t.selected = 0
	t.ensureVisible()
}

// SelectLast moves selection to the last visible item.
func (t *Tree) SelectLast() {
	if len(t.flat) == 0 {
		return
	}
	t.selected = len(t.flat) - 1
	t.ensureVisible()
}

// SelectPage moves selection by one visible page.
func (t *Tree) SelectPage(direction int) {
	if len(t.flat) == 0 || direction == 0 {
		return
	}
	page := t.contentHeight()
	if page < 1 {
		page = 1
	}
	if t.selected < 0 {
		t.selected = 0
	} else {
		t.selected += direction * page
	}
	if t.selected < 0 {
		t.selected = 0
	}
	if t.selected >= len(t.flat) {
		t.selected = len(t.flat) - 1
	}
	t.ensureVisible()
}

// NavigateLeft collapses the selected folder or selects its parent.
func (t *Tree) NavigateLeft() {
	sel := t.SelectedItem()
	if sel == nil {
		return
	}
	if sel.IsFolder && sel.Expanded {
		sel.Expanded = false
		t.rebuildFlat()
		t.reselectItem(sel)
		t.autoSave()
		return
	}
	if sel.parent != nil {
		t.reselectItem(sel.parent)
		t.ensureVisible()
	}
}

// NavigateRight expands the selected folder or selects its first child.
func (t *Tree) NavigateRight() {
	sel := t.SelectedItem()
	if sel == nil || !sel.IsFolder {
		return
	}
	if !sel.Expanded {
		sel.Expanded = true
		t.rebuildFlat()
		t.reselectItem(sel)
		t.autoSave()
		return
	}
	if len(sel.Children) > 0 {
		t.reselectItem(sel.Children[0])
		t.ensureVisible()
	}
}

// contentHeight returns the number of rows available for tree items.
func (t *Tree) contentHeight() int {
	h := t.height - 2
	if h < 1 {
		return 1
	}
	return h
}

// ensureVisible makes sure the selected item is within the visible scroll area.
func (t *Tree) ensureVisible() {
	if t.selected < 0 {
		return
	}
	if t.selected < t.scroll {
		t.scroll = t.selected
	}
	contentHeight := t.contentHeight()
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
//	profile "human-horizon", root chat "chat1" → "human-horizon"
//	no profile, folder "Projects" → "projects"
func (f *Item) Domain(profile string) string {
	folders := f.Path()
	if f.IsFolder {
		folders = append(folders, f.Name)
	}
	return paths.DomainID(profile, folders)
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
		_, _ = t.CreateChildFolder(sel, name)
		return
	}
	_, _ = t.CreateFolder(name)
}

// AddChatToSelected adds a chat to the selected item if it's a folder,
// otherwise adds to the root.
func (t *Tree) AddChatToSelected(name string) {
	if sel := t.SelectedItem(); sel != nil && sel.IsFolder {
		_, _ = t.CreateChildChat(sel, name)
		return
	}
	_, _ = t.CreateChat(name)
}

// AddTerminalToSelected adds a terminal to the selected item if it's a folder,
// otherwise adds to the root.
func (t *Tree) AddTerminalToSelected(name string) {
	if sel := t.SelectedItem(); sel != nil && sel.IsFolder {
		_, _ = t.CreateChildTerminal(sel, name)
		return
	}
	_, _ = t.CreateTerminal(name)
}

// --- Helpers ---

// bindFolder anchors a folder to a real filesystem path. An empty path
// clears the binding. The path is normalised via filepath.Clean and then
// stat-ed so BoundStale is up to date.
func (t *Tree) bindFolder(item *Item, path string) {
	_ = t.bindFolderChecked(item, path)
}

func (t *Tree) bindFolderChecked(item *Item, path string) error {
	if item == nil || !item.IsFolder || !t.containsItem(item) {
		return t.recordActionError(fmt.Errorf("bind target must be a folder in this tree"))
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return t.unbindFolderChecked(item)
	}
	path = filepath.Clean(path)
	oldPath, oldStale := item.BoundPath, item.BoundStale
	item.SetBoundPath(path)
	t.rebuildFlat()
	return t.persistMutation(func() {
		item.BoundPath, item.BoundStale = oldPath, oldStale
		t.rebuildFlat()
	})
}

// unbindFolder removes the filesystem anchor from a folder.
func (t *Tree) unbindFolder(item *Item) {
	_ = t.unbindFolderChecked(item)
}

func (t *Tree) unbindFolderChecked(item *Item) error {
	if item == nil || !item.IsFolder || !t.containsItem(item) {
		return t.recordActionError(fmt.Errorf("unbind target must be a folder in this tree"))
	}
	oldPath, oldStale := item.BoundPath, item.BoundStale
	item.SetBoundPath("")
	t.rebuildFlat()
	return t.persistMutation(func() {
		item.BoundPath, item.BoundStale = oldPath, oldStale
		t.rebuildFlat()
	})
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

func (t *Tree) addChildFolder(parent *Item, name string) error {
	_, err := t.CreateChildFolder(parent, name)
	return err
}

func (t *Tree) addChildChat(parent *Item, name string) error {
	_, err := t.CreateChildChat(parent, name)
	return err
}

func (t *Tree) addChildTerminal(parent *Item, name string) error {
	_, err := t.CreateChildTerminal(parent, name)
	return err
}

// sortChildrenAlphabetically reorders parent.Children in place: folders
// first (A→Z), then chats (A→Z), then terminals (A→Z). Stable sort
// preserves the original relative order of items sharing a name+kind, so
// users who rely on drag-and-drop can re-sort without losing intent when
// names are unique. The folder itself, its position in the parent list,
// and any deeper hierarchy are not touched.
func (t *Tree) sortChildrenAlphabetically(parent *Item) {
	if parent == nil || !parent.IsFolder || !t.containsItem(parent) || len(parent.Children) < 2 {
		return
	}
	oldChildren := append([]*Item(nil), parent.Children...)
	oldExpanded := parent.Expanded
	oldSelection, oldSelectedIndex, oldScroll := t.SelectedItem(), t.selected, t.scroll
	sortItemsAlphabetically(parent.Children)
	parent.Expanded = true
	t.rebuildFlat()
	if oldSelection != nil {
		t.reselectItem(oldSelection)
	}
	_ = t.persistMutation(func() {
		parent.Children = oldChildren
		parent.Expanded = oldExpanded
		t.rebuildFlat()
		t.selected = oldSelectedIndex
		t.scroll = oldScroll
		if oldSelection != nil {
			t.reselectItem(oldSelection)
		}
	})
}

// sortRootByName reorders only active root items by name. Archived folders
// remain in the archived group at the end of the root list.
func (t *Tree) sortRootByName() {
	t.sortRoot(func(items []*Item) {
		sort.SliceStable(items, func(i, j int) bool {
			return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
		})
	})
}

// sortRootByType groups active root items by kind and sorts each group by name.
// Archived folders remain in the archived group at the end of the root list.
func (t *Tree) sortRootByType() {
	t.sortRoot(sortItemsAlphabetically)
}

func (t *Tree) sortRoot(sortItems func([]*Item)) {
	if len(t.root) < 2 {
		return
	}
	oldRoot := append([]*Item(nil), t.root...)
	selected, oldSelectedIndex, oldScroll := t.SelectedItem(), t.selected, t.scroll
	active := make([]*Item, 0, len(t.root))
	archived := make([]*Item, 0)
	for _, item := range t.root {
		if item.IsFolder && item.Archived {
			archived = append(archived, item)
			continue
		}
		active = append(active, item)
	}
	sortItems(active)
	sortItems(archived)
	t.root = append(active, archived...)
	t.rebuildFlat()
	if selected != nil {
		t.reselectItem(selected)
	}
	_ = t.persistMutation(func() {
		t.root = oldRoot
		t.rebuildFlat()
		t.selected = oldSelectedIndex
		t.scroll = oldScroll
		if selected != nil {
			t.reselectItem(selected)
		}
	})
}

// sortRootAlphabetically preserves the historical type-first root sort.
func (t *Tree) sortRootAlphabetically() {
	t.sortRootByType()
}

// sortItemsAlphabetically is the shared ordering kernel: folders first,
// then chats, then terminals, A→Z case-insensitive. Stable sort keeps
// the relative order of items that share both kind and name.
func sortItemsAlphabetically(items []*Item) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		rankA := itemKindRank(a)
		rankB := itemKindRank(b)
		if rankA != rankB {
			return rankA < rankB
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
}

// itemKindRank returns a small int that places folders before chats
// before terminals. Standalone items (root level) share the chat rank.
func itemKindRank(it *Item) int {
	switch {
	case it.IsFolder:
		return 0
	case it.IsTerminal:
		return 2
	default:
		return 1
	}
}

// RenameItem applies the same validated rename flow used by the UI.
func (t *Tree) RenameItem(item *Item, name string) error {
	return t.renameItem(item, name)
}

func (t *Tree) renameItem(item *Item, name string) error {
	if item == nil {
		return fmt.Errorf("item is required")
	}

	name = strings.TrimSpace(name)
	key := canonicalSiblingKey(name)
	if key == "" {
		return t.recordActionError(fmt.Errorf("name must contain a letter or digit"))
	}
	if err := validateSiblingIdentity(t.siblings(item), key, item); err != nil {
		return t.recordActionError(err)
	}
	if item.Name == name {
		return nil
	}

	oldName := item.Name
	var rollback func() error
	if t.onBeforeRename != nil {
		var err error
		rollback, err = t.onBeforeRename(item, name)
		if err != nil {
			return err
		}
	} else if t.onRename != nil {
		if err := t.onRename(item, name); err != nil {
			return err
		}
	}

	item.Name = name
	t.rebuildFlat()
	if err := t.SaveState(); err != nil {
		if stateCommitWasApplied(err) {
			t.recordActionError(err)
			if t.onRenameCommitted != nil {
				t.onRenameCommitted(item, oldName, name)
			}
			return nil
		}
		item.Name = oldName
		t.rebuildFlat()
		var rollbackErr error
		if rollback != nil {
			rollbackErr = rollback()
		}
		if rollbackErr != nil {
			return t.recordActionError(fmt.Errorf("save renamed tree: %w; rollback: %v", err, rollbackErr))
		}
		return t.recordActionError(err)
	}
	t.lastActionError = nil
	if t.onRenameCommitted != nil {
		t.onRenameCommitted(item, oldName, name)
	}
	return nil
}

func (t *Tree) siblings(item *Item) []*Item {
	if item == nil {
		return nil
	}
	if item.parent != nil {
		return item.parent.Children
	}
	return t.root
}

func (t *Tree) deleteItem(item *Item) {
	_ = t.DeleteItem(item)
}

// DeleteItem removes an item after a side-effect-free runtime preflight, then
// runs cleanup only after the tree snapshot commits.
func (t *Tree) DeleteItem(item *Item) error {
	if item == nil || !t.containsItem(item) {
		return nil
	}
	if t.onBeforeDelete != nil {
		if err := t.onBeforeDelete(item); err != nil {
			if t.onDeleteAborted != nil {
				t.onDeleteAborted(item)
			}
			return t.recordActionError(err)
		}
	}

	oldRoot := append([]*Item(nil), t.root...)
	oldSelectedItem, oldSelectedIndex, oldScroll := t.SelectedItem(), t.selected, t.scroll
	oldParent := item.parent
	var oldParentChildren []*Item
	if oldParent != nil {
		oldParentChildren = append([]*Item(nil), oldParent.Children...)
	}
	if oldParent != nil {
		for i, child := range oldParent.Children {
			if child == item {
				oldParent.Children = append(oldParent.Children[:i], oldParent.Children[i+1:]...)
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
	saveErr := t.SaveState()
	if saveErr != nil && !stateCommitWasApplied(saveErr) {
		t.root = oldRoot
		if oldParent != nil {
			oldParent.Children = oldParentChildren
		}
		item.parent = oldParent
		t.rebuildFlat()
		t.selected = oldSelectedIndex
		t.scroll = oldScroll
		if oldSelectedItem != nil {
			t.reselectItem(oldSelectedItem)
		}
		if t.onDeleteAborted != nil {
			t.onDeleteAborted(item)
		}
		return t.recordActionError(saveErr)
	}
	if saveErr != nil {
		t.recordActionError(saveErr)
	} else {
		t.lastActionError = nil
	}
	if t.onDeleteCommitted != nil {
		if err := t.onDeleteCommitted(item); err != nil {
			if saveErr != nil {
				err = fmt.Errorf("%w; post-delete cleanup: %v", saveErr, err)
			}
			return t.recordActionError(err)
		}
	}
	return saveErr
}

// MoveItem moves an item relative to a target item using the same guarded
// path as drag-and-drop.
func (t *Tree) MoveItem(item, target *Item) {
	_ = t.MoveItemChecked(item, target)
}

// MoveItemChecked moves an item and returns identity, preflight, or persistence errors.
func (t *Tree) MoveItemChecked(item, target *Item) error {
	if item == nil || item == target || target == nil {
		return nil
	}
	if !t.containsItem(item) || !t.containsItem(target) {
		return t.recordActionError(fmt.Errorf("move source and target must belong to this tree"))
	}

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
	if isDescendantOf(targetParent, item) {
		return t.recordActionError(fmt.Errorf("cannot move an item into itself or its descendant"))
	}
	targetSiblings := t.root
	if targetParent != nil {
		targetSiblings = targetParent.Children
	}
	if err := validateSiblingIdentity(targetSiblings, canonicalSiblingKey(item.Name), item); err != nil {
		return t.recordActionError(err)
	}

	oldID := t.sessionIDOf(item)
	newID := t.sessionIDOfWithParent(item, targetParent)
	var rollback func() error
	if oldID != newID && t.onBeforeItemMoved != nil {
		var err error
		rollback, err = t.onBeforeItemMoved(item, targetParent)
		if err != nil {
			return t.recordActionError(err)
		}
	}

	oldRoot := append([]*Item(nil), t.root...)
	oldParent := item.parent
	var oldParentChildren []*Item
	if oldParent != nil {
		oldParentChildren = append([]*Item(nil), oldParent.Children...)
	}
	var oldTargetChildren []*Item
	if targetParent != nil && targetParent != oldParent {
		oldTargetChildren = append([]*Item(nil), targetParent.Children...)
	}
	oldArchived := item.Archived
	oldSelectedItem := t.SelectedItem()
	oldSelectedIndex := t.selected
	oldScroll := t.scroll

	if oldParent != nil {
		for i, child := range oldParent.Children {
			if child == item {
				if oldParent == targetParent && i < targetIndex {
					targetIndex--
				}
				oldParent.Children = append(oldParent.Children[:i], oldParent.Children[i+1:]...)
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

	if item.IsFolder {
		siblings := t.root
		if targetParent != nil {
			siblings = targetParent.Children
		}
		firstArchived := -1
		for i, sibling := range siblings {
			if sibling.IsFolder && sibling.Archived {
				firstArchived = i
				break
			}
		}
		item.Archived = firstArchived >= 0 && targetIndex >= firstArchived
	}
	if targetIndex < 0 {
		targetIndex = 0
	}
	if targetParent != nil {
		if targetIndex > len(targetParent.Children) {
			targetIndex = len(targetParent.Children)
		}
		targetParent.Children = append(targetParent.Children, nil)
		copy(targetParent.Children[targetIndex+1:], targetParent.Children[targetIndex:])
		targetParent.Children[targetIndex] = item
		item.parent = targetParent
	} else {
		if targetIndex > len(t.root) {
			targetIndex = len(t.root)
		}
		t.root = append(t.root, nil)
		copy(t.root[targetIndex+1:], t.root[targetIndex:])
		t.root[targetIndex] = item
		item.parent = nil
	}

	t.rebuildFlat()
	t.reselectItem(oldSelectedItem)
	if err := t.SaveState(); err != nil {
		if stateCommitWasApplied(err) {
			t.recordActionError(err)
			if oldID != newID && t.onItemMoved != nil {
				t.onItemMoved(item, oldID, newID)
			}
			return err
		}
		t.root = oldRoot
		if oldParent != nil {
			oldParent.Children = oldParentChildren
		}
		if targetParent != nil && targetParent != oldParent {
			targetParent.Children = oldTargetChildren
		}
		item.parent = oldParent
		item.Archived = oldArchived
		t.rebuildFlat()
		t.selected = oldSelectedIndex
		t.scroll = oldScroll
		if oldSelectedItem != nil {
			t.reselectItem(oldSelectedItem)
		}
		if rollback != nil {
			if rollbackErr := rollback(); rollbackErr != nil {
				err = fmt.Errorf("%w; rollback moved data: %v", err, rollbackErr)
			}
		}
		return t.recordActionError(err)
	}
	t.lastActionError = nil
	if oldID != newID && t.onItemMoved != nil {
		t.onItemMoved(item, oldID, newID)
	}
	return nil
}

func (t *Tree) moveItem(item, target *Item) {
	_ = t.MoveItemChecked(item, target)
}

func isDescendantOf(candidate, ancestor *Item) bool {
	for current := candidate; current != nil; current = current.parent {
		if current == ancestor {
			return true
		}
	}
	return false
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
			{Name: "New Folder", Action: func() {
				t.startCreateInput("Folder name:", func(name string) (*Item, error) { return t.CreateChildFolder(sel, name) })
			}},
			{Name: "New Chat", Action: func() {
				t.startCreateInput("Chat name:", func(name string) (*Item, error) { return t.CreateChildChat(sel, name) })
			}},
			{Name: "New Terminal", Action: func() {
				t.startCreateInput("Terminal name:", func(name string) (*Item, error) { return t.CreateChildTerminal(sel, name) })
			}},
			{Name: "Rename", Action: func() { t.startRename(sel) }},
			{Name: archiveLabel, Action: func() { t.toggleArchive(sel) }},
			{Name: "Move up", Action: func() { t.MoveSelectedUp() }},
			{Name: "Move down", Action: func() { t.MoveSelectedDown() }},
			{Name: "Move out", Action: func() { t.MoveSelectedOut() }},
			{Name: "Sort A→Z", Action: func() { t.sortChildrenAlphabetically(sel) }},
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
					t.startCheckedInput("Bind to path:", func(path string) error { return t.bindFolderChecked(sel, path) })
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
			{Name: "Rename", Action: func() { t.startRename(sel) }},
			{Name: "Move up", Action: func() { t.MoveSelectedUp() }},
			{Name: "Move down", Action: func() { t.MoveSelectedDown() }},
			{Name: deleteLabel, Action: func() { t.startConfirm(sel) }},
		}
	} else {
		items = t.rootMenuItems()
	}
	return items
}

func (t *Tree) rootMenuItems() []warp.PopoverItem {
	items := t.rootSortMenuItems()
	return append(items,
		warp.PopoverItem{Name: "New Folder", Action: func() { t.startCreateInput("Folder name:", t.CreateFolder) }},
		warp.PopoverItem{Name: "New Chat", Action: func() { t.startCreateInput("Chat name:", t.CreateChat) }},
		warp.PopoverItem{Name: "New Terminal", Action: func() { t.startCreateInput("Terminal name:", t.CreateTerminal) }},
	)
}

func (t *Tree) rootSortMenuItems() []warp.PopoverItem {
	return []warp.PopoverItem{
		{Name: "Sort by name", Action: func() { t.sortRootByName() }},
		{Name: "Sort by type", Action: func() { t.sortRootByType() }},
	}
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

func (t *Tree) showRootMenu(x, y int) {
	t.popover = &warp.Popover{
		Items:   t.rootMenuItems(),
		X:       x,
		Y:       y,
		OnClose: func() { t.popover = nil },
	}
}

func (t *Tree) showSettingsMenu(x, y int) {
	items := make([]warp.PopoverItem, 0, len(apptheme.All()))
	for _, item := range apptheme.All() {
		label := item.Name
		if item.ID == t.ThemeID() {
			label = "✓ " + label
		}
		id := item.ID
		items = append(items, warp.PopoverItem{
			Name: label,
			Action: func() {
				t.SetTheme(id)
			},
		})
	}
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
	if item == nil || !item.IsFolder || !t.containsItem(item) {
		return
	}
	oldRoot := append([]*Item(nil), t.root...)
	parent := item.parent
	var oldChildren []*Item
	if parent != nil {
		oldChildren = append([]*Item(nil), parent.Children...)
	}
	oldArchived := item.Archived
	oldSelection, oldSelectedIndex, oldScroll := t.SelectedItem(), t.selected, t.scroll
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
	if oldSelection != nil {
		t.reselectItem(oldSelection)
	}
	_ = t.persistMutation(func() {
		t.root = oldRoot
		if parent != nil {
			parent.Children = oldChildren
		}
		item.Archived = oldArchived
		t.rebuildFlat()
		t.selected = oldSelectedIndex
		t.scroll = oldScroll
		if oldSelection != nil {
			t.reselectItem(oldSelection)
		}
	})
}

func (t *Tree) showCreateMenu(parent *Item, x, y int) {
	var items []warp.PopoverItem
	if parent != nil {
		items = []warp.PopoverItem{
			{Name: "New Folder", Action: func() {
				t.startCreateInput("Folder name:", func(name string) (*Item, error) { return t.CreateChildFolder(parent, name) })
			}},
			{Name: "New Chat", Action: func() {
				t.startCreateInput("Chat name:", func(name string) (*Item, error) { return t.CreateChildChat(parent, name) })
			}},
			{Name: "New Terminal", Action: func() {
				t.startCreateInput("Terminal name:", func(name string) (*Item, error) { return t.CreateChildTerminal(parent, name) })
			}},
		}
	} else {
		items = []warp.PopoverItem{
			{Name: "New Folder", Action: func() { t.startCreateInput("Folder name:", t.CreateFolder) }},
			{Name: "New Chat", Action: func() { t.startCreateInput("Chat name:", t.CreateChat) }},
			{Name: "New Terminal", Action: func() { t.startCreateInput("Terminal name:", t.CreateTerminal) }},
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

// OpenHelp opens the keyboard navigation help overlay.
func (t *Tree) OpenHelp() {
	if t.helpMode {
		t.closeModal()
		return
	}
	t.helpMode = true
	t.modal = warp.NewModal(
		"Keyboard help",
		keyboardHelpText(),
		[]warp.ModalButton{{Label: "Close", Action: func() { t.closeModal() }}},
		func() { t.closeModal() },
	)
	t.modalActive = true
}

// HelpOpen reports whether the keyboard help overlay is visible.
func (t *Tree) HelpOpen() bool {
	return t.helpMode
}

// startInput enters input mode with a compatibility callback.
func (t *Tree) startInput(prompt string, done func(name string)) {
	t.startCheckedInput(prompt, func(name string) error {
		done(name)
		return nil
	})
}

func (t *Tree) startCheckedInput(prompt string, done func(name string) error) {
	t.inputMode = true
	t.inputPrompt = prompt
	t.inputValue = ""
	t.inputCursor = 0
	t.inputError = ""
	t.inputDone = done
	t.lastActionError = nil
	t.updateInputModal()
}

func (t *Tree) startCreateInput(prompt string, create func(string) (*Item, error)) {
	t.startCheckedInput(prompt, func(name string) error {
		_, err := create(name)
		return err
	})
}

// startRename opens the rename modal with the current name pre-filled.
func (t *Tree) startRename(item *Item) {
	if item == nil {
		return
	}
	t.inputMode = true
	t.inputPrompt = "Rename:"
	t.inputValue = item.Name
	t.inputCursor = len([]rune(item.Name))
	t.inputError = ""
	t.inputDone = func(name string) error {
		return t.renameItem(item, name)
	}
	t.updateInputModal()
}

// confirmInput confirms the current input.
func (t *Tree) confirmInput() {
	t.inputError = ""
	if t.inputDone != nil {
		if err := t.inputDone(t.inputValue); err != nil {
			if stateCommitWasApplied(err) {
				t.recordActionError(err)
			} else {
				t.inputError = err.Error()
				t.updateInputModal()
				return
			}
		}
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
	t.inputError = ""
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
	t.inputError = ""
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
	t.inputError = ""
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

// SetOnBeforeDelete sets a side-effect-free preflight called before persistence.
// Returning an error leaves the tree and runtime unchanged.
func (t *Tree) SetOnBeforeDelete(fn func(*Item) error) {
	t.onBeforeDelete = fn
}

// SetOnDeleteCommitted registers runtime cleanup after the tree deletion commits.
func (t *Tree) SetOnDeleteCommitted(fn func(*Item) error) {
	t.onDeleteCommitted = fn
}

// SetOnDeleteAborted clears resources prepared by the preflight when deletion aborts.
func (t *Tree) SetOnDeleteAborted(fn func(*Item)) {
	t.onDeleteAborted = fn
}

// SetOnThemeChange registers a callback invoked after a theme is selected.
func (t *Tree) SetOnThemeChange(fn func(string)) {
	t.onThemeChange = fn
}

// SetOnOpenHelp registers a callback for the application-level Help overlay.
func (t *Tree) SetOnOpenHelp(fn func()) {
	t.onOpenHelp = fn
}

// SetOnOpenSettings registers a callback for the application-level Settings overlay.
func (t *Tree) SetOnOpenSettings(fn func()) {
	t.onOpenSettings = fn
}

// SetTheme applies a known theme, persists it, and notifies the application.
func (t *Tree) SetTheme(id string) bool {
	resolved, ok := apptheme.ByID(id)
	if !ok {
		return false
	}
	if t.Theme != resolved.ID {
		t.Theme = resolved.ID
	}
	if err := t.SaveState(); err != nil {
		t.recordActionError(err)
	} else {
		t.lastActionError = nil
	}
	if t.onThemeChange != nil {
		t.onThemeChange(resolved.ID)
	}
	return true
}

// ThemeID returns the active theme identifier.
func (t *Tree) ThemeID() string {
	return apptheme.Resolve(t.Theme).ID
}

// SetOnBeforeItemMoved registers a callback that runs before an item changes
// parent. The callback can migrate external data and return a rollback
// function for a later state-save failure. Returning an error aborts the move.
func (t *Tree) SetOnBeforeItemMoved(fn func(*Item, *Item) (func() error, error)) {
	t.onBeforeItemMoved = fn
}

// SetOnItemMoved registers a callback fired after a successful move with
// (item, oldSessionID, newSessionID). The IDs are bare (no profile prefix) and
// stable across renames within the same parent.
func (t *Tree) SetOnItemMoved(fn func(*Item, string, string)) {
	t.onItemMoved = fn
}

// SetOnRename registers a legacy callback that performs external data
// migration before the tree item name changes. New callers should use
// SetOnBeforeRename and SetOnRenameCommitted for transactional persistence.
func (t *Tree) SetOnRename(fn func(*Item, string) error) {
	t.onRename = fn
}

// SetOnBeforeRename registers a reversible external migration hook. The
// rollback runs after the Tree has restored its old in-memory name when the
// subsequent SaveState fails.
func (t *Tree) SetOnBeforeRename(fn func(*Item, string) (func() error, error)) {
	t.onBeforeRename = fn
}

// SetOnRenameCommitted registers a callback invoked only after the renamed
// tree state has been persisted successfully.
func (t *Tree) SetOnRenameCommitted(fn func(*Item, string, string)) {
	t.onRenameCommitted = fn
}

// SetSaveStateFunc replaces persistence with a test seam. Passing nil restores
// the normal atomic state writer.
func (t *Tree) SetSaveStateFunc(fn func() error) {
	t.saveStateOverride = fn
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
	return t.sessionIDOfWithParent(item, item.parent)
}

func (t *Tree) sessionIDOfWithParent(item, parent *Item) string {
	if item == nil {
		return ""
	}
	var parts []string
	for p := parent; p != nil; p = p.parent {
		parts = append([]string{slug.Slug(p.Name)}, parts...)
	}
	parts = append(parts, slug.Slug(item.Name))
	return strings.Join(parts, ".")
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
// persists it to state.json. The error is returned so lifecycle callers can
// surface a failed active-session commit instead of silently continuing.
func (t *Tree) SetActiveSessions(ids map[string]struct{}) error {
	previous := t.activeSessions
	t.SetActiveSessionsInMemory(ids)
	if err := t.SaveState(); err != nil {
		if !stateCommitWasApplied(err) {
			t.SetActiveSessionsInMemory(previous)
		}
		return err
	}
	return nil
}

// SetActiveSessionsInMemory updates active-session state without persisting the
// tree. Transactional rename/move rollback uses this before an explicit save.
func (t *Tree) SetActiveSessionsInMemory(ids map[string]struct{}) {
	if ids == nil {
		t.activeSessions = nil
		return
	}
	owned := make(map[string]struct{}, len(ids))
	for id := range ids {
		owned[id] = struct{}{}
	}
	t.activeSessions = owned
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
	visited := make(map[*Item]struct{})
	var walk func(items []*Item)
	walk = func(items []*Item) {
		for _, it := range items {
			if it == nil {
				continue
			}
			if _, ok := visited[it]; ok {
				continue
			}
			visited[it] = struct{}{}
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
	t.inputError = ""
	t.confirmMode = false
	t.helpMode = false
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
	if t.inputError != "" {
		content += "\nError: " + t.inputError
	}
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
