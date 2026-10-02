package main

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// Private preparation seam for the capturing owner. Prepared means the empty
// independent project has the frozen configuration, never that data is copied
// or that source writers can be released. The public capture gate stays closed
// until import, SQL isolation and dispatched-target cleanup are qualified.
func (s *server) prepareProjectEnvironmentClonePostgresCopyTargets(ctx context.Context, lease state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneLease, bool, error) {
	if lease.Operation.Status != state.CloneOperationCapturing {
		return lease, false, state.ErrConflict
	}
	plans, err := s.capturedProjectEnvironmentDatabasePlans(ctx, lease.Operation)
	if err != nil {
		return lease, false, err
	}
	if len(plans) == 0 {
		return lease, true, nil
	}
	if _, _, err := validateCapturedProjectEnvironmentDatabaseResources(plans, lease.Operation.Resources); err != nil {
		return lease, false, err
	}
	targets, targetOK := s.store.(state.ProjectEnvironmentClonePostgresCopyTargetStore)
	snapshots, snapshotOK := s.store.(state.ProjectEnvironmentClonePostgresSnapshotStore)
	captures, captureOK := s.store.(state.ProjectEnvironmentClonePostgresSnapshotRestoreStore)
	leases, leaseOK := s.store.(state.ProjectEnvironmentCloneWorkerLeaseStore)
	if !targetOK || !snapshotOK || !captureOK || !leaseOK || s.managedPostgres == nil {
		return lease, false, managedpostgres.ErrUnavailable
	}
	prepared := true
	for _, plan := range plans {
		renewed, err := leases.RenewProjectEnvironmentCloneLease(ctx, lease, projectEnvironmentCloneWorkerLeaseDuration)
		if err != nil {
			return lease, false, err
		}
		lease = renewed
		copyCtx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
		snapshot, err := snapshots.ProjectEnvironmentClonePostgresSnapshotForLease(copyCtx, lease, plan.source.ID)
		if err == nil {
			err = validateClonePostgresSnapshotPlan(lease.Operation, plan, snapshot)
		}
		if err == nil && snapshot.State != "retained" {
			err = state.ErrConflict
		}
		if err != nil {
			cancel()
			return lease, false, err
		}
		capture, err := captures.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(copyCtx, lease, plan.source.ID)
		if err == nil && (capture.State != "adopted" || capture.AdoptedDatabaseID != capture.TargetOwnerID || capture.AdoptedAt.IsZero()) {
			err = state.ErrConflict
		}
		if err != nil {
			cancel()
			return lease, false, err
		}
		definition := clonePostgresSnapshotDefinition(plan)
		target, err := targets.ProjectEnvironmentClonePostgresCopyTargetForLease(copyCtx, lease, plan.source.ID)
		if errors.Is(err, state.ErrNotFound) {
			var limit int
			limit, err = s.managedPostgres.AdmitSnapshotCopyTargetReservation(copyCtx, lease.Operation.AccountID, definition)
			if err == nil {
				target, _, err = targets.ReserveProjectEnvironmentClonePostgresCopyTarget(copyCtx, lease, plan.source.ID, limit)
			}
		}
		if err != nil {
			cancel()
			return lease, false, err
		}
		target, dispatch, err := targets.ClaimProjectEnvironmentClonePostgresCopyTargetRequest(copyCtx, lease, plan.source.ID)
		if err != nil {
			cancel()
			return lease, false, err
		}
		request := managedpostgres.SnapshotCopyTargetRequest{ResourceID: target.TargetDatabaseID,
			ExpectedProviderResourceID: target.ProviderResourceID, ExpectedCreatedAt: target.ProviderCreatedAt,
			SnapshotCreatedAt: snapshot.SnapshotCreatedAt, CaptureCreatedAt: capture.TargetCreatedAt,
			Capture: managedpostgres.SnapshotRestoreRequest{ResourceID: capture.TargetOwnerID, ProviderSnapshotID: snapshot.ProviderSnapshotID,
				ExpectedTargetResourceID: capture.TargetProviderResourceID, Snapshot: clonePostgresSnapshotRequest(snapshot)}}
		var actual managedpostgres.SnapshotCopyTargetObservation
		if dispatch {
			actual, err = s.managedPostgres.PrepareSnapshotCopyTarget(copyCtx, lease.Operation.AccountID, definition, request)
		} else {
			actual, err = s.managedPostgres.FindSnapshotCopyTarget(copyCtx, definition, request)
			if errors.Is(err, managedpostgres.ErrNotFound) {
				err = managedpostgres.ErrUnavailable
			}
		}
		if err == nil {
			target, err = targets.RecordProjectEnvironmentClonePostgresCopyTarget(copyCtx, lease, plan.source.ID,
				state.ProjectEnvironmentClonePostgresCopyTargetObservation{ProviderResourceID: actual.ProviderResourceID, CreatedAt: actual.CreatedAt, Prepared: actual.Prepared})
		}
		cancel()
		if err != nil {
			return lease, false, err
		}
		prepared = prepared && target.State == "prepared"
	}
	return lease, prepared, nil
}
