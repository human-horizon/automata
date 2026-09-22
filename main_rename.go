package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	akjobs "github.com/HumanHorizon/automata/internal/ai-knowledge/jobs"
	"github.com/HumanHorizon/automata/internal/kanban"
	"github.com/HumanHorizon/automata/internal/paths"
	"github.com/HumanHorizon/automata/internal/slug"
	"github.com/HumanHorizon/automata/internal/tree"
	"github.com/HumanHorizon/automata/internal/ui"
)

type renameSessionPlan struct {
	oldID string
	newID string
	cwd   string
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

type renamePlan struct {
	sessions  []renameSessionPlan
	domains   []renameDomainPlan
	familiars []renameFamiliarPlan
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

func renameDomainID(profile string, folders []string) string {
	parts := make([]string, 0, len(folders))
	for _, folder := range folders {
		parts = append(parts, slug.Slug(folder))
	}
	domain := strings.Join(parts, ".")
	if profile == "" {
		return domain
	}
	return slug.Slug(profile) + "__" + domain
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

func sessionDomainID(sessionID string) string {
	if dot := strings.LastIndex(sessionID, "."); dot > 0 {
		return sessionID[:dot]
	}
	return sessionID
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
		plan.domains = append(plan.domains, renameDomainPlan{
			oldDomain: sessionDomainID(oldID),
			newDomain: sessionDomainID(newID),
		})
		plan.sessions = append(plan.sessions, renameSessionPlan{
			oldID: oldID,
			newID: newID,
			cwd:   item.CWD,
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
		plan.domains = append(plan.domains, renameDomainPlan{
			oldDomain: renameDomainID(profile, oldFolders),
			newDomain: renameDomainID(profile, newFolders),
		})
		for _, child := range folder.Children {
			if child.IsFolder {
				walk(child, oldFolders, newFolders)
				continue
			}
			plan.sessions = append(plan.sessions, renameSessionPlan{
				oldID: fullRenameSessionID(profile, oldFolders, child.Name),
				newID: fullRenameSessionID(profile, newFolders, child.Name),
				cwd:   child.CWD,
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
		plan.domains = append(plan.domains, renameDomainPlan{
			oldDomain: sessionDomainID(oldID),
			newDomain: sessionDomainID(newID),
		})
		plan.sessions = append(plan.sessions, renameSessionPlan{oldID: oldID, newID: newID, cwd: item.CWD})
		return plan, nil
	}

	var walk func(folder *tree.Item, oldParent, newParentPath []string)
	walk = func(folder *tree.Item, oldParent, newParentPath []string) {
		oldFolders := appendRenamePath(oldParent, folder.Name)
		newFolders := appendRenamePath(newParentPath, folder.Name)
		plan.domains = append(plan.domains, renameDomainPlan{
			oldDomain: renameDomainID(profile, oldFolders),
			newDomain: renameDomainID(profile, newFolders),
		})
		for _, child := range folder.Children {
			if child.IsFolder {
				walk(child, oldFolders, newFolders)
				continue
			}
			plan.sessions = append(plan.sessions, renameSessionPlan{
				oldID: fullRenameSessionID(profile, oldFolders, child.Name),
				newID: fullRenameSessionID(profile, newFolders, child.Name),
				cwd:   child.CWD,
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

func (a *App) stopRenameSessions(plan *renamePlan) error {
	ids := make(map[string]struct{}, len(plan.sessions)+len(plan.familiars))
	for _, session := range plan.sessions {
		if session.oldID == session.newID {
			continue
		}
		ids[session.oldID] = struct{}{}
	}
	for _, familiar := range plan.familiars {
		ids[familiar.oldID] = struct{}{}
	}

	for id := range ids {
		if em, ok := a.emulatorCache[id]; ok {
			em.Stop()
			delete(a.emulatorCache, id)
		}
		if em, ok := a.familiarEmulatorCache[id]; ok {
			em.Stop()
			delete(a.familiarEmulatorCache, id)
		}
		if a.container != nil {
			if panel := a.container.Active(); panel != nil {
				if cp, ok := panel.(*ui.ChatPanel); ok {
					for _, session := range cp.Sessions() {
						if session.Em() != nil && session.Em().SessionID == id {
							session.Em().Stop()
						}
					}
				}
			}
		}
		if err := akjobs.KillSession(id); err != nil {
			return fmt.Errorf("stop jobs for %s: %w", id, err)
		}
		delete(a.activeSessions, id)
	}
	if a.tree != nil {
		a.tree.SetActiveSessions(a.activeSessions)
	}
	return nil
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

func rollbackRename(
	a *App,
	directoryMoves []renameDirectoryMove,
	jsonlMoves []renameJSONLMove,
	familiarFiles []renameFamiliarFileMove,
	assignmentMoves []renameAssignmentMove,
) {
	agentDir := a.renameAgentDir()
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
	if err := a.stopRenameSessions(plan); err != nil {
		return nil, err
	}

	var directoryMoves []renameDirectoryMove
	var jsonlMoves []renameJSONLMove
	var familiarFiles []renameFamiliarFileMove
	var assignmentMoves []renameAssignmentMove
	fail := func(err error) (func() error, error) {
		rollbackRename(a, directoryMoves, jsonlMoves, familiarFiles, assignmentMoves)
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

	for _, session := range plan.sessions {
		if session.oldID == session.newID {
			continue
		}
		for _, domain := range plan.domains {
			tasks, err := kanban.ReadAll(domain.newDomain, a.profile)
			if err != nil {
				return fail(fmt.Errorf("read Kanban %s: %w", domain.newDomain, err))
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
		rollbackRename(a, directoryMoves, jsonlMoves, familiarFiles, assignmentMoves)
		return nil
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
	return nil
}
