package main

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// The capture owner calls this only after freezing the common point and
// retaining owned snapshots. Native storage completion neither releases source
// writers nor advances to copying. Public data capture remains separately gated.
func (s *server) restoreProjectEnvironmentClonePostgresSnapshots(ctx context.Context, lease state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneLease, bool, error) {
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
	restores, restoreOK := s.store.(state.ProjectEnvironmentClonePostgresSnapshotRestoreStore)
	snapshots, snapshotOK := s.store.(state.ProjectEnvironmentClonePostgresSnapshotStore)
	leases, leaseOK := s.store.(state.ProjectEnvironmentCloneWorkerLeaseStore)
	if !restoreOK || !snapshotOK || !leaseOK || s.managedPostgres == nil {
		return lease, false, managedpostgres.ErrUnavailable
	}
	complete := true
	for _, plan := range plans {
		renewed, err := leases.RenewProjectEnvironmentCloneLease(ctx, lease, projectEnvironmentCloneWorkerLeaseDuration)
		if err != nil {
			return lease, false, err
		}
		lease = renewed
		restoreCtx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
		snapshot, err := snapshots.ProjectEnvironmentClonePostgresSnapshotForLease(restoreCtx, lease, plan.source.ID)
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
		definition := clonePostgresSnapshotDefinition(plan)
		receipt, err := restores.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(restoreCtx, lease, plan.source.ID)
		if errors.Is(err, state.ErrNotFound) {
			var limit int
			limit, err = s.managedPostgres.AdmitRestoreReservation(restoreCtx, lease.Operation.AccountID, definition)
			if err == nil {
				receipt, err = restores.ReserveProjectEnvironmentClonePostgresSnapshotRestore(restoreCtx, lease, plan.source.ID, limit)
			}
		}
		if err != nil {
			cancel()
			return lease, false, err
		}
		receipt, dispatch, err := restores.ClaimProjectEnvironmentClonePostgresSnapshotRestoreRequest(restoreCtx, lease, plan.source.ID)
		if err != nil {
			cancel()
			return lease, false, err
		}
		request := managedpostgres.SnapshotRestoreRequest{ResourceID: receipt.TargetOwnerID, ProviderSnapshotID: snapshot.ProviderSnapshotID,
			ExpectedTargetResourceID: receipt.TargetProviderResourceID, Snapshot: clonePostgresSnapshotRequest(snapshot)}
		var actual managedpostgres.SnapshotRestoreObservation
		if dispatch {
			actual, err = s.managedPostgres.RestoreSnapshot(restoreCtx, lease.Operation.AccountID, definition, request)
		} else {
			actual, err = s.managedPostgres.FindSnapshotRestore(restoreCtx, definition, request)
			if errors.Is(err, managedpostgres.ErrNotFound) {
				err = managedpostgres.ErrUnavailable
			}
		}
		if err == nil {
			receipt, err = restores.RecordProjectEnvironmentClonePostgresSnapshotRestore(restoreCtx, lease, plan.source.ID,
				state.ProjectEnvironmentClonePostgresSnapshotRestoreObservation{ProviderSnapshotID: actual.ProviderSnapshotID,
					SourceDataResourceID: actual.SourceDataResourceID, TargetProviderResourceID: actual.ProviderResourceID,
					CapturePoint: actual.PointInTime, SnapshotCreatedAt: actual.SnapshotCreatedAt, TargetCreatedAt: actual.TargetCreatedAt, Restored: actual.Restored})
		}
		cancel()
		if err != nil {
			return lease, false, err
		}
		complete = complete && receipt.State == "restored"
	}
	return lease, complete, nil
}
