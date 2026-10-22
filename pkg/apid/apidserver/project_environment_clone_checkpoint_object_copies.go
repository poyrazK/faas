package apidserver

import (
	"context"
	"github.com/onebox-faas/faas/pkg/state"
	"time"
)

// A qualified provider fences native writes to the original request and fixed
// WriteUntil, including delayed requests. Completion seals every writer forever.
// Recovery only observes that terminal receipt and fresh private-target closure;
// it cannot issue another copy, select live source versions or create grants.
// Dispatch atomically requires the originally created target to remain empty,
// private and free of other writers; unowned or preexisting contents fail closed.
type cloneCheckpointObjectCopyDriver interface {
	cloneCheckpointReleaseDriver
	CopyCheckpointObjects(context.Context, state.ProjectEnvironmentCloneCheckpointObjectCopyPlan) error
	ObserveCheckpointObjectCopy(context.Context, state.ProjectEnvironmentCloneCheckpointObjectCopyPlan, time.Time) (state.ProjectEnvironmentCloneCheckpointObjectCopyObservation, error)
}

func checkpointObjectCopyPlanCopy(p state.ProjectEnvironmentCloneCheckpointObjectCopyPlan) state.ProjectEnvironmentCloneCheckpointObjectCopyPlan {
	p.Inventory = checkpointObjectVerificationPlanCopy(p.Inventory)
	return p
}

// A committing planned -> requested claim is the sole copy authority.
// Requested/copied recovery observes the original target, even after worker
// handoff. Lost claim acknowledgements cannot authorize another copy on retry.
func (s *server) copyCheckpointObjects(ctx context.Context, lease state.ProjectEnvironmentCloneLease, driver cloneCheckpointObjectCopyDriver) (state.ProjectEnvironmentCloneLease, error) {
	store, ok := s.store.(state.ProjectEnvironmentCloneCheckpointObjectCopyStore)
	if !ok || driver == nil {
		return lease, errCloneCheckpointUnavailable
	}
	lease, targets, err := s.checkpointObjectTargetsForLease(ctx, lease, driver)
	if err != nil {
		return lease, err
	}
	for _, target := range targets {
		// Each target gets a fresh lease and independently verified capture horizon.
		lease, _, err = s.checkpointObjectTargetsForLease(ctx, lease, driver)
		if err != nil {
			return lease, err
		}
		if err := s.copyCheckpointObject(ctx, lease, driver, store, target); err != nil {
			return lease, err
		}
	}
	return lease, nil
}

func (s *server) copyCheckpointObject(ctx context.Context, lease state.ProjectEnvironmentCloneLease, driver cloneCheckpointObjectCopyDriver, store state.ProjectEnvironmentCloneCheckpointObjectCopyStore, target state.ObjectBucket) error {
	ctx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
	defer cancel()
	creation, err := store.ReserveProjectEnvironmentCloneCheckpointObjectCopy(ctx, lease, target.EnvironmentCloneSourceBucketID)
	if err != nil {
		return err
	}
	retention, err := s.checkpointTargetRetention(ctx, lease, driver)
	if err != nil {
		return err
	}
	if err := s.cloneWorkerAdmission(ctx); err != nil {
		return err
	}
	creation, dispatch, err := store.ClaimProjectEnvironmentCloneCheckpointObjectCopy(ctx, lease, creation.Plan.Inventory.Target.SourceBucketID, retention)
	if err != nil {
		return err
	}
	if dispatch {
		if err := s.cloneWorkerAdmission(ctx); err != nil {
			return err
		}
		if err := driver.CopyCheckpointObjects(ctx, checkpointObjectCopyPlanCopy(creation.Plan)); err != nil {
			return err
		}
	}
	if err := s.cloneWorkerAdmission(ctx); err != nil {
		return err
	}
	observed, err := driver.ObserveCheckpointObjectCopy(ctx, checkpointObjectCopyPlanCopy(creation.Plan), lease.ExpiresAt)
	if err != nil {
		return err
	}
	retention, err = s.checkpointTargetRetention(ctx, lease, driver)
	if err != nil {
		return err
	}
	if err := s.cloneWorkerAdmission(ctx); err != nil {
		return err
	}
	err = store.FinishProjectEnvironmentCloneCheckpointObjectCopy(ctx, lease, creation.Plan.Inventory.Target.SourceBucketID, observed, retention)
	return err
}
