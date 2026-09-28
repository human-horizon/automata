package main

import (
	"log"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/starframe-dev/warp"

	"github.com/HumanHorizon/automata/internal/tree"
	"github.com/HumanHorizon/automata/internal/ui"
)

func (a *App) handleWindowSizeMsg(msg tea.WindowSizeMsg) tea.Cmd {
	treeW := a.tree.TreeWidth()
	if treeW > 0 && msg.Width > 0 {
		frac := float64(treeW) / float64(msg.Width)
		a.sm.tab.SetSplitFraction(a.tree, frac)
	}
	_, cmd := a.warp.Update(msg)
	return cmd
}

func (a *App) handleClearSessionErrorMsg(msg clearSessionErrorMsg) tea.Cmd {
	if msg.err != nil {
		log.Printf("clearSession %q: %v", msg.sessionID, msg.err)
		if msg.panel != nil {
			msg.panel.SetActionWarning(msg.err.Error())
		}
	}
	return nil
}

func (a *App) handleTreeMetadataSaveMsg(msg treeMetadataSaveMsg) tea.Cmd {
	if msg.generation != a.metadataSaveGeneration || !a.metadataSavePending {
		return nil
	}
	a.metadataSavePending = false
	if !a.metadataSaveDirty {
		return nil
	}
	_ = a.flushPendingTreeMetadata()
	return nil
}

func (a *App) handleStatusWatcherClosedMsg(msg statusWatcherClosedMsg) tea.Cmd {
	if a.isCurrentStatusWatcher(msg.generation, msg.watcher) {
		return a.recoverStatusWatcher()
	}
	return nil
}

func (a *App) handleStatusWatcherErrorMsg(msg statusWatcherErrorMsg) tea.Cmd {
	if !a.isCurrentStatusWatcher(msg.generation, msg.watcher) {
		return nil
	}
	if msg.err != nil {
		log.Printf("automata: status watcher failed: %v", msg.err)
	}
	return a.recoverStatusWatcher()
}

func (a *App) handleTreeStatusChangedMsg(msg treeStatusChangedMsg) tea.Cmd {
	if !a.isCurrentStatusWatcher(msg.generation, msg.watcher) {
		return nil
	}
	a.statusWatchPending = false
	changedPath := filepath.Clean(msg.path)
	if sessionID, ok := a.statusSessionDirs[filepath.Dir(changedPath)]; ok {
		if filepath.Base(changedPath) == "status.json" {
			a.statusReader.Invalidate(sessionID)
			a.refreshStatusBadge(sessionID)
		}
	}
	return a.watchTreeStatusCmd()
}

func (a *App) handleTreeCollapsedMsg(msg tree.TreeCollapsedMsg) tea.Cmd {
	a.sm.tab.ToggleSplitCollapse(a.tree)
	cmds := []tea.Cmd{func() tea.Msg { return a.sm.tab.BroadcastResize() }}
	_, warpCmd := a.warp.Update(msg)
	if warpCmd != nil {
		cmds = append(cmds, warpCmd)
	}
	return tea.Batch(cmds...)
}

func (a *App) handleItemSelectedMsg(msg tree.ItemSelectedMsg) tea.Cmd {
	if a.container == nil {
		_, cmd := a.warp.Update(msg)
		return cmd
	}

	var cmds []tea.Cmd
	active := a.container.Active()
	if tp, ok := active.(*ui.TermPanel); ok && tp != nil {
		if cmd := tp.Start(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		a.sm.tab.SetFocus(tp)
	} else if cp, ok := active.(*ui.ChatPanel); ok && cp != nil {
		if em := cp.ActiveEmulator(); em != nil {
			if cmd := em.Start(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		a.container.SetFocus(cp)
		a.sm.tab.SetFocus(a.container)
	}

	w, h := a.warp.Width(), a.warp.Height()
	if w > 0 && h > 0 {
		_, warpCmd := a.warp.Update(warp.ResizeMsg{Width: w, Height: h})
		if warpCmd != nil {
			cmds = append(cmds, warpCmd)
		}
	}
	if cmd := a.container.Activate(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

func (a *App) handleFolderSelectedMsg(msg tree.FolderSelectedMsg) tea.Cmd {
	if a.container == nil {
		return nil
	}
	a.container.SetFolder(msg.Item)
	cmds := []tea.Cmd{a.container.Activate()}
	w, h := a.warp.Width(), a.warp.Height()
	if w > 0 && h > 0 {
		_, warpCmd := a.warp.Update(warp.ResizeMsg{Width: w, Height: h})
		if warpCmd != nil {
			cmds = append(cmds, warpCmd)
		}
	}
	return tea.Batch(cmds...)
}
