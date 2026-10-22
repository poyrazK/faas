package apidserver

import (
	"context"
	"github.com/onebox-faas/faas/pkg/state"
	"time"
)

// Verification has no copy, policy, or credential authority. A qualified driver
// compares the pinned source bodies and metadata with the complete private
// target inventory. Recovery observes its original immutable receipt and fresh
// writer closure; it cannot restart an unknown request.
type cloneCheckpointObjectVerificationDriver interface {
	cloneCheckpointReleaseDriver
	VerifyCheckpointObjects(context.Context, state.ProjectEnvironmentCloneCheckpointObjectVerificationPlan) error
	ObserveCheckpointObjectVerification(context.Context, state.ProjectEnvironmentCloneCheckpointObjectVerificationPlan, time.Time) (state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation, error)
}

func checkpointObjectVerificationPlanCopy(p state.ProjectEnvironmentCloneCheckpointObjectVerificationPlan) state.ProjectEnvironmentCloneCheckpointObjectVerificationPlan {
	p.Objects = append([]state.ProjectEnvironmentCloneObjectVersion{}, p.Objects...)
	return p
}

// A committing planned -> requested claim is the sole read-only comparison authority.
// Requested/verified recovery observes the original target, even after worker
// handoff. Lost claim acknowledgements cannot authorize another comparison on retry.
func (s *server) verifyCheckpointObjects(ctx context.Context, lease state.ProjectEnvironmentCloneLease, driver cloneCheckpointObjectVerificationDriver) (state.ProjectEnvironmentCloneLease, error) {
	store, ok := s.store.(state.ProjectEnvironmentCloneCheckpointObjectVerificationStore)
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
		if err := s.verifyCheckpointObject(ctx, lease, driver, store, target); err != nil {
			return lease, err
		}
	}
	return lease, nil
}

func (s *server) verifyCheckpointObject(ctx context.Context, lease state.ProjectEnvironmentCloneLease, driver cloneCheckpointObjectVerificationDriver, store state.ProjectEnvironmentCloneCheckpointObjectVerificationStore, target state.ObjectBucket) error {
	ctx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
	defer cancel()
	creation, err := store.ReserveProjectEnvironmentCloneCheckpointObjectVerification(ctx, lease, target.EnvironmentCloneSourceBucketID)
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
	creation, dispatch, err := store.ClaimProjectEnvironmentCloneCheckpointObjectVerification(ctx, lease, creation.Plan.Target.SourceBucketID, retention)
	if err != nil {
		return err
	}
	if dispatch {
		if err := s.cloneWorkerAdmission(ctx); err != nil {
			return err
		}
		if err := driver.VerifyCheckpointObjects(ctx, checkpointObjectVerificationPlanCopy(creation.Plan)); err != nil {
			return err
		}
	}
	if err := s.cloneWorkerAdmission(ctx); err != nil {
		return err
	}
	observed, err := driver.ObserveCheckpointObjectVerification(ctx, checkpointObjectVerificationPlanCopy(creation.Plan), lease.ExpiresAt)
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
	err = store.FinishProjectEnvironmentCloneCheckpointObjectVerification(ctx, lease, creation.Plan.Target.SourceBucketID, observed, retention)
	return err
}
