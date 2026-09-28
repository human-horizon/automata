package tree

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	warp "github.com/starframe-dev/warp"
)

// Update handles Bubbletea messages for the tree.
func (t *Tree) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return t.handleKey(msg)
	case tea.MouseMsg:
		return t.handleMouse(msg)
	}
	return nil
}

// IsModalOpen returns true if a modal (input/menu) is currently open.
func (t *Tree) IsModalOpen() bool {
	return t.modalActive || t.popover != nil
}

func (t *Tree) handleKey(msg tea.KeyMsg) tea.Cmd {
	if t.modalActive {
		return t.handleModalKey(msg)
	}
	if t.popover != nil {
		if t.popover.HandleKey(msg) {
			t.popover = nil
		}
		return nil
	}
	if t.handleNavigationKey(msg.Type) {
		return nil
	}

	switch msg.Type {
	case tea.KeyF1:
		t.OpenHelp()
	case tea.KeyF2:
		if sel := t.SelectedItem(); sel != nil {
			t.startRename(sel)
		}
	case tea.KeyF10:
		if sel := t.SelectedItem(); sel != nil {
			t.showContextMenu(0, 1)
		} else {
			t.showRootMenu(0, 1)
		}
	case tea.KeyEnter:
		return t.selectCurrentItem()
	case tea.KeyEsc:
		t.resetHover()
		t.selected = -1
	}
	return nil
}

func (t *Tree) handleModalKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEnter:
		switch {
		case t.helpMode:
			t.closeModal()
		case t.confirmMode:
			if t.confirmYes != nil {
				t.confirmYes()
			}
			t.closeModal()
		case t.inputMode:
			t.confirmInput()
		}
		return nil
	case tea.KeyF1:
		if t.helpMode {
			t.closeModal()
		}
		return nil
	case tea.KeyEsc:
		t.closeModal()
		return nil
	}

	if t.inputMode {
		t.handleInputModalKey(msg)
		return nil
	}
	if t.confirmMode {
		t.handleConfirmModalKey(msg)
	}
	return nil
}

func (t *Tree) handleInputModalKey(msg tea.KeyMsg) {
	switch msg.Type {
	case tea.KeyBackspace:
		t.deleteInputBefore()
	case tea.KeyDelete:
		t.deleteInputAfter()
	case tea.KeyLeft:
		t.inputCursor--
		t.inputCursorClamp()
	case tea.KeyRight:
		t.inputCursor++
		t.inputCursorClamp()
	case tea.KeyHome:
		t.inputCursor = 0
	case tea.KeyEnd:
		t.inputCursor = len(t.inputRunes())
	case tea.KeySpace:
		t.insertInput([]rune{' '})
	case tea.KeyRunes:
		t.insertInput(msg.Runes)
	default:
		return
	}
	t.updateInputModal()
}

func (t *Tree) handleConfirmModalKey(msg tea.KeyMsg) {
	if msg.Type != tea.KeyRunes || len(msg.Runes) != 1 {
		return
	}
	switch msg.Runes[0] {
	case 'y', 'Y':
		if t.confirmYes != nil {
			t.confirmYes()
		}
		t.closeModal()
	case 'n', 'N':
		t.closeModal()
	}
}

func (t *Tree) handleNavigationKey(key tea.KeyType) bool {
	switch key {
	case tea.KeyUp:
		t.SelectPrev()
	case tea.KeyDown:
		t.SelectNext()
	case tea.KeyHome:
		t.SelectFirst()
	case tea.KeyEnd:
		t.SelectLast()
	case tea.KeyPgUp:
		t.SelectPage(-1)
	case tea.KeyPgDown:
		t.SelectPage(1)
	case tea.KeyLeft:
		t.NavigateLeft()
	case tea.KeyRight:
		t.NavigateRight()
	default:
		return false
	}
	return true
}

func (t *Tree) selectCurrentItem() tea.Cmd {
	if t.selected < 0 || t.selected >= len(t.flat) {
		return nil
	}
	sel := t.flat[t.selected]
	if sel.IsFolder {
		t.ToggleFolder(sel)
		if t.onSelectFolder != nil {
			t.onSelectFolder(sel)
		}
		return func() tea.Msg {
			return FolderSelectedMsg{Item: sel}
		}
	}
	if t.onSelectChat == nil {
		return nil
	}
	t.onSelectChat(sel)
	return func() tea.Msg {
		return ItemSelectedMsg{Item: sel}
	}
}

