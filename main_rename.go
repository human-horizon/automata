package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/HumanHorizon/automata/internal/kanban"
	"github.com/HumanHorizon/automata/internal/paths"
	"github.com/HumanHorizon/automata/internal/slug"
	"github.com/HumanHorizon/automata/internal/tree"
	"github.com/Starframe/portalis"
)

type renameSessionPlan struct {
	oldID      string
	newID      string
	cwd        string
	taskDomain string
	newDomain  string
}

type renameDomainPlan struct {
	oldDomain string
	newDomain string
}

type renameFamiliarPlan struct {
	ownerOldID string
	ownerNewID string
	oldID      string
	newID      string
	cwd        string
}

type renameTaskMove struct {
	oldPath           string
	newPath           string
	oldAssigned       string
	newAssigned       string
	moved             bool
	assignmentUpdated bool
	createdTargetDir  bool
}

type renamePlan struct {
	sessions  []renameSessionPlan
	domains   []renameDomainPlan
	familiars []renameFamiliarPlan
	taskMoves []renameTaskMove
}

type renameRuntimeSnapshot struct {
	emulators              map[string]*portalis.Emulator
	familiarEmulators      map[string]*portalis.Emulator
	activeSessions         map[string]struct{}
	runningSessions        map[string]struct{}
	currentSessionID       string
	currentSessionCaptured bool
}

type renameDirectoryMove struct {
	oldPath string
	newPath string
}

type renameJSONLMove struct {
	oldID string
	newID string
	cwd   string
}

type renameFamiliarFileMove struct {
	oldOwnerID string
	newOwnerID string
}

type renameAssignmentMove struct {
	path string
	old  string
}

func fullRenameSessionID(profile string, folders []string, name string) string {
	id := slug.SessionName(folders, name)
	if profile == "" {
		return id
	}
	return slug.Slug(profile) + "__" + id
}

func (a *App) renameAgentDir() string {
	if a.piAgentDir != "" {
		return a.piAgentDir
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "/Users/a"
	}
	return filepath.Join(home, ".ai", "just", "pi")
}

func appendRenamePath(parts []string, name string) []string {
	result := make([]string, 0, len(parts)+1)
	result = append(result, parts...)
	return append(result, name)
}

func buildRenamePlan(item *tree.Item, newName, profile string) (*renamePlan, error) {
	if item == nil {
		return nil, fmt.Errorf("item is required")
	}
	plan := &renamePlan{}
	if !item.IsFolder {
		parentFolders := item.Path()
		oldID := fullRenameSessionID(profile, parentFolders, item.Name)
		newID := fullRenameSessionID(profile, parentFolders, newName)
		domain := paths.DomainID(profile, parentFolders)
		plan.sessions = append(plan.sessions, renameSessionPlan{
			oldID:      oldID,
			newID:      newID,
			cwd:        item.CWD,
			taskDomain: domain,
			newDomain:  domain,
		})
		return plan, nil
	}

	var walk func(folder *tree.Item, oldParent, newParent []string)
	walk = func(folder *tree.Item, oldParent, newParent []string) {
		oldName := folder.Name
		newFolderName := oldName
		if folder == item {
			newFolderName = newName
		}
		oldFolders := appendRenamePath(oldParent, oldName)
		newFolders := appendRenamePath(newParent, newFolderName)
		oldDomain := paths.DomainID(profile, oldFolders)
		newDomain := paths.DomainID(profile, newFolders)
		plan.domains = append(plan.domains, renameDomainPlan{
			oldDomain: oldDomain,
			newDomain: newDomain,
		})
		for _, child := range folder.Children {
			if child.IsFolder {
				walk(child, oldFolders, newFolders)
				continue
			}
			plan.sessions = append(plan.sessions, renameSessionPlan{
				oldID:      fullRenameSessionID(profile, oldFolders, child.Name),
				newID:      fullRenameSessionID(profile, newFolders, child.Name),
				cwd:        child.CWD,
				taskDomain: newDomain,
				newDomain:  newDomain,
			})
		}
	}
	walk(item, item.Path(), item.Path())
	return plan, nil
}

