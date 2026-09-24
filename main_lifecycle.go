package main

import (
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"

	akjobs "github.com/HumanHorizon/automata/internal/ai-knowledge/jobs"
	"github.com/HumanHorizon/automata/internal/ui"
)

type stopSessionOptions struct {
	stopJobs           bool
	stopFamiliars      bool
	persistInactive    bool
	familiarSessionIDs []string
}

type stopSessionError struct {
	committed   bool
	persistence bool
	err         error
}

func (e *stopSessionError) Error() string {
	if e == nil || e.err == nil {
		return "runtime stop failed"
	}
	return e.err.Error()
}

func (e *stopSessionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func runtimeStopWasCommitted(err error) bool {
	var stopErr *stopSessionError
	return errors.As(err, &stopErr) && stopErr.committed
}

func runtimeStopPersistenceFailed(err error) bool {
	var stopErr *stopSessionError
	return errors.As(err, &stopErr) && stopErr.persistence
}

type preparedSessionJobs struct {
	sessionID string
	plan      *akjobs.KillPlan
	injected  bool
}

func (a *App) killSessionForProfile(sessionID string) error {
	if a.killSessionFn != nil {
		return a.killSessionFn(a.profile, sessionID)
	}
	return akjobs.KillSessionForProfile(a.profile, sessionID)
}

func (a *App) prepareSessionJob(sessionID string) (preparedSessionJobs, error) {
	if a.prepareJobSessionFn != nil {
		if err := a.prepareJobSessionFn(a.profile, sessionID); err != nil {
			return preparedSessionJobs{}, fmt.Errorf("prepare jobs for %s: %w", sessionID, err)
		}
	}
	if a.killSessionFn != nil {
		return preparedSessionJobs{sessionID: sessionID, injected: true}, nil
	}
	plan, err := akjobs.PrepareKillSessionForProfile(a.profile, sessionID)
	if err != nil {
		return preparedSessionJobs{}, fmt.Errorf("prepare jobs for %s: %w", sessionID, err)
	}
	return preparedSessionJobs{sessionID: sessionID, plan: plan}, nil
}

func (a *App) prepareSessionJobs(sessionIDs []string) ([]preparedSessionJobs, error) {
	prepared := make([]preparedSessionJobs, 0, len(sessionIDs))
	for _, sessionID := range sessionIDs {
		jobPlan, err := a.prepareSessionJob(sessionID)
		if err != nil {
			return nil, err
		}
		prepared = append(prepared, jobPlan)
	}
	return prepared, nil
}

func (a *App) executeSessionJob(jobPlan preparedSessionJobs, sessionID string) error {
	if jobPlan.injected {
		return a.killSessionForProfile(sessionID)
	}
	if jobPlan.plan == nil {
		return nil
	}
	return jobPlan.plan.ExecuteForProfile(a.profile, sessionID)
}

func (a *App) persistRuntimeActiveSessions() error {
	if a.tree == nil {
		return nil
	}
	if err := a.tree.SetActiveSessions(a.activeSessions); err != nil {
		// Runtime has already changed; keep the UI snapshot aligned with reality.
		a.tree.SetActiveSessionsInMemory(a.activeSessions)
		return err
	}
	return nil
}

func (a *App) stopSessionRuntime(sessionID string, opts stopSessionOptions) error {
	return a.stopSessionRuntimeIDs([]string{sessionID}, opts)
}

func (a *App) stopSessionRuntimeIDs(ownerIDs []string, opts stopSessionOptions) error {
	ids := a.expandRuntimeSessionIDs(ownerIDs, opts)
	var prepared []preparedSessionJobs
	if opts.stopJobs {
		var err error
		prepared, err = a.prepareSessionJobs(ids)
		if err != nil {
			return &stopSessionError{err: err}
		}
	}

	for _, sessionID := range ids {
		a.stopCachedEmulator(sessionID)
		a.closeSessionWatcher(sessionID)
		delete(a.runningSessions, sessionID)
		if opts.persistInactive {
			delete(a.activeSessions, sessionID)
		}
	}
	var failures []error
	persistenceFailed := false
	if opts.persistInactive {
		if err := a.persistRuntimeActiveSessions(); err != nil {
			persistenceFailed = true
			failures = append(failures, fmt.Errorf("persist inactive sessions: %w", err))
		}
	}

	if opts.stopJobs {
		for _, jobPlan := range prepared {
			if err := a.executeSessionJob(jobPlan, jobPlan.sessionID); err != nil {
				failures = append(failures, fmt.Errorf("stop jobs for %s: %w", jobPlan.sessionID, err))
			}
		}
	}
	if err := errors.Join(failures...); err != nil {
		return &stopSessionError{committed: true, persistence: persistenceFailed, err: err}
	}
	return nil
}

func (a *App) expandRuntimeSessionIDs(ownerIDs []string, opts stopSessionOptions) []string {
	seen := make(map[string]struct{}, len(ownerIDs)+len(opts.familiarSessionIDs))
	ids := make([]string, 0, len(ownerIDs)+len(opts.familiarSessionIDs))
	add := func(sessionID string) {
		if sessionID == "" {
			return
		}
		if _, exists := seen[sessionID]; exists {
			return
		}
		seen[sessionID] = struct{}{}
		ids = append(ids, sessionID)
	}
	for _, sessionID := range ownerIDs {
		add(sessionID)
	}
	if !opts.stopFamiliars {
		sort.Strings(ids)
		return ids
	}
	for _, sessionID := range opts.familiarSessionIDs {
		for _, ownerID := range ownerIDs {
			if strings.HasPrefix(sessionID, ownerID+"__") {
				add(sessionID)
				break
			}
		}
	}
	for _, ownerID := range ownerIDs {
		prefix := ownerID + "__"
		for sessionID := range a.emulatorCache {
			if strings.HasPrefix(sessionID, prefix) {
				add(sessionID)
			}
		}
		for sessionID := range a.familiarEmulatorCache {
			if strings.HasPrefix(sessionID, prefix) {
				add(sessionID)
			}
		}
		for sessionID := range a.activeSessions {
			if strings.HasPrefix(sessionID, prefix) {
				add(sessionID)
			}
		}
		for sessionID := range a.runningSessions {
			if strings.HasPrefix(sessionID, prefix) {
				add(sessionID)
			}
		}
	}
	if a.container == nil {
		sort.Strings(ids)
		return ids
	}
	panel := a.container.Active()
	cp, ok := panel.(*ui.ChatPanel)
	if !ok || cp == nil {
		return ids
	}
	for _, sessionID := range cp.FamiliarSessionIDs() {
		for _, ownerID := range ownerIDs {
			if strings.HasPrefix(sessionID, ownerID+"__") {
				add(sessionID)
				break
			}
		}
	}
	sort.Strings(ids)
	return ids
}

func (a *App) stopCachedEmulator(sessionID string) {
	if em, ok := a.emulatorCache[sessionID]; ok {
		if em != nil {
			em.Stop()
		}
		delete(a.emulatorCache, sessionID)
	}
	if em, ok := a.familiarEmulatorCache[sessionID]; ok {
		if em != nil {
			em.Stop()
		}
		delete(a.familiarEmulatorCache, sessionID)
	}
	if a.container == nil {
		return
	}
	panel := a.container.Active()
	cp, ok := panel.(*ui.ChatPanel)
	if !ok || cp == nil {
		return
	}
	for _, session := range cp.Sessions() {
		em := session.Em()
		if em != nil && em.SessionID == sessionID {
			em.Stop()
		}
	}
}

func (a *App) closeSessionWatcher(sessionID string) {
	if watcher, ok := a.sessionWatchers[sessionID]; ok {
		_ = watcher.Close()
		delete(a.sessionWatchers, sessionID)
	}
	delete(a.sessionWatchPending, sessionID)
}

func (a *App) stopAllRuntimeSessions(persistInactive bool) {
	ids := make([]string, 0, len(a.emulatorCache)+len(a.familiarEmulatorCache)+len(a.runningSessions)+len(a.sessionWatchers))
	seen := make(map[string]struct{})
	add := func(sessionID string) {
		if sessionID == "" {
			return
		}
		if _, ok := seen[sessionID]; ok {
			return
		}
		seen[sessionID] = struct{}{}
		ids = append(ids, sessionID)
	}
	for sessionID := range a.emulatorCache {
		add(sessionID)
	}
	for sessionID := range a.familiarEmulatorCache {
		add(sessionID)
	}
	for sessionID := range a.runningSessions {
		add(sessionID)
	}
	for sessionID := range a.sessionWatchers {
		add(sessionID)
	}
	if err := a.stopSessionRuntimeIDs(ids, stopSessionOptions{persistInactive: persistInactive}); err != nil {
		log.Printf("automata: stop all runtime sessions: %v", err)
	}
}
