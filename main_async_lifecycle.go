package main

import (
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"

	"github.com/HumanHorizon/automata/internal/atomicfile"
	"github.com/HumanHorizon/automata/internal/paths"
	"github.com/HumanHorizon/automata/internal/ui"
	"github.com/Starframe/portalis"
	tea "github.com/charmbracelet/bubbletea"
)

type runtimeJobsCompletedMsg struct {
	operationID uint64
	operation   string
	sessionIDs  []string
	deletePlan  *preparedDeleteRuntime
	renamePlan  *renamePlan
	err         error
}

type runtimeJobsSnapshot struct {
	operationID uint64
	operation   string
	sessionIDs  []string
	prepared    []preparedSessionJobs
	deletePlan  *preparedDeleteRuntime
	renamePlan  *renamePlan
}

type sessionStopJobsCompletedMsg struct {
	operationID uint64
	ownerIDs    []string
	sessionIDs  []string
	err         error
}

func (a *App) runtimeJobsCmd(snapshot runtimeJobsSnapshot) tea.Cmd {
	snapshot.sessionIDs = slices.Clone(snapshot.sessionIDs)
	snapshot.prepared = slices.Clone(snapshot.prepared)
	if snapshot.deletePlan != nil {
		plan := *snapshot.deletePlan
		plan.ownerIDs = slices.Clone(plan.ownerIDs)
		plan.sessionIDs = slices.Clone(plan.sessionIDs)
		plan.jobs = slices.Clone(plan.jobs)
		snapshot.deletePlan = &plan
	}
	if snapshot.renamePlan != nil {
		plan := *snapshot.renamePlan
		plan.sessions = slices.Clone(plan.sessions)
		plan.domains = slices.Clone(plan.domains)
		plan.familiars = slices.Clone(plan.familiars)
		plan.jobStops = slices.Clone(plan.jobStops)
		snapshot.renamePlan = &plan
	}
	return func() tea.Msg {
		return runtimeJobsCompletedMsg{
			operationID: snapshot.operationID,
			operation:   snapshot.operation,
			sessionIDs:  snapshot.sessionIDs,
			deletePlan:  snapshot.deletePlan,
			renamePlan:  snapshot.renamePlan,
			err:         a.executePreparedSessionJobs(snapshot.prepared),
		}
	}
}

func (a *App) stopSessionRuntimeCmd(sessionID string) (tea.Cmd, error) {
	ownerIDs := []string{sessionID}
	opts := stopSessionOptions{stopFamiliars: true, persistInactive: true}
	sessionIDs := a.expandRuntimeSessionIDs(ownerIDs, opts)
	operationID, err := a.reserveRuntimeOperation(sessionIDs)
	if err != nil {
		return nil, err
	}
	return func() tea.Msg {
		prepared, err := a.prepareSessionJobs(sessionIDs)
		if err == nil {
			err = a.executePreparedSessionJobs(prepared)
		}
		return sessionStopJobsCompletedMsg{
			operationID: operationID,
			ownerIDs:    ownerIDs,
			sessionIDs:  sessionIDs,
			err:         err,
		}
	}, nil
}

func (a *App) handleClearSessionJobsCompleted(result clearSessionJobsCompletedMsg) tea.Cmd {
	if !a.runtimeOperationOwned(result.operationID, result.sessionIDs...) {
		log.Printf("automata: ignore stale Clear jobs completion for %q", result.sessionID)
		return nil
	}
	if result.runtimeCommitted {
		stateErr := a.stopSessionRuntimeIDs([]string{result.sessionID}, stopSessionOptions{
			stopFamiliars:      true,
			persistInactive:    true,
			familiarSessionIDs: result.sessionIDs,
		})
		result.err = errors.Join(result.err, stateErr)
		if result.registryCleared {
			panel := a.clearSessionPanel(result.panel)
			if panel != nil && panel.SessionID() == result.sessionID {
				panel.HandleSessionClear()
			}
		}
		a.pendingBubbleTeaCmds = append(a.pendingBubbleTeaCmds, a.syncSessionWatchers()...)
	}
	if result.err != nil {
		a.releaseRuntimeOperation(result.operationID)
		if result.panel == nil && a.tree != nil {
			a.tree.RecordActionWarning(result.err)
		}
		return a.handleClearSessionErrorMsg(clearSessionErrorMsg{
			sessionID: result.sessionID,
			panel:     result.panel,
			err:       result.err,
		})
	}
	em := a.newChatEmulatorForPendingOperation(result.sessionID)
	profile := a.profile
	sessionIDs := slices.Clone(result.sessionIDs)
	return func() tea.Msg {
		if em == nil {
			return clearSessionRestartCompletedMsg{
				sessionID: result.sessionID, panel: result.panel, operationID: result.operationID,
				err: fmt.Errorf("cannot restart chat %q: no Pi emulator is available", result.sessionID),
			}
		}
		if err := clearHumanProjections(profile, result.sessionID, sessionIDs); err != nil {
			return clearSessionRestartCompletedMsg{
				sessionID: result.sessionID, panel: result.panel, operationID: result.operationID,
				err: err,
			}
		}
		if err := a.startEmulatorSync(em, nil); err != nil {
			em.Stop()
			return clearSessionRestartCompletedMsg{
				sessionID: result.sessionID, panel: result.panel, operationID: result.operationID,
				emulator: em, err: fmt.Errorf("restart chat %q: %w", result.sessionID, err),
			}
		}
		return clearSessionRestartCompletedMsg{
			sessionID: result.sessionID, panel: result.panel, operationID: result.operationID,
			emulator: em,
		}
	}
}

