package apidserver

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// Read the exact retained captures from the original pinned native journal.
// Snapshots must belong to clonePostgresSnapshotRequest, including its original
// owner and idempotency key, and preserve the original request start time.
// Object CaptureIdentity is the canonical manifest
// hash, not a fresh listing at the same timestamp. Verify retained versions at
// the original placement through retainUntil. This method must never capture,
// list a replacement manifest, provision, copy, or release a source hold.
type cloneCheckpointAdoptionDriver interface {
	cloneCommonCheckpointDriver
	ObserveCapturedDatasets(context.Context, cloneCommonCheckpointPlan, cloneCommonCheckpointReceipt, time.Time) (state.ProjectEnvironmentCloneCheckpointAdoption, error)
}

// Private journals become usable by the existing snapshot restore and object
// copy workers together. Provider qualification, source-hold release and public
// full-clone admission remain separate gates.
func (s *server) adoptProjectEnvironmentCloneCheckpoint(ctx context.Context, lease state.ProjectEnvironmentCloneLease, driver cloneCheckpointAdoptionDriver, bucketLimit int) (state.ProjectEnvironmentCloneLease, error) {
	store, ok := s.store.(state.ProjectEnvironmentCloneCheckpointAdoptionStore)
	common, commonOK := s.store.(cloneCommonCheckpointStore)
	if !ok || !commonOK || driver == nil || bucketLimit < 1 || s.cloneWorkerAdmission == nil {
		return lease, errCloneCheckpointUnavailable
	}
	lease, receipt, err := s.prepareProjectEnvironmentCloneCommonCheckpoint(ctx, lease, driver)
	if err != nil {
		return lease, err
	}
	ctx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
	defer cancel()
	plan, _, _, err := s.cloneCommonCheckpointPlan(ctx, lease, common)
	if err != nil {
		return lease, err
	}
	if err := s.checkCloneCommonCheckpointHolds(ctx, lease, common, plan); err != nil {
		return lease, err
	}
	input := receipt
	input.Sources = append([]cloneCommonCheckpointSourceReceipt(nil), receipt.Sources...)
	adoption, err := driver.ObserveCapturedDatasets(ctx, cloneCommonCheckpointPlanCopy(plan), input, lease.ExpiresAt)
	if err != nil {
		return lease, err
	}
	if cloneCheckpointDigest(adoption.Receipt) != cloneCheckpointDigest(receipt) {
		return lease, state.ErrConflict
	}
	// Re-observe the original checkpoint after reading all subordinate captures.
	// Neither a stale receipt nor a root/hold change can authorize adoption.
	if err := s.checkCloneCommonCheckpointHolds(ctx, lease, common, plan); err != nil {
		return lease, err
	}
	current, err := driver.Observe(ctx, cloneCommonCheckpointPlanCopy(plan))
	if err != nil {
		return lease, err
	}
	if validateCloneCommonCheckpointReceipt(plan, current) != nil || cloneCheckpointDigest(current) != cloneCheckpointDigest(receipt) {
		return lease, state.ErrConflict
	}
	if err := s.checkCloneCommonCheckpointHolds(ctx, lease, common, plan); err != nil {
		return lease, err
	}
	updated, err := store.AdoptProjectEnvironmentCloneCheckpoint(ctx, lease, adoption, bucketLimit)
	if err != nil {
		return lease, err
	}
	lease.Operation = updated
	if err := s.checkCloneCommonCheckpointHolds(ctx, lease, common, plan); err != nil {
		return lease, err
	}
	return lease, nil
}