func buildMovePlan(item, newParent *tree.Item, profile string) (*renamePlan, error) {
	if item == nil {
		return nil, fmt.Errorf("item is required")
	}
	if newParent != nil && !newParent.IsFolder {
		return nil, fmt.Errorf("move target parent must be a folder")
	}

	oldFolders := item.Path()
	newFolders := []string(nil)
	if newParent != nil {
		newFolders = appendRenamePath(newParent.Path(), newParent.Name)
	}
	plan := &renamePlan{}
	if !item.IsFolder {
		oldID := fullRenameSessionID(profile, oldFolders, item.Name)
		newID := fullRenameSessionID(profile, newFolders, item.Name)
		plan.sessions = append(plan.sessions, renameSessionPlan{
			oldID:      oldID,
			newID:      newID,
			cwd:        item.CWD,
			taskDomain: paths.DomainID(profile, oldFolders),
			newDomain:  paths.DomainID(profile, newFolders),
		})
		return plan, nil
	}

	var walk func(folder *tree.Item, oldParent, newParentPath []string)
	walk = func(folder *tree.Item, oldParent, newParentPath []string) {
		oldFolders := appendRenamePath(oldParent, folder.Name)
		newFolders := appendRenamePath(newParentPath, folder.Name)
		oldDomain := paths.DomainID(profile, oldFolders)
		newDomain := paths.DomainID(profile, newFolders)
		plan.domains = append(plan.domains, renameDomainPlan{
			oldDomain: oldDomain,
			newDomain: newDomain,
		})
		for _, child := range folder.Children {
			if child.IsFolder {
				walk(child, oldFolders, newFolders)
				continue
			}
			plan.sessions = append(plan.sessions, renameSessionPlan{
				oldID:      fullRenameSessionID(profile, oldFolders, child.Name),
				newID:      fullRenameSessionID(profile, newFolders, child.Name),
				cwd:        child.CWD,
				taskDomain: newDomain,
				newDomain:  newDomain,
			})
		}
	}
	walk(item, oldFolders[:len(oldFolders):len(oldFolders)], newFolders)
	return plan, nil
}

func (a *App) applyRenameMappings(plan *renamePlan) {
	oldToNew := make(map[string]string, len(plan.sessions)+len(plan.familiars))
	for _, session := range plan.sessions {
		oldToNew[session.oldID] = session.newID
	}
	for _, familiar := range plan.familiars {
		oldToNew[familiar.oldID] = familiar.newID
	}
	if newID, ok := oldToNew[a.currentSessionID]; ok {
		a.currentSessionID = newID
	}
	if a.container != nil {
		a.container.RenameSessionIDs(oldToNew)
		sessionDomains := make(map[string]string, len(plan.sessions))
		for _, session := range plan.sessions {
			if session.newID != session.oldID {
				sessionDomains[session.newID] = session.newDomain
			}
		}
		a.container.RenameSessionDomains(sessionDomains)
		domainMap := make(map[string]string, len(plan.domains))
		for _, domain := range plan.domains {
			if domain.oldDomain != domain.newDomain {
				domainMap[domain.oldDomain] = domain.newDomain
			}
		}
		a.container.RenameDomains(domainMap)
	}
}

func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func checkRenamePath(oldPath, newPath, label string) error {
	if oldPath == newPath {
		return nil
	}
	oldExists, err := pathExists(oldPath)
	if err != nil {
		return fmt.Errorf("check %s source %q: %w", label, oldPath, err)
	}
	newExists, err := pathExists(newPath)
	if err != nil {
		return fmt.Errorf("check %s target %q: %w", label, newPath, err)
	}
	if newExists {
		return fmt.Errorf("%s target already exists: %s", label, newPath)
	}
	if !oldExists {
		return nil
	}
	return nil
}