func (a *App) handleClearSessionRestartCompleted(result clearSessionRestartCompletedMsg) tea.Cmd {
	if !a.runtimeOperationOwned(result.operationID, result.sessionID) {
		if result.emulator != nil {
			result.emulator.Stop()
		}
		log.Printf("automata: ignore stale Clear restart completion for %q", result.sessionID)
		return nil
	}
	if result.err != nil || result.emulator == nil {
		a.releaseRuntimeOperation(result.operationID)
		if result.emulator != nil {
			result.emulator.Stop()
		}
		if result.err == nil {
			result.err = fmt.Errorf("restart chat %q returned no emulator", result.sessionID)
		}
		if result.panel == nil && a.tree != nil {
			a.tree.RecordActionWarning(result.err)
		}
		return a.handleClearSessionErrorMsg(clearSessionErrorMsg{
			sessionID: result.sessionID,
			panel:     result.panel,
			err:       result.err,
		})
	}
	if a.emulatorCache == nil {
		a.emulatorCache = make(map[string]*portalis.Emulator)
	}
	a.emulatorCache[result.sessionID] = result.emulator
	if panel := a.clearSessionPanel(result.panel); panel != nil {
		for _, session := range panel.Sessions() {
			if session.Em() != nil && session.Em().SessionID == result.sessionID {
				session.SetEm(result.emulator)
				break
			}
		}
	}
	cmd, _ := a.routeCachedEmulatorMessage(portalis.PtyReadyMsg{SessionID: result.sessionID})
	a.releaseRuntimeOperation(result.operationID)
	return cmd
}

func (a *App) clearSessionPanel(panel *ui.ChatPanel) *ui.ChatPanel {
	if panel != nil || a.container == nil {
		return panel
	}
	active := a.container.Active()
	panel, _ = active.(*ui.ChatPanel)
	return panel
}

func (a *App) handleSessionStopJobsCompleted(result sessionStopJobsCompletedMsg) {
	if !a.runtimeOperationOwned(result.operationID, result.sessionIDs...) {
		log.Printf("automata: ignore stale stop completion for %v", result.sessionIDs)
		return
	}
	defer a.releaseRuntimeOperation(result.operationID)
	if result.err != nil {
		a.recordRuntimeWarning(fmt.Errorf("stop session jobs: %w", result.err))
		return
	}
	stopErr := a.stopSessionRuntimeIDs(result.ownerIDs, stopSessionOptions{
		stopFamiliars:      true,
		persistInactive:    true,
		familiarSessionIDs: result.sessionIDs,
	})
	if stopErr != nil {
		a.recordRuntimeWarning(fmt.Errorf("stop session runtime: %w", stopErr))
	}
	a.pendingBubbleTeaCmds = append(a.pendingBubbleTeaCmds, a.syncSessionWatchers()...)
}