func (t *Tree) handleMouse(msg tea.MouseMsg) tea.Cmd {
	if t.modalActive {
		if t.modal != nil {
			t.modal.EnsureDimensions(t.width, t.height)
			t.modal.HandleMouse(msg)
		}
		return nil
	}
	if t.inputMode || t.confirmMode {
		return nil
	}
	if t.Collapsed {
		return t.handleCollapsedMouse(msg)
	}

	// Popover takes priority when it consumes the event. Clicks outside it
	// intentionally continue to the underlying tree.
	if t.popover != nil && t.popover.HandleMouse(msg) {
		return nil
	}
	if msg.Y == 0 {
		return t.handleHeaderMouse(msg)
	}
	if msg.Y == t.height-treeFooterHeight {
		return t.handleFooterMouse(msg)
	}
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonRight {
		return t.handleRightPress(msg)
	}
	return t.handleTreeMouseAction(msg)
}

func (t *Tree) handleCollapsedMouse(msg tea.MouseMsg) tea.Cmd {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return nil
	}
	t.Collapsed = false
	return func() tea.Msg {
		return TreeCollapsedMsg{Collapsed: false}
	}
}

func (t *Tree) handleHeaderMouse(msg tea.MouseMsg) tea.Cmd {
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		return t.handleToolbarClick(msg)
	}
	return nil
}

func (t *Tree) handleFooterMouse(msg tea.MouseMsg) tea.Cmd {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return nil
	}
	switch {
	case msg.X < 6:
		if t.onOpenHelp != nil {
			t.onOpenHelp()
		} else {
			t.OpenHelp()
		}
	case msg.X >= 8 && msg.X < 16:
		if t.onOpenSettings != nil {
			t.onOpenSettings()
		} else {
			t.showSettingsMenu(int(msg.X), int(msg.Y))
		}
	}
	return nil
}

func (t *Tree) handleRightPress(msg tea.MouseMsg) tea.Cmd {
	idx := t.flatIndexAt(int(msg.Y))
	if idx >= 0 && idx < len(t.flat) {
		t.selected = idx
		t.showContextMenu(int(msg.X), int(msg.Y))
	} else {
		t.showRootMenu(int(msg.X), int(msg.Y))
	}
	return nil
}

func (t *Tree) handleTreeMouseAction(msg tea.MouseMsg) tea.Cmd {
	switch msg.Action {
	case tea.MouseActionPress:
		switch msg.Button {
		case tea.MouseButtonLeft:
			return t.handleLeftPress(msg)
		case tea.MouseButtonWheelUp:
			t.scroll--
			t.clampScroll(t.contentHeight())
		case tea.MouseButtonWheelDown:
			t.scroll++
			t.clampScroll(t.contentHeight())
		}
	case tea.MouseActionMotion:
		t.handleMouseMotion(msg)
	case tea.MouseActionRelease:
		if t.dragMode {
			return t.handleDragDrop(msg)
		}
		if t.dragItem != nil {
			return t.handleLeftRelease(msg)
		}
	}
	return nil
}