func (a *App) prepareRenamePlan(plan *renamePlan) error {
	agentDir := a.renameAgentDir()
	plan.taskMoves = nil
	seenTaskTargets := make(map[string]struct{})
	seenSessionTargets := make(map[string]struct{}, len(plan.sessions))
	for _, session := range plan.sessions {
		if session.oldID == session.newID {
			continue
		}
		if _, exists := seenSessionTargets[session.newID]; exists {
			return fmt.Errorf("session target collision: %s", session.newID)
		}
		seenSessionTargets[session.newID] = struct{}{}

		oldDir := paths.SessionDir(a.profile, session.oldID)
		newDir := paths.SessionDir(a.profile, session.newID)
		if err := checkRenamePath(oldDir, newDir, "session"); err != nil {
			return err
		}
		oldJSONL := paths.FindSessionJSONL(session.oldID, session.cwd, agentDir)
		newJSONL := paths.FindSessionJSONL(session.newID, session.cwd, agentDir)
		if newJSONL != "" && newJSONL != oldJSONL {
			return fmt.Errorf("session JSONL target already exists: %s", newJSONL)
		}

		if session.taskDomain != session.newDomain {
			tasks, err := kanban.ReadAll(session.taskDomain, a.profile)
			if err != nil {
				return fmt.Errorf("read Kanban %s: %w", session.taskDomain, err)
			}
			for _, task := range tasks {
				if task.AssignedTo != session.oldID {
					continue
				}
				newPath := filepath.Join(kanban.KanbanDir(session.newDomain, a.profile), filepath.Base(task.Path))
				if _, exists := seenTaskTargets[newPath]; exists {
					return fmt.Errorf("Kanban task target collision: %s", newPath)
				}
				seenTaskTargets[newPath] = struct{}{}
				exists, err := pathExists(newPath)
				if err != nil {
					return fmt.Errorf("check Kanban task target %q: %w", newPath, err)
				}
				if exists {
					return fmt.Errorf("Kanban task target already exists: %s", newPath)
				}
				plan.taskMoves = append(plan.taskMoves, renameTaskMove{
					oldPath:     task.Path,
					newPath:     newPath,
					oldAssigned: session.oldID,
					newAssigned: session.newID,
				})
			}
		}

		entries, err := paths.ReadFamiliars(a.profile, session.oldID)
		if err != nil {
			return fmt.Errorf("read familiars for %s: %w", session.oldID, err)
		}
		prefix := session.oldID + "__"
		for _, entry := range entries {
			if !strings.HasPrefix(entry.SessionID, prefix) {
				continue
			}
			newFamiliarID := session.newID + entry.SessionID[len(session.oldID):]
			familiar := renameFamiliarPlan{
				ownerOldID: session.oldID,
				ownerNewID: session.newID,
				oldID:      entry.SessionID,
				newID:      newFamiliarID,
				cwd:        session.cwd,
			}
			plan.familiars = append(plan.familiars, familiar)
		}
	}

	seenDomainTargets := make(map[string]struct{}, len(plan.domains))
	for _, domain := range plan.domains {
		if domain.oldDomain == domain.newDomain {
			continue
		}
		if _, exists := seenDomainTargets[domain.newDomain]; exists {
			return fmt.Errorf("domain target collision: %s", domain.newDomain)
		}
		seenDomainTargets[domain.newDomain] = struct{}{}
		oldPath := paths.DomainDir(a.profile, domain.oldDomain)
		newPath := paths.DomainDir(a.profile, domain.newDomain)
		if err := checkRenamePath(oldPath, newPath, "domain"); err != nil {
			return err
		}
	}

	seenFamiliarTargets := make(map[string]struct{}, len(plan.familiars))
	for _, familiar := range plan.familiars {
		if _, exists := seenFamiliarTargets[familiar.newID]; exists {
			return fmt.Errorf("familiar target collision: %s", familiar.newID)
		}
		seenFamiliarTargets[familiar.newID] = struct{}{}
		oldDir := paths.SessionDir(a.profile, familiar.oldID)
		newDir := paths.SessionDir(a.profile, familiar.newID)
		if err := checkRenamePath(oldDir, newDir, "familiar session"); err != nil {
			return err
		}
		oldJSONL := paths.FindSessionJSONL(familiar.oldID, familiar.cwd, agentDir)
		newJSONL := paths.FindSessionJSONL(familiar.newID, familiar.cwd, agentDir)
		if newJSONL != "" && newJSONL != oldJSONL {
			return fmt.Errorf("familiar JSONL target already exists: %s", newJSONL)
		}
	}
	return nil
}

func renamePlanSessionIDs(plan *renamePlan) map[string]struct{} {
	ids := make(map[string]struct{}, len(plan.sessions)+len(plan.familiars))
	for _, session := range plan.sessions {
		if session.oldID != session.newID {
			ids[session.oldID] = struct{}{}
		}
	}
	for _, familiar := range plan.familiars {
		ids[familiar.oldID] = struct{}{}
	}
	return ids
}

