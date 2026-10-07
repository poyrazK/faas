package main

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
