package main

type focusArea int

const (
	focusTree focusArea = iota
	focusPrimary
	focusKnowledge
)

func (a *App) focusOrder() []focusArea {
	if a.tree == nil {
		return nil
	}
	areas := []focusArea{focusTree}
	if a.container == nil || a.container.Active() == nil {
		return areas
	}
	areas = append(areas, focusPrimary)
	if a.container.Knowledge() != nil {
		areas = append(areas, focusKnowledge)
	}
	return areas
}

func (a *App) currentFocusArea() focusArea {
	if a.sm == nil || a.sm.tab == nil || a.tree == nil {
		return focusTree
	}
	focused := a.sm.tab.Focus()
	if focused == a.tree {
		return focusTree
	}
	if a.container != nil {
		if focused == a.container.Knowledge() ||
			(focused == a.container && a.container.Focused() == a.container.Knowledge()) {
			return focusKnowledge
		}
		if focused == a.container || focused == a.container.Active() {
			return focusPrimary
		}
	}
	return focusTree
}

func (a *App) setFocusArea(area focusArea) {
	if a.sm == nil || a.sm.tab == nil {
		return
	}
	switch area {
	case focusTree:
		a.sm.tab.SetFocus(a.tree)
	case focusPrimary:
		if a.container == nil || a.container.Active() == nil {
			a.sm.tab.SetFocus(a.tree)
			return
		}
		a.container.SetFocus(a.container.Active())
		a.sm.tab.SetFocus(a.container)
	case focusKnowledge:
		if a.container == nil || a.container.Knowledge() == nil {
			a.sm.tab.SetFocus(a.tree)
			return
		}
		a.container.SetFocus(a.container.Knowledge())
		a.sm.tab.SetFocus(a.container)
	}
}

func (a *App) cycleFocus(direction int) {
	areas := a.focusOrder()
	if len(areas) == 0 || direction == 0 {
		return
	}
	current := a.currentFocusArea()
	index := 0
	for i, area := range areas {
		if area == current {
			index = i
			break
		}
	}
	index = (index + direction) % len(areas)
	if index < 0 {
		index += len(areas)
	}
	a.setFocusArea(areas[index])
}