func (a *App) captureRenameRuntime(plan *renamePlan) renameRuntimeSnapshot {
	ids := renamePlanSessionIDs(plan)
	snapshot := renameRuntimeSnapshot{
		emulators:         make(map[string]*portalis.Emulator),
		familiarEmulators: make(map[string]*portalis.Emulator),
		activeSessions:    make(map[string]struct{}),
		runningSessions:   make(map[string]struct{}),
	}
	for id := range ids {
		if em, ok := a.emulatorCache[id]; ok {
			snapshot.emulators[id] = em
		}
		if em, ok := a.familiarEmulatorCache[id]; ok {
			snapshot.familiarEmulators[id] = em
		}
		if _, ok := a.activeSessions[id]; ok {
			snapshot.activeSessions[id] = struct{}{}
		}
		if _, ok := a.runningSessions[id]; ok {
			snapshot.runningSessions[id] = struct{}{}
		}
	}
	if _, ok := ids[a.currentSessionID]; ok {
		snapshot.currentSessionID = a.currentSessionID
		snapshot.currentSessionCaptured = true
	}
	return snapshot
}

func (a *App) restoreRenameRuntime(snapshot renameRuntimeSnapshot) error {
	if a.emulatorCache == nil {
		a.emulatorCache = make(map[string]*portalis.Emulator)
	}
	if a.familiarEmulatorCache == nil {
		a.familiarEmulatorCache = make(map[string]*portalis.Emulator)
	}
	if a.activeSessions == nil {
		a.activeSessions = make(map[string]struct{})
	}
	if a.runningSessions == nil {
		a.runningSessions = make(map[string]struct{})
	}
	for id, em := range snapshot.emulators {
		a.emulatorCache[id] = em
	}
	for id, em := range snapshot.familiarEmulators {
		a.familiarEmulatorCache[id] = em
	}
	for id := range snapshot.activeSessions {
		a.activeSessions[id] = struct{}{}
	}
	for id := range snapshot.runningSessions {
		em := snapshot.emulators[id]
		if em == nil {
			em = snapshot.familiarEmulators[id]
		}
		if em == nil {
			continue
		}
		if err := a.startEmulatorSync(em, nil); err != nil {
			return fmt.Errorf("restore emulator %s: %w", id, err)
		}
		a.runningSessions[id] = struct{}{}
		if cmd := em.Update(portalis.PtyReadyMsg{SessionID: id}); cmd != nil {
			a.pendingRuntimeCmds = append(a.pendingRuntimeCmds, cmd)
		}
	}
	if snapshot.currentSessionCaptured {
		a.currentSessionID = snapshot.currentSessionID
	}
	if a.tree != nil {
		a.tree.SetActiveSessions(a.activeSessions)
		a.pendingRuntimeCmds = append(a.pendingRuntimeCmds, a.syncSessionWatchers()...)
	}
	return nil
}

