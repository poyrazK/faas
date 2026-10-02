package main

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) cleanupProjectEnvironmentClonePostgresSnapshotRestores(ctx context.Context, lease state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneLease, bool, error) {
	if lease.Operation.Status != state.CloneOperationCompensating {
		return lease, false, state.ErrConflict
	}
	plans, err := s.capturedProjectEnvironmentDatabasePlans(ctx, lease.Operation)
	if err != nil {
		return lease, false, err
	}
	if len(plans) == 0 {
		return lease, true, nil
	}
	forks, ok := s.store.(state.ProjectEnvironmentClonePostgresSnapshotRestoreCleanupStore)
	snapshots, snapshotOK := s.store.(state.ProjectEnvironmentClonePostgresSnapshotStore)
	leases, leaseOK := s.store.(state.ProjectEnvironmentCloneWorkerLeaseStore)
	if !ok || !snapshotOK || !leaseOK {
		return lease, false, managedpostgres.ErrUnavailable
	}
	complete := true
	for _, plan := range plans {
		renewed, err := leases.RenewProjectEnvironmentCloneLease(ctx, lease, projectEnvironmentCloneWorkerLeaseDuration)
		if err != nil {
			return lease, false, err
		}
		lease = renewed
		cleanupCtx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
		receipt, err := forks.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(cleanupCtx, lease, plan.source.ID)
		if errors.Is(err, state.ErrNotFound) {
			cancel()
			continue
		}
		if err != nil {
			cancel()
			return lease, false, err
		}
		if receipt.State == "deleted" {
			cancel()
			continue
		}
		receipt, err = forks.BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup(cleanupCtx, lease, plan.source.ID)
		if err != nil {
			cancel()
			return lease, false, err
		}
		if receipt.RequestStartedAt.IsZero() {
			_, err = forks.FinishProjectEnvironmentClonePostgresSnapshotRestoreCleanup(cleanupCtx, lease, plan.source.ID, state.ProjectEnvironmentClonePostgresSnapshotRestoreDeletion{Done: true})
			cancel()
			if err != nil {
				return lease, false, err
			}
			continue
		}
		if s.managedPostgres == nil {
			cancel()
			return lease, false, managedpostgres.ErrUnavailable
		}
		snapshot, err := snapshots.ProjectEnvironmentClonePostgresSnapshotForLease(cleanupCtx, lease, plan.source.ID)
		if err == nil {
			err = validateClonePostgresSnapshotPlan(lease.Operation, plan, snapshot)
		}
		if err != nil {
			cancel()
			return lease, false, err
		}
		definition := clonePostgresSnapshotDefinition(plan)
		request := managedpostgres.SnapshotRestoreRequest{ResourceID: receipt.TargetOwnerID, ProviderSnapshotID: snapshot.ProviderSnapshotID,
			ExpectedTargetResourceID: receipt.TargetProviderResourceID, Snapshot: clonePostgresSnapshotRequest(snapshot)}
		if receipt.TargetProviderResourceID == "" {
			actual, findErr := s.managedPostgres.FindSnapshotRestore(cleanupCtx, definition, request)
			if errors.Is(findErr, managedpostgres.ErrNotFound) {
				findErr = managedpostgres.ErrUnavailable
			}
			if findErr != nil {
				cancel()
				return lease, false, findErr
			}
			receipt, err = forks.RecordProjectEnvironmentClonePostgresSnapshotRestoreCleanupIdentity(cleanupCtx, lease, plan.source.ID,
				state.ProjectEnvironmentClonePostgresSnapshotRestoreObservation{ProviderSnapshotID: actual.ProviderSnapshotID, SourceDataResourceID: actual.SourceDataResourceID,
					TargetProviderResourceID: actual.ProviderResourceID, CapturePoint: actual.PointInTime, SnapshotCreatedAt: actual.SnapshotCreatedAt, TargetCreatedAt: actual.TargetCreatedAt, Restored: actual.Restored})
			if err != nil {
				cancel()
				return lease, false, err
			}
			request.ExpectedTargetResourceID = receipt.TargetProviderResourceID
		}
		ids, err := receipt.DeletionOperationIDs()
		if err != nil {
			cancel()
			return lease, false, err
		}
		deletion := managedpostgres.SnapshotRestoreDeletionRequest{Restore: request, SnapshotCreatedAt: snapshot.SnapshotCreatedAt, TargetCreatedAt: receipt.TargetCreatedAt, OperationIDs: ids}
		actual, err := s.managedPostgres.DeleteSnapshotRestore(cleanupCtx, definition, deletion)
		if err == nil {
			receipt, err = forks.RecordProjectEnvironmentClonePostgresSnapshotRestoreDeletionOperations(cleanupCtx, lease, plan.source.ID,
				state.ProjectEnvironmentClonePostgresSnapshotRestoreDeletion{TargetProviderResourceID: actual.ProviderResourceID, OperationIDs: actual.OperationIDs})
		}
		if err != nil {
			cancel()
			return lease, false, err
		}
		deletion.OperationIDs, err = receipt.DeletionOperationIDs()
		if err == nil {
			actual, err = s.managedPostgres.ObserveSnapshotRestoreDeletion(cleanupCtx, definition, deletion)
		}
		if err == nil && len(actual.OperationIDs) > 0 {
			// A lost DELETE reply may be recovered by a separate operation list.
			// Persist that exact set before the independent terminal read.
			receipt, err = forks.RecordProjectEnvironmentClonePostgresSnapshotRestoreDeletionOperations(cleanupCtx, lease, plan.source.ID,
				state.ProjectEnvironmentClonePostgresSnapshotRestoreDeletion{TargetProviderResourceID: actual.ProviderResourceID, OperationIDs: actual.OperationIDs})
			if err == nil && len(deletion.OperationIDs) == 0 {
				deletion.OperationIDs, err = receipt.DeletionOperationIDs()
				if err == nil {
					actual, err = s.managedPostgres.ObserveSnapshotRestoreDeletion(cleanupCtx, definition, deletion)
				}
			}
		}
		if err == nil && actual.Done {
			_, err = forks.FinishProjectEnvironmentClonePostgresSnapshotRestoreCleanup(cleanupCtx, lease, plan.source.ID,
				state.ProjectEnvironmentClonePostgresSnapshotRestoreDeletion{TargetProviderResourceID: actual.ProviderResourceID, OperationIDs: actual.OperationIDs, Done: true})
		} else if err == nil {
			complete = false
		}
		cancel()
		if err != nil {
			return lease, false, err
		}
	}
	return lease, complete, nil
}
