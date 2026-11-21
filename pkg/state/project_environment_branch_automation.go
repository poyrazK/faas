package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// A branch copies its parent's crons and event subscriptions paused, as a
// full clone does (ADR-20261031193635 amendment 2026-11-17): disabled, marked
// clone_paused when the parent ran them, so `resume-automation` turns them on
// for the branch alone. A plain clone that is not a branch copies neither.

// captureBranchAutomationDB reads the parent environment's crons and event
// subscriptions for every app the project runs there.
func captureBranchAutomationDB(ctx context.Context, db sqlc.DBTX, clone *ProjectEnvironmentClone) error {
	apps, err := projectEnvironmentAppIDsDB(ctx, db, clone.AccountID, clone.ProjectID, clone.SourceSlug)
	if err != nil {
		return err
	}
	clone.capturedCrons = make(map[string]projectCloneCrons, len(apps))
	clone.capturedEventSubscriptions = make(map[string]projectCloneEventSubscriptions, len(apps))
	for _, appID := range apps {
		crons, err := captureCloneCronsDB(ctx, db, appID, clone.SourceSlug)
		if err != nil {
			return err
		}
		subscriptions, err := captureCloneEventSubscriptionsDB(ctx, db, appID, clone.SourceSlug)
		if err != nil {
			return err
		}
		clone.capturedCrons[appID], clone.capturedEventSubscriptions[appID] = crons, subscriptions
	}
	return nil
}

func (m *MemStore) captureBranchAutomationLocked(clone *ProjectEnvironmentClone, apps map[string]string) error {
	clone.capturedCrons = make(map[string]projectCloneCrons, len(apps))
	clone.capturedEventSubscriptions = make(map[string]projectCloneEventSubscriptions, len(apps))
	for appID := range apps {
		crons, err := m.captureCloneCronsLocked(appID, clone.SourceSlug)
		if err != nil {
			return err
		}
		subscriptions, err := m.captureCloneEventSubscriptionsLocked(appID, clone.SourceSlug)
		if err != nil {
			return err
		}
		clone.capturedCrons[appID], clone.capturedEventSubscriptions[appID] = crons, subscriptions
	}
	return nil
}