func (a *App) stopRenameSessions(plan *renamePlan) (renameRuntimeSnapshot, error) {
	snapshot := a.captureRenameRuntime(plan)
	ids := renamePlanSessionIDs(plan)
	owners := make([]string, 0, len(ids))
	for id := range ids {
		owners = append(owners, id)
	}
	if err := a.stopSessionRuntimeIDs(owners, stopSessionOptions{
		stopFamiliars:   true,
		persistInactive: true,
	}); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

func (a *App) finalizeRenamePlan(plan *renamePlan) {
	for sessionID := range renamePlanSessionIDs(plan) {
		if err := a.killSessionForProfile(sessionID); err != nil {
			log.Printf("automata: stop jobs for renamed session %s: %v", sessionID, err)
		}
	}
}

func moveRenameDirectory(oldPath, newPath string, moves *[]renameDirectoryMove) error {
	exists, err := pathExists(oldPath)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if err := paths.RenameDirectory(oldPath, newPath); err != nil {
		return err
	}
	*moves = append(*moves, renameDirectoryMove{oldPath: oldPath, newPath: newPath})
	return nil
}

func moveRenameTask(move *renameTaskMove) error {
	targetDir := filepath.Dir(move.newPath)
	if _, err := os.Stat(targetDir); os.IsNotExist(err) {
		move.createdTargetDir = true
	} else if err != nil {
		return err
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return err
	}
	if err := os.Rename(move.oldPath, move.newPath); err != nil {
		return err
	}
	move.moved = true
	if _, err := kanban.AssignTask(move.newPath, move.newAssigned); err != nil {
		return err
	}
	move.assignmentUpdated = true
	return nil
}

func rollbackRenameTask(move *renameTaskMove) {
	if !move.moved {
		return
	}
	if move.assignmentUpdated {
		if _, err := kanban.AssignTask(move.newPath, move.oldAssigned); err != nil {
			log.Printf("automata: rollback Kanban task assignment %s: %v", move.newPath, err)
		}
	}
	if err := os.Rename(move.newPath, move.oldPath); err != nil {
		log.Printf("automata: rollback Kanban task %s -> %s: %v", move.newPath, move.oldPath, err)
	}
	if move.createdTargetDir {
		if err := os.Remove(filepath.Dir(move.newPath)); err != nil && !os.IsNotExist(err) {
			log.Printf("automata: rollback empty Kanban directory %s: %v", filepath.Dir(move.newPath), err)
		}
	}
}

func rollbackRename(
	a *App,
	directoryMoves []renameDirectoryMove,
	jsonlMoves []renameJSONLMove,
	familiarFiles []renameFamiliarFileMove,
	assignmentMoves []renameAssignmentMove,
	taskMoves []renameTaskMove,
) {
	agentDir := a.renameAgentDir()
	for i := len(taskMoves) - 1; i >= 0; i-- {
		rollbackRenameTask(&taskMoves[i])
	}
	for i := len(assignmentMoves) - 1; i >= 0; i-- {
		move := assignmentMoves[i]
		if _, err := kanban.AssignTask(move.path, move.old); err != nil {
			log.Printf("automata: rollback Kanban assignment %s: %v", move.path, err)
		}
	}
	for i := len(familiarFiles) - 1; i >= 0; i-- {
		move := familiarFiles[i]
		if _, err := paths.RewriteFamiliarSessionIDs(a.profile, move.newOwnerID, move.oldOwnerID); err != nil {
			log.Printf("automata: rollback familiars %s -> %s: %v", move.newOwnerID, move.oldOwnerID, err)
		}
	}
	for i := len(jsonlMoves) - 1; i >= 0; i-- {
		move := jsonlMoves[i]
		if _, err := paths.MigrateSessionJSONL(move.newID, move.oldID, move.cwd, agentDir); err != nil {
			log.Printf("automata: rollback JSONL %s -> %s: %v", move.newID, move.oldID, err)
		}
	}
	for i := len(directoryMoves) - 1; i >= 0; i-- {
		move := directoryMoves[i]
		if err := paths.RenameDirectory(move.newPath, move.oldPath); err != nil {
			log.Printf("automata: rollback directory %s -> %s: %v", move.newPath, move.oldPath, err)
		}
	}
}

func (a *App) applyRenamePlan(plan *renamePlan) (func() error, error) {
	agentDir := a.renameAgentDir()
	if err := a.prepareRenamePlan(plan); err != nil {
		return nil, err
	}
	runtimeSnapshot, err := a.stopRenameSessions(plan)
	if err != nil {
		if restoreErr := a.restoreRenameRuntime(runtimeSnapshot); restoreErr != nil {
			return nil, fmt.Errorf("%w; restore runtime: %v", err, restoreErr)
		}
		return nil, err
	}

	var directoryMoves []renameDirectoryMove
	var jsonlMoves []renameJSONLMove
	var familiarFiles []renameFamiliarFileMove
	var assignmentMoves []renameAssignmentMove
	fail := func(err error) (func() error, error) {
		rollbackRename(a, directoryMoves, jsonlMoves, familiarFiles, assignmentMoves, plan.taskMoves)
		if restoreErr := a.restoreRenameRuntime(runtimeSnapshot); restoreErr != nil {
			return nil, fmt.Errorf("%w; restore runtime: %v", err, restoreErr)
		}
		return nil, err
	}

	for _, session := range plan.sessions {
		if session.oldID == session.newID {
			continue
		}
		oldDir := paths.SessionDir(a.profile, session.oldID)
		newDir := paths.SessionDir(a.profile, session.newID)
		if err := moveRenameDirectory(oldDir, newDir, &directoryMoves); err != nil {
			return fail(fmt.Errorf("move session %s: %w", session.oldID, err))
		}
	}
	for _, domain := range plan.domains {
		if domain.oldDomain == domain.newDomain {
			continue
		}
		oldPath := paths.DomainDir(a.profile, domain.oldDomain)
		newPath := paths.DomainDir(a.profile, domain.newDomain)
		if err := moveRenameDirectory(oldPath, newPath, &directoryMoves); err != nil {
			return fail(fmt.Errorf("move domain %s: %w", domain.oldDomain, err))
		}
	}

	for i := range plan.taskMoves {
		if err := moveRenameTask(&plan.taskMoves[i]); err != nil {
			return fail(fmt.Errorf("move Kanban task %s: %w", plan.taskMoves[i].oldPath, err))
		}
	}

	for _, session := range plan.sessions {
		if session.oldID == session.newID || session.taskDomain != session.newDomain {
			continue
		}
		tasks, err := kanban.ReadAll(session.taskDomain, a.profile)
		if err != nil {
			return fail(fmt.Errorf("read Kanban %s: %w", session.taskDomain, err))
		}
		for _, task := range tasks {
			if task.AssignedTo != session.oldID {
				continue
			}
			if _, err := kanban.AssignTask(task.Path, session.newID); err != nil {
				return fail(fmt.Errorf("migrate Kanban assignment %s: %w", task.Path, err))
			}
			assignmentMoves = append(assignmentMoves, renameAssignmentMove{path: task.Path, old: session.oldID})
		}
	}

	for _, session := range plan.sessions {
		if session.oldID == session.newID {
			continue
		}
		if _, err := paths.MigrateSessionJSONL(session.oldID, session.newID, session.cwd, agentDir); err != nil {
			return fail(fmt.Errorf("migrate session JSONL %s: %w", session.oldID, err))
		}
		if paths.FindSessionJSONL(session.newID, session.cwd, agentDir) != "" {
			jsonlMoves = append(jsonlMoves, renameJSONLMove{oldID: session.oldID, newID: session.newID, cwd: session.cwd})
		}
	}

	for _, session := range plan.sessions {
		if session.oldID == session.newID {
			continue
		}
		mapping, err := paths.RewriteFamiliarSessionIDs(a.profile, session.oldID, session.newID)
		if err != nil {
			return fail(fmt.Errorf("migrate familiars %s: %w", session.oldID, err))
		}
		if len(mapping) > 0 {
			familiarFiles = append(familiarFiles, renameFamiliarFileMove{oldOwnerID: session.oldID, newOwnerID: session.newID})
		}
	}
	for _, familiar := range plan.familiars {
		if err := moveRenameDirectory(
			paths.SessionDir(a.profile, familiar.oldID),
			paths.SessionDir(a.profile, familiar.newID),
			&directoryMoves,
		); err != nil {
			return fail(fmt.Errorf("move familiar session %s: %w", familiar.oldID, err))
		}
		if _, err := paths.MigrateSessionJSONL(familiar.oldID, familiar.newID, familiar.cwd, agentDir); err != nil {
			return fail(fmt.Errorf("migrate familiar JSONL %s: %w", familiar.oldID, err))
		}
		if paths.FindSessionJSONL(familiar.newID, familiar.cwd, agentDir) != "" {
			jsonlMoves = append(jsonlMoves, renameJSONLMove{oldID: familiar.oldID, newID: familiar.newID, cwd: familiar.cwd})
		}
	}

	rolledBack := false
	return func() error {
		if rolledBack {
			return nil
		}
		rolledBack = true
		rollbackRename(a, directoryMoves, jsonlMoves, familiarFiles, assignmentMoves, plan.taskMoves)
		return a.restoreRenameRuntime(runtimeSnapshot)
	}, nil
}

func (a *App) renameTreeItem(item *tree.Item, newName string) error {
	plan, err := buildRenamePlan(item, newName, a.profile)
	if err != nil {
		return err
	}
	if _, err := a.applyRenamePlan(plan); err != nil {
		return err
	}
	a.applyRenameMappings(plan)
	a.finalizeRenamePlan(plan)
	return nil
}