func (t *Tree) handleToolbarClick(msg tea.MouseMsg) tea.Cmd {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return nil
	}

	rootMenuWidth := lipgloss.Width(" ⋮ ")
	plusWidth := lipgloss.Width(" + ")
	plusStart := t.width - 1 - plusWidth
	rootMenuStart := plusStart - rootMenuWidth
	if int(msg.X) >= rootMenuStart && int(msg.X) < plusStart {
		t.showRootMenu(int(msg.X), int(msg.Y)+1)
		return nil
	}

	// Show a dropdown popover with create options.
	items := []warp.PopoverItem{
		{Name: "+ Folder", Action: func() { t.startCreateInput("Folder name:", t.CreateFolder) }},
		{Name: "+ Chat", Action: func() { t.startCreateInput("Chat name:", t.CreateChat) }},
		{Name: "+ Terminal", Action: func() { t.startCreateInput("Terminal name:", t.CreateTerminal) }},
	}

	t.popover = &warp.Popover{
		Items:   items,
		X:       int(msg.X),
		Y:       int(msg.Y),
		OnClose: func() { t.popover = nil },
	}
	return nil
}
func (t *Tree) handleLeftPress(msg tea.MouseMsg) tea.Cmd {
	idx := t.flatIndexAt(int(msg.Y))
	if idx < 0 || idx >= len(t.flat) {
		return nil
	}

	item := t.flat[idx]
	// Remember the previously-selected row so the release handler can decide
	// whether this is a first focus (don't toggle) or a second click on an
	// already-focused folder (do toggle).
	t.prevSelectedIdx = t.selected
	t.selected = idx

	// Check for action icon click (stop or menu)
	action := t.actionAt(int(msg.X), int(msg.Y))
	switch action {
	case "stop":
		if t.onStopSession != nil {
			t.onStopSession(item)
		}
		return func() tea.Msg {
			return StopSessionMsg{Item: item}
		}
	case "menu":
		t.showContextMenu(int(msg.X), int(msg.Y))
		return nil
	}

	// Start drag & drop (left-click on item content, not folder toggle).
	// Record the starting cell; `handleMouseMotion` promotes to a real drag
	// once the cursor has moved more than a couple of cells. Until then this
	// behaves like a regular click on release.
	if msg.X >= 0 && msg.X < t.width {
		t.dragMode = false
		t.dragStarted = false
		t.dragItem = item
		t.dragStartX = int(msg.X)
		t.dragStartY = int(msg.Y)
		return nil
	}

	return nil
}

func (t *Tree) handleLeftRelease(msg tea.MouseMsg) tea.Cmd {
	defer func() {
		t.dragItem = nil
		t.prevSelectedIdx = -1
	}()

	idx := t.flatIndexAt(int(msg.Y))
	if idx < 0 || idx >= len(t.flat) {
		return nil
	}

	t.selected = idx
	sel := t.flat[idx]
	if sel.IsFolder {
		// Toggle only on the second click: when this row was already selected
		// before the press. A fresh click just moves focus.
		if t.prevSelectedIdx == idx {
			t.ToggleFolder(sel)
		}
		if t.onSelectFolder != nil {
			t.onSelectFolder(sel)
		}
		return func() tea.Msg {
			return FolderSelectedMsg{Item: sel}
		}
	}
	if t.onSelectChat != nil {
		t.onSelectChat(sel)
		return func() tea.Msg {
			return ItemSelectedMsg{Item: sel}
		}
	}
	return nil
}

func (t *Tree) handleDragDrop(msg tea.MouseMsg) tea.Cmd {
	defer func() {
		t.dragMode = false
		t.dragItem = nil
		t.dragTargetIdx = -1
	}()

	selected := t.ItemAt(t.selected)
	dragItem := t.dragItem
	if selected == nil || dragItem == nil {
		return nil
	}

	targetIdx := t.flatIndexAt(int(msg.Y))
	if targetIdx < 0 || targetIdx >= len(t.flat) {
		return nil
	}

	target := t.flat[targetIdx]
	t.moveItem(dragItem, target)
	return nil
}

// motionThrottle is the minimum interval between two processed motion
// events. With all-motion (mode 1003) bubbletea fires one event per
// cursor pixel, so without throttling a normal hover spins the update
// loop to 100% CPU. 120 FPS keeps hover affordances silky-smooth while
// still capping the event rate.
const motionThrottle = 8 * time.Millisecond