func (a *App) handleRuntimeJobsCompleted(result runtimeJobsCompletedMsg) {
	if result.operationID != 0 && !a.runtimeOperationOwned(result.operationID, result.sessionIDs...) {
		log.Printf("automata: ignore stale %s completion for %v", result.operation, result.sessionIDs)
		return
	}
	if result.deletePlan != nil {
		if err := a.commitDeletedTreeRuntimeState(result.deletePlan); err != nil {
			a.recordRuntimeWarning(fmt.Errorf("%s runtime state: %w", result.operation, err))
		}
		a.pendingBubbleTeaCmds = append(a.pendingBubbleTeaCmds, a.syncSessionWatchers()...)
	}
	if result.renamePlan != nil {
		a.applyRenameMappings(result.renamePlan)
	}
	a.releaseRuntimeOperation(result.operationID)
	if result.err != nil {
		a.recordRuntimeWarning(fmt.Errorf("%s: %w", result.operation, result.err))
	}
}

func (a *App) recordRuntimeWarning(err error) {
	if err == nil {
		return
	}
	log.Printf("automata: %v", err)
	if a.tree != nil {
		a.tree.RecordActionWarning(err)
	}
}

func (a *App) reserveRuntimeOperation(sessionIDs []string) (uint64, error) {
	unique := make(map[string]struct{}, len(sessionIDs))
	ids := make([]string, 0, len(sessionIDs))
	for _, sessionID := range sessionIDs {
		if sessionID == "" {
			continue
		}
		if _, exists := unique[sessionID]; exists {
			continue
		}
		unique[sessionID] = struct{}{}
		ids = append(ids, sessionID)
	}
	if len(ids) == 0 {
		return 0, nil
	}
	if a.pendingRuntimeOperations == nil {
		a.pendingRuntimeOperations = make(map[string]uint64)
	}
	for _, sessionID := range ids {
		if operationID, exists := a.pendingRuntimeOperations[sessionID]; exists {
			return 0, fmt.Errorf("runtime operation %d is already pending for session %q", operationID, sessionID)
		}
	}
	a.runtimeOperationGeneration++
	if a.runtimeOperationGeneration == 0 {
		a.runtimeOperationGeneration++
	}
	operationID := a.runtimeOperationGeneration
	for _, sessionID := range ids {
		a.pendingRuntimeOperations[sessionID] = operationID
	}
	return operationID, nil
}

func (a *App) runtimeOperationOwned(operationID uint64, sessionIDs ...string) bool {
	if operationID == 0 {
		return false
	}
	for _, sessionID := range sessionIDs {
		if sessionID == "" || a.pendingRuntimeOperations[sessionID] != operationID {
			return false
		}
	}
	return true
}

func (a *App) releaseRuntimeOperation(operationID uint64) {
	if operationID == 0 {
		return
	}
	for sessionID, owner := range a.pendingRuntimeOperations {
		if owner == operationID {
			delete(a.pendingRuntimeOperations, sessionID)
		}
	}
}

func (a *App) runtimeOperationPending(sessionID string) bool {
	return sessionID != "" && a.pendingRuntimeOperations[sessionID] != 0
}

func (a *App) runtimeOperationBlocksSession(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	for reservedID, operationID := range a.pendingRuntimeOperations {
		if operationID == 0 {
			continue
		}
		if sessionID == reservedID || strings.HasPrefix(sessionID, reservedID+"__") || strings.HasPrefix(reservedID, sessionID+"__") {
			return true
		}
	}
	return false
}

func (a *App) requestFamiliarCleanup(panel *ui.ChatPanel, familiarID string, em *portalis.Emulator, kind ui.FamiliarCleanupKind) tea.Cmd {
	ownerSessionID := ""
	if panel != nil {
		ownerSessionID = panel.SessionID()
	}
	result := ui.FamiliarCleanupResultMsg{
		Panel:             panel,
		OwnerSessionID:    ownerSessionID,
		FamiliarSessionID: familiarID,
		Emulator:          em,
		Kind:              kind,
		Phase:             ui.FamiliarJobsStopped,
	}
	if err := paths.ValidateFamiliarSessionID(ownerSessionID, familiarID); err != nil {
		result.Err = fmt.Errorf("invalid familiar session ID: %w", err)
		return func() tea.Msg { return result }
	}
	operationID, err := a.reserveRuntimeOperation([]string{ownerSessionID, familiarID})
	if err != nil {
		result.Err = err
		return func() tea.Msg { return result }
	}
	result.OperationID = operationID
	return func() tea.Msg {
		prepared, err := a.prepareSessionJobs([]string{familiarID})
		if err != nil {
			result.Err = err
			return result
		}
		if err := a.executePreparedSessionJobs(prepared); err != nil {
			result.Err = fmt.Errorf("stop familiar jobs: %w", err)
			return result
		}
		if kind == ui.FamiliarCloseCleanup {
			if err := removeFamiliarRegistry(a.profile, ownerSessionID, familiarID); err != nil {
				result.Err = fmt.Errorf("remove familiar %q from registry: %w", familiarID, err)
				result.Committed = atomicfile.IsCommitted(err)
				return result
			}
		}
		result.Committed = true
		return result
	}
}

