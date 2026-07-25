package tree

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/starframe-dev/warp"
)

// Elements returns a semantic tree of UI elements with screen coordinates.
// This implements warp.ElementProvider so tests can query elements via HTTP.
func (t *Tree) Elements(width, height int) []warp.Element {
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}
	t.width = width
	t.height = height

	var elems []warp.Element
	elems = append(elems, t.headerElements()...)

	if t.popover != nil {
		elems = append(elems, t.popoverElements()...)
	}

	if t.modalActive && t.modal != nil {
		if t.inputMode {
			elems = append(elems, t.inputModalElements()...)
		} else if t.confirmMode {
			elems = append(elems, t.confirmModalElements()...)
		}
		return elems
	}

	elems = append(elems, t.treeElements()...)
	return elems
}


func (t *Tree) headerElements() []warp.Element {
	if t.Collapsed {
		return []warp.Element{
			{
				Role:   "button",
				Name:   "expand",
				Action: "expand",
				Bounds: warp.Bounds{X: 0, Y: 0, W: t.width, H: t.height},
			},
		}
	}

	plusW := lipgloss.Width(" + ")

	return []warp.Element{
		{
			Role:   "header",
			Name:   "Automata",
			Bounds: warp.Bounds{X: 0, Y: 0, W: t.width, H: 1},
			Children: []warp.Element{
				{Role: "button", Name: "+Add", Action: "add", Bounds: warp.Bounds{X: t.width - 1 - plusW, Y: 0, W: plusW, H: 1}},
				{Role: "button", Name: "collapse", Action: "collapse", Bounds: warp.Bounds{X: t.width - 1, Y: 0, W: 1, H: 1}},
			},
		},
	}
}

func (t *Tree) treeElements() []warp.Element {
	if len(t.flat) == 0 {
		return nil
	}

	contentHeight := t.height - 1
	if contentHeight < 1 {
		contentHeight = 1
	}
	t.clampScroll(contentHeight)

	start := t.scroll
	end := start + contentHeight
	if end > len(t.flat) {
		end = len(t.flat)
	}

	var elems []warp.Element
	for i := start; i < end; i++ {
		item := t.flat[i]
		row := 2 + (i - start)
		elems = append(elems, t.itemElement(item, row))
	}
	return elems
}

func (t *Tree) itemElement(item *Item, row int) warp.Element {
	// Compute the new-style label (branch prefix + expand marker + name).
	// We need the flat index for branch info; find it by scanning.
	flatIdx := -1
	for i, f := range t.flat {
		if f == item {
			flatIdx = i
			break
		}
	}
	prefix := ""
	if flatIdx >= 0 {
		bi := computeBranchInfo(t.flat)
		prefix = branchPrefix(item, bi[flatIdx])
	}
	expandM := expandMarker(item)
	plainLabel := prefix + expandM + " " + item.Name
	labelWidth := lipgloss.Width(plainLabel)

	role := "chat"
	if item.IsFolder {
		role = "folder"
	} else if item.IsTerminal {
		role = "terminal"
	}

	elem := warp.Element{
		Role:   role,
		Name:   item.Name,
		Bounds: warp.Bounds{X: 0, Y: row, W: t.width, H: 1},
	}
	// Visible label region (prefix + expand marker + name).
	elem.Children = append(elem.Children, warp.Element{
		Role:   "label",
		Name:   item.Name,
		Bounds: warp.Bounds{X: 0, Y: row, W: labelWidth, H: 1},
	})

	// Menu icon ⋮.
	actionsX := labelWidth
	elem.Children = append(elem.Children,
		warp.Element{Role: "action", Name: "menu:" + item.Name, Action: "menu", Bounds: warp.Bounds{X: actionsX + 2, Y: row, W: 1, H: 1}},
	)

	return elem
}

func (t *Tree) popoverElements() []warp.Element {
	if t.popover == nil {
		return nil
	}

	menuW := t.popover.Width
	if menuW <= 0 {
		menuW = 20
	}
	if menuW > t.width {
		menuW = t.width
	}

	// Recompute clamped position (same logic as Popover.Overlay).
	menuX := t.popover.X
	menuY := t.popover.Y - 1
	boxW := t.popover.Width + 2 // approximate with border
	if boxW <= 0 {
		boxW = 22
	}
	if menuX+boxW > t.width {
		menuX = t.width - boxW
	}
	if menuX < 0 {
		menuX = 0
	}
	if menuY < 0 {
		menuY = 0
	}
	contentHeight := t.height - 1
	if menuY+len(t.popover.Items) > contentHeight {
		menuY = contentHeight - len(t.popover.Items)
		if menuY < 0 {
			menuY = 0
		}
	}

	var elems []warp.Element
	for i, item := range t.popover.Items {
		elems = append(elems, warp.Element{
			Role:   "menu-item",
			Name:   item.Name,
			Action: menuActionName(item.Name),
			Bounds: warp.Bounds{X: menuX, Y: menuY + i, W: menuW, H: 1},
		})
	}
	return elems
}

