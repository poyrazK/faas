package main

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// Independent projects retire before the native input and source snapshot.
// Lost creation/deletion replies retain the exact owner until remote proof.
func (s *server) cleanupProjectEnvironmentClonePostgresCopyTargets(ctx context.Context, lease state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneLease, bool, error) {
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
	targets, targetOK := s.store.(state.ProjectEnvironmentClonePostgresCopyTargetCleanupStore)
	snapshots, snapshotOK := s.store.(state.ProjectEnvironmentClonePostgresSnapshotStore)
	captures, captureOK := s.store.(state.ProjectEnvironmentClonePostgresSnapshotRestoreStore)
	leases, leaseOK := s.store.(state.ProjectEnvironmentCloneWorkerLeaseStore)
	if !targetOK || !snapshotOK || !captureOK || !leaseOK {
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
		target, err := targets.ProjectEnvironmentClonePostgresCopyTargetForLease(cleanupCtx, lease, plan.source.ID)
		if errors.Is(err, state.ErrNotFound) {
			cancel()
			continue
		}
		if err != nil {
			cancel()
			return lease, false, err
		}
		if target.State == "retired" {
			cancel()
			continue
		}
		if target.RequestStartedAt.IsZero() {
			_, err = targets.RetireUndispatchedProjectEnvironmentClonePostgresCopyTarget(cleanupCtx, lease, plan.source.ID)
			cancel()
			if err != nil {
				return lease, false, err
			}
			continue
		}
		target, err = targets.BeginProjectEnvironmentClonePostgresCopyTargetCleanup(cleanupCtx, lease, plan.source.ID)
		if err != nil {
			cancel()
			return lease, false, err
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
		capture, err := captures.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(cleanupCtx, lease, plan.source.ID)
		if err != nil {
			cancel()
			return lease, false, err
		}
		definition := clonePostgresSnapshotDefinition(plan)
		request := managedpostgres.SnapshotCopyTargetRequest{ResourceID: target.TargetDatabaseID, ExpectedProviderResourceID: target.ProviderResourceID, ExpectedCreatedAt: target.ProviderCreatedAt,
			SnapshotCreatedAt: snapshot.SnapshotCreatedAt, CaptureCreatedAt: capture.TargetCreatedAt, Capture: managedpostgres.SnapshotRestoreRequest{ResourceID: capture.TargetOwnerID, ProviderSnapshotID: snapshot.ProviderSnapshotID, ExpectedTargetResourceID: capture.TargetProviderResourceID, Snapshot: clonePostgresSnapshotRequest(snapshot)}}
		if target.ProviderResourceID == "" {
			actual, findErr := s.managedPostgres.DiscoverSnapshotCopyTargetForCleanup(cleanupCtx, definition, request)
			if errors.Is(findErr, managedpostgres.ErrNotFound) {
				findErr = managedpostgres.ErrUnavailable
			}
			if findErr != nil {
				cancel()
				return lease, false, findErr
			}
			target, err = targets.RecordProjectEnvironmentClonePostgresCopyTargetCleanupIdentity(cleanupCtx, lease, plan.source.ID, state.ProjectEnvironmentClonePostgresCopyTargetDeletion{ProviderResourceID: actual.ProviderResourceID, CreatedAt: actual.CreatedAt})
			if err != nil {
				cancel()
				return lease, false, err
			}
			request.ExpectedProviderResourceID, request.ExpectedCreatedAt = target.ProviderResourceID, target.ProviderCreatedAt
		}
		_, err = s.managedPostgres.DeleteSnapshotCopyTarget(cleanupCtx, definition, request)
		if err != nil {
			cancel()
			return lease, false, err
		}
		// The DELETE result supplies no terminal authority. Recovery always
		// uses a separate exact-identity observation before local retirement.
		actual, err := s.managedPostgres.ObserveSnapshotCopyTargetDeletion(cleanupCtx, definition, request)
		if err == nil && actual.Deleted {
			_, err = targets.FinishProjectEnvironmentClonePostgresCopyTargetCleanup(cleanupCtx, lease, plan.source.ID, state.ProjectEnvironmentClonePostgresCopyTargetDeletion{ProviderResourceID: actual.ProviderResourceID, CreatedAt: actual.CreatedAt, Done: true})
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