func (a *App) handleFamiliarCleanupResult(result ui.FamiliarCleanupResultMsg) tea.Cmd {
	if !a.runtimeOperationOwned(result.OperationID, result.OwnerSessionID, result.FamiliarSessionID) {
		if result.OperationID != 0 {
			log.Printf("automata: ignore stale familiar cleanup completion for %q", result.FamiliarSessionID)
		}
		if result.OperationID == 0 {
			if panel := result.Panel; panel != nil {
				panel.HandleFamiliarCleanupResult(result)
			}
		}
		return nil
	}

	if !result.Committed {
		a.releaseRuntimeOperation(result.OperationID)
		if result.Panel != nil {
			result.Panel.HandleFamiliarCleanupResult(result)
		}
		return nil
	}

	if result.Phase == ui.FamiliarHistoryRemoved {
		a.releaseRuntimeOperation(result.OperationID)
		a.finishFamiliarCleanup(result)
		return nil
	}
	if result.Phase != ui.FamiliarJobsStopped {
		a.releaseRuntimeOperation(result.OperationID)
		log.Printf("automata: ignore unexpected familiar cleanup phase %d for %q", result.Phase, result.FamiliarSessionID)
		return nil
	}

	em := result.Emulator
	if em == nil {
		em = a.familiarEmulatorCache[result.FamiliarSessionID]
	}
	if em == nil {
		em = a.emulatorCache[result.FamiliarSessionID]
	}
	cwd, cleanupErr := a.commitFamiliarRuntimeCleanup(result.FamiliarSessionID, em)
	result.Err = errors.Join(result.Err, cleanupErr)

	if result.Kind == ui.FamiliarExternalRemovalCleanup {
		a.releaseRuntimeOperation(result.OperationID)
		a.finishFamiliarCleanup(result)
		return nil
	}

	result.Phase = ui.FamiliarHistoryRemoved
	return func() tea.Msg {
		if err := deleteFamiliarSessionHistory(result.FamiliarSessionID, cwd, a.piAgentDir); err != nil {
			result.Err = errors.Join(result.Err, err)
		}
		return result
	}
}

func (a *App) commitFamiliarRuntimeCleanup(sessionID string, em *portalis.Emulator) (string, error) {
	if em == nil {
		em = a.familiarEmulatorCache[sessionID]
	}
	if em == nil {
		em = a.emulatorCache[sessionID]
	}
	cwd := ""
	if em != nil {
		cwd = em.CWD()
		em.Stop()
	}
	delete(a.emulatorCache, sessionID)
	delete(a.familiarEmulatorCache, sessionID)
	delete(a.runningSessions, sessionID)
	delete(a.activeSessions, sessionID)
	var err error
	if persistErr := a.persistRuntimeActiveSessions(); persistErr != nil {
		err = fmt.Errorf("persist inactive familiar session: %w", persistErr)
	}
	a.pendingBubbleTeaCmds = append(a.pendingBubbleTeaCmds, a.syncSessionWatchers()...)
	return cwd, err
}

func (a *App) finishFamiliarCleanup(result ui.FamiliarCleanupResultMsg) {
	if result.Err != nil {
		log.Printf("automata: committed familiar cleanup warning for %q: %v", result.FamiliarSessionID, result.Err)
		if a.tree != nil {
			a.tree.RecordActionWarning(result.Err)
		}
	}
	if result.Panel != nil {
		result.Panel.HandleFamiliarCleanupResult(result)
	}
}

func deleteFamiliarSessionHistory(sessionID, cwd, piAgentDir string) error {
	historyPath, err := paths.FindSessionJSONLChecked(sessionID, cwd, piAgentDir)
	if err != nil {
		return fmt.Errorf("inspect familiar JSONL %q: %w", sessionID, err)
	}
	if historyPath == "" {
		return nil
	}
	if _, err := paths.DeleteSessionJSONL(sessionID, cwd, piAgentDir); err != nil {
		return fmt.Errorf("delete familiar JSONL %q: %w", sessionID, err)
	}
	return nil
}