func (t *Tree) handleMouseMotion(msg tea.MouseMsg) {
	// Time-based throttle for all-motion events. Drag must always run so
	// the drop target tracks the cursor exactly.
	if !t.dragMode {
		now := time.Now()
		if !t.lastMotionAt.IsZero() && now.Sub(t.lastMotionAt) < motionThrottle {
			return
		}
		t.lastMotionAt = now
	}

	// Promote a held press into a real drag once the cursor has moved
	// more than a couple of cells from the press origin. Until then the
	// press behaves like a normal click and is forwarded as such on release.
	if t.dragItem != nil && !t.dragMode {
		dx := int(msg.X) - t.dragStartX
		if dx < 0 {
			dx = -dx
		}
		dy := int(msg.Y) - t.dragStartY
		if dy < 0 {
			dy = -dy
		}
		if dx+dy >= 2 {
			t.dragMode = true
			t.dragStarted = true
		}
	}

	idx := t.flatIndexAt(int(msg.Y))
	if idx < 0 || idx >= len(t.flat) {
		t.hoverIdx = -1
		return
	}
	t.hoverIdx = idx

	// Track action hover state.
	t.actionIcon = ""
	switch t.actionAt(int(msg.X), int(msg.Y)) {
	case "stop":
		t.actionIcon = "hover-stop"
	case "menu":
		t.actionIcon = "hover-menu"
	}

	// While dragging, remember the last hovered row so the release handler
	// knows which item to drop onto.
	if t.dragMode {
		t.dragTargetIdx = idx
	}
}

// actionAt checks if the click is on an action icon (stop or menu).
func (t *Tree) actionAt(x, y int) (action string) {
	idx := t.flatIndexAt(y)
	if idx < 0 || idx >= len(t.flat) {
		return ""
	}

	item := t.flat[idx]

	// The action icons are appended after the label. Find their positions by
	// scanning the rendered line; this is more robust than computing widths
	// because icons can be multi-cell.
	isHover := idx == t.hoverIdx
	isSelected := idx == t.selected
	hasStop := t.IsActiveSession(item) && !item.IsFolder
	if !hasStop && !isHover && !isSelected {
		return ""
	}

	bi := t.branchInfo
	if len(bi) != len(t.flat) {
		bi = computeBranchInfo(t.flat)
		t.branchInfo = bi
	}
	itemBi := branchInfo{}
	if idx < len(bi) {
		itemBi = bi[idx]
	}

	rendered := stripANSI(t.renderItemLine(item, t.width, isSelected, isHover, itemBi, 0))
	if x < 0 || x >= lipgloss.Width(rendered) {
		return ""
	}

	// Locate stop and menu glyphs by their visual column. Wide characters
	// (e.g. emoji icons) occupy more than one byte/rune, so strings.Index
	// would return the wrong coordinate for mouse matching.
	stopX := -1
	menuX := -1
	if hasStop {
		stopX = visualIndexOf(rendered, "■")
	}
	if isHover || isSelected {
		menuX = visualIndexOf(rendered, "⋮")
	}

	if stopX >= 0 && x == stopX {
		return "stop"
	}
	if menuX >= 0 && x == menuX {
		return "menu"
	}
	return ""
}

// visualIndexOf returns the visual (screen) column of the first rune of sym
// within s, or -1 if not found.
func visualIndexOf(s, sym string) int {
	idx := strings.Index(s, sym)
	if idx < 0 {
		return -1
	}
	return lipgloss.Width(s[:idx])
}

// inputButtonLine returns the rendered text of the input modal button row.
func (t *Tree) inputButtonLine(boxWidth int) string {
	innerWidth := boxWidth - 6
	btnLine := " [Create]  [Cancel] "
	if lipgloss.Width(btnLine) > innerWidth {
		btnLine = " [+]  [×] "
	}
	pad := max(0, innerWidth-lipgloss.Width(btnLine))
	return btnLine + strings.Repeat(" ", pad)
}

// confirmButtonLine returns the rendered text of the confirmation button row.
func (t *Tree) confirmButtonLine(boxWidth int) string {
	innerWidth := boxWidth - 6
	btnLine := " [Del]  [Esc] "
	pad := max(0, innerWidth-lipgloss.Width(btnLine))
	return btnLine + strings.Repeat(" ", pad)
}

// findBracketPair finds the first "[...]" pair in s starting at or after start.
func findBracketPair(s string, start int) (int, int) {
	open := strings.IndexByte(s[start:], '[')
	if open < 0 {
		return -1, -1
	}
	open += start
	closeIdx := strings.IndexByte(s[open+1:], ']')
	if closeIdx < 0 {
		return -1, -1
	}
	closeIdx += open + 1 + 1 // include the ']' itself
	return open, closeIdx
}

// VisibleCount returns the number of visible items.
func (t *Tree) VisibleCount() int {
	return len(t.flat)
}
