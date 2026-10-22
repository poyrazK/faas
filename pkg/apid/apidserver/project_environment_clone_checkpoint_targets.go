package apidserver

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state"
)

// Target preparation starts from the saved release journal, never a fresh source
// capture. Renew first so independent retention covers the actual work deadline.
// This preflight performs no native mutation and leaves all targets private.
func (s *server) checkpointObjectTargetsForLease(ctx context.Context, lease state.ProjectEnvironmentCloneLease, driver cloneCheckpointReleaseDriver) (state.ProjectEnvironmentCloneLease, []state.ObjectBucket, error) {
	store, ok := s.store.(cloneObjectWorkerStore)
	releases, releaseOK := s.store.(state.ProjectEnvironmentCloneCheckpointReleaseStore)
	if !ok || !releaseOK || driver == nil || s.cloneWorkerAdmission == nil {
		return lease, nil, errCloneCheckpointUnavailable
	}
	if err := s.cloneWorkerAdmission(ctx); err != nil {
		return lease, nil, err
	}
	lease, err := renewCloneObjectWorkerLease(ctx, store, lease, state.CloneOperationCapturing)
	if err != nil {
		return lease, nil, err
	}
	ctx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
	defer cancel()
	release, err := releases.ProjectEnvironmentCloneCheckpointReleaseForLease(ctx, lease)
	if err != nil {
		return lease, nil, err
	}
	if release.State != "released" {
		return lease, nil, state.ErrConflict
	}
	observed, err := driver.ObserveCapturedSourceRelease(ctx, cloneCommonCheckpointPlanCopy(release.Plan), cloneCheckpointReceiptCopy(release.Receipt), lease.ExpiresAt)
	if err != nil {
		return lease, nil, err
	}
	if err := s.cloneWorkerAdmission(ctx); err != nil {
		return lease, nil, err
	}
	// Released replay authenticates immutable journals and renewed retention,
	// without reopening, releasing, or inspecting subsequent source holds.
	updated, err := releases.FinishProjectEnvironmentCloneCheckpointRelease(ctx, lease, observed)
	if err != nil {
		return lease, nil, err
	}
	lease.Operation = updated
	plans, err := s.capturedProjectEnvironmentObjectPlans(ctx, updated)
	if err != nil {
		return lease, nil, err
	}
	indices, _, err := validateCapturedProjectEnvironmentObjectResources(plans, updated.Resources, true)
	if err != nil {
		return lease, nil, err
	}
	targets := make([]state.ObjectBucket, 0, len(plans))
	for _, plan := range plans {
		resource := updated.Resources[indices[plan.source.ID]]
		bucket, err := store.ProjectEnvironmentCloneObjectBucketForLease(ctx, lease, plan.appID, plan.source.ID, resource.TargetID)
		if err != nil {
			return lease, nil, err
		}
		manifest, err := store.ProjectEnvironmentCloneObjectManifest(ctx, updated.AccountID, updated.ProjectID, updated.ID, plan.source.ID)
		if err != nil {
			return lease, nil, err
		}
		if err := validateCapturedCloneObjectManifest(updated, resource, manifest); err != nil {
			return lease, nil, err
		}
		targets = append(targets, bucket)
	}
	return lease, targets, nil
}

// A qualified driver must bind native creation and its durable ownership journal
// to the exact request ID and plan digest. Observe independently authenticates
// that original journal and the pinned target's current private, empty state.
// It must never create, adopt an unowned bucket, retry, copy, or modify policy.
// No built-in provider implements this qualified protocol yet.
type cloneCheckpointObjectTargetDriver interface {
	cloneCheckpointReleaseDriver
	CreateCheckpointObjectTarget(context.Context, state.ProjectEnvironmentCloneCheckpointObjectTargetPlan) error
	ObserveCheckpointObjectTarget(context.Context, state.ProjectEnvironmentCloneCheckpointObjectTargetPlan) (state.ProjectEnvironmentCloneCheckpointObjectTargetObservation, error)
}

func (s *server) checkpointTargetRetention(ctx context.Context, lease state.ProjectEnvironmentCloneLease, driver cloneCheckpointReleaseDriver) (state.ProjectEnvironmentCloneCheckpointReleaseObservation, error) {
	store, ok := s.store.(state.ProjectEnvironmentCloneCheckpointReleaseStore)
	if !ok {
		return state.ProjectEnvironmentCloneCheckpointReleaseObservation{}, errCloneCheckpointUnavailable
	}
	release, err := store.ProjectEnvironmentCloneCheckpointReleaseForLease(ctx, lease)
	if err != nil {
		return state.ProjectEnvironmentCloneCheckpointReleaseObservation{}, err
	}
	if release.State != "released" {
		return state.ProjectEnvironmentCloneCheckpointReleaseObservation{}, state.ErrConflict
	}
	return driver.ObserveCapturedSourceRelease(ctx, cloneCommonCheckpointPlanCopy(release.Plan), cloneCheckpointReceiptCopy(release.Receipt), lease.ExpiresAt)
}

// A committing planned -> requested claim is the sole native creation authority.
// Requested/ready recovery observes the original target, even after worker
// handoff. Lost claim acknowledgements cannot authorize native IO on retry.
func (s *server) prepareCheckpointObjectTargets(ctx context.Context, lease state.ProjectEnvironmentCloneLease, driver cloneCheckpointObjectTargetDriver) (state.ProjectEnvironmentCloneLease, error) {
	store, ok := s.store.(state.ProjectEnvironmentCloneCheckpointObjectTargetStore)
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
		if err := s.prepareCheckpointObjectTarget(ctx, lease, driver, store, target); err != nil {
			return lease, err
		}
	}
	return lease, nil
}

func (s *server) prepareCheckpointObjectTarget(ctx context.Context, lease state.ProjectEnvironmentCloneLease, driver cloneCheckpointObjectTargetDriver, store state.ProjectEnvironmentCloneCheckpointObjectTargetStore, target state.ObjectBucket) error {
	ctx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
	defer cancel()
	creation, err := store.ReserveProjectEnvironmentCloneCheckpointObjectTarget(ctx, lease, target.AppID, target.EnvironmentCloneSourceBucketID, target.ID)
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
	creation, dispatch, err := store.ClaimProjectEnvironmentCloneCheckpointObjectTarget(ctx, lease, creation.Plan.SourceBucketID, retention)
	if err != nil {
		return err
	}
	if dispatch {
		if err := s.cloneWorkerAdmission(ctx); err != nil {
			return err
		}
		if err := driver.CreateCheckpointObjectTarget(ctx, creation.Plan); err != nil {
			return err
		}
	}
	if err := s.cloneWorkerAdmission(ctx); err != nil {
		return err
	}
	observed, err := driver.ObserveCheckpointObjectTarget(ctx, creation.Plan)
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
	_, err = store.FinishProjectEnvironmentCloneCheckpointObjectTarget(ctx, lease, creation.Plan.SourceBucketID, observed, retention)
	return err
}