func menuActionName(name string) string {
	switch strings.ToLower(name) {
	case "new folder":
		return "new-folder"
	case "new chat":
		return "new-chat"
	case "new terminal":
		return "new-terminal"
	case "rename":
		return "rename"
	case "delete":
		return "delete"
	}
	return ""
}

func (t *Tree) inputModalElements() []warp.Element {
	if t.modal == nil {
		return nil
	}
	startX := t.modal.StartX()
	startY := t.modal.StartY()
	boxWidth := t.modal.BoxWidth()

	titleY := startY + 2 // box[2]: title line
	inputY := startY + 3  // box[3]: input line
	btnY := startY + 4   // box[4]: button line

	var elems []warp.Element
	// Modal container spans full 7 box lines.
	elems = append(elems, warp.Element{
		Role:   "modal",
		Name:   t.inputPrompt,
		Bounds: warp.Bounds{X: startX, Y: startY + 1, W: boxWidth, H: 7},
	})

	// Title bar (draggable).
	elems = append(elems, warp.Element{
		Role:   "title-bar",
		Name:   t.inputPrompt,
		Bounds: warp.Bounds{X: startX + 2, Y: titleY, W: boxWidth - 5, H: 1},
	})

	// Close button.
	elems = append(elems, warp.Element{
		Role:   "button",
		Name:   "✕",
		Action: "cancel",
		Bounds: warp.Bounds{X: startX + boxWidth - 3, Y: titleY, W: 1, H: 1},
	})

	// Input field.
	elems = append(elems, warp.Element{
		Role:   "input",
		Name:   t.inputValue,
		Bounds: warp.Bounds{X: startX + 3, Y: inputY, W: boxWidth - 6, H: 1},
	})

	// Buttons.
	btnLine := t.inputButtonLine(boxWidth)
	createStart, createEnd := findBracketPair(btnLine, 0)
	cancelStart, cancelEnd := findBracketPair(btnLine, createEnd)
	contentStart := startX + 3

	if createStart >= 0 {
		elems = append(elems, warp.Element{
			Role:   "button",
			Name:   "[Create]",
			Action: "create",
			Bounds: warp.Bounds{X: contentStart + createStart, Y: btnY, W: createEnd - createStart, H: 1},
		})
	}
	if cancelStart >= 0 {
		elems = append(elems, warp.Element{
			Role:   "button",
			Name:   "[Cancel]",
			Action: "cancel",
			Bounds: warp.Bounds{X: contentStart + cancelStart, Y: btnY, W: cancelEnd - cancelStart, H: 1},
		})
	}

	return elems
}

func (t *Tree) confirmModalElements() []warp.Element {
	if t.modal == nil {
		return nil
	}
	startX := t.modal.StartX()
	startY := t.modal.StartY()
	boxWidth := t.modal.BoxWidth()

	titleY := startY + 2 // box[2]: title line
	btnY := startY + 4   // box[4]: button line

	title := "Delete"
	if t.confirmItem != nil {
		if t.confirmItem.IsFolder {
			title = "Delete folder"
		} else {
			title = "Delete chat"
		}
	}

	var elems []warp.Element
	elems = append(elems, warp.Element{
		Role:   "confirm",
		Name:   title,
		Bounds: warp.Bounds{X: startX, Y: startY + 1, W: boxWidth, H: 7},
	})

	// Title bar.
	elems = append(elems, warp.Element{
		Role:   "title-bar",
		Name:   title,
		Bounds: warp.Bounds{X: startX + 2, Y: titleY, W: boxWidth - 4, H: 1},
	})

	// Buttons.
	btnLine := t.confirmButtonLine(boxWidth)
	delStart, delEnd := findBracketPair(btnLine, 0)
	escStart, escEnd := findBracketPair(btnLine, delEnd)
	contentStart := startX + 3

	if delStart >= 0 {
		elems = append(elems, warp.Element{
			Role:   "button",
			Name:   "[Del]",
			Action: "confirm-delete",
			Bounds: warp.Bounds{X: contentStart + delStart, Y: btnY, W: delEnd - delStart, H: 1},
		})
	}
	if escStart >= 0 {
		elems = append(elems, warp.Element{
			Role:   "button",
			Name:   "[Esc]",
			Action: "cancel",
			Bounds: warp.Bounds{X: contentStart + escStart, Y: btnY, W: escEnd - escStart, H: 1},
		})
	}

	return elems
}

