package apidserver

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/state"
)

// Qualified drivers restore only the supplied original snapshot, authenticate
// the durable request's TargetOwnerID at the pinned backend, and observe that
// original native journal without retry, discovery or catalogue adoption.
// There is no built-in qualified checkpoint restore driver.
type cloneCheckpointPostgresRestoreDriver interface {
	cloneCheckpointReleaseDriver
	RestoreCheckpointPostgres(context.Context, cloneCommonCheckpointPlan, state.ProjectEnvironmentClonePostgresSnapshot, state.ProjectEnvironmentClonePostgresSnapshotRestore) error
	ObserveCheckpointPostgresRestore(context.Context, cloneCommonCheckpointPlan, state.ProjectEnvironmentClonePostgresSnapshot, state.ProjectEnvironmentClonePostgresSnapshotRestore) (cloneCheckpointPostgresRestoreObservation, error)
}

type cloneCheckpointPostgresRestoreObservation struct {
	OwnerID string
	Restore state.ProjectEnvironmentClonePostgresSnapshotRestoreObservation
}

// Successful forks remain private native owners. Catalogue adoption, auxiliary
// SQL isolation, copy verification and publication require later protocols.
func (s *server) restoreCheckpointPostgres(ctx context.Context, lease state.ProjectEnvironmentCloneLease, driver cloneCheckpointPostgresRestoreDriver, limit int) (state.ProjectEnvironmentCloneLease, bool, error) {
	restores, ok := s.store.(state.ProjectEnvironmentClonePostgresSnapshotRestoreStore)
	snapshots, snapshotOK := s.store.(state.ProjectEnvironmentClonePostgresSnapshotStore)
	leases, leaseOK := s.store.(state.ProjectEnvironmentCloneWorkerLeaseStore)
	releases, releaseOK := s.store.(state.ProjectEnvironmentCloneCheckpointReleaseStore)
	if !ok || !snapshotOK || !leaseOK || !releaseOK || driver == nil || s.cloneWorkerAdmission == nil || limit < 1 {
		return lease, false, errCloneCheckpointUnavailable
	}
	if err := s.cloneWorkerAdmission(ctx); err != nil {
		return lease, false, err
	}
	var err error
	lease, err = renewCloneObjectWorkerLease(ctx, leases, lease, state.CloneOperationCapturing)
	if err != nil {
		return lease, false, err
	}
	// Even a PostgreSQL-empty checkpoint must authenticate its original release.
	lease, err = s.authenticateCheckpointRestoreRetention(ctx, lease, driver, releases)
	if err != nil {
		return lease, false, err
	}
	plans, err := s.capturedProjectEnvironmentDatabasePlans(ctx, lease.Operation)
	if err != nil {
		return lease, false, err
	}
	complete := true
	for _, plan := range plans {
		lease, err = renewCloneObjectWorkerLease(ctx, leases, lease, state.CloneOperationCapturing)
		if err != nil {
			return lease, false, err
		}
		lease, err = s.authenticateCheckpointRestoreRetention(ctx, lease, driver, releases)
		if err != nil {
			return lease, false, err
		}
		ready, err := s.restoreCheckpointPostgresSource(ctx, lease, driver, restores, snapshots, releases, plan, limit)
		if err != nil {
			return lease, false, err
		}
		complete = complete && ready
	}
	return lease, complete, nil
}

func (s *server) authenticateCheckpointRestoreRetention(ctx context.Context, lease state.ProjectEnvironmentCloneLease, driver cloneCheckpointReleaseDriver, releases state.ProjectEnvironmentCloneCheckpointReleaseStore) (state.ProjectEnvironmentCloneLease, error) {
	ctx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
	defer cancel()
	observed, err := s.checkpointTargetRetention(ctx, lease, driver)
	if err != nil {
		return lease, err
	}
	if err := s.cloneWorkerAdmission(ctx); err != nil {
		return lease, err
	}
	updated, err := releases.FinishProjectEnvironmentCloneCheckpointRelease(ctx, lease, observed)
	if err != nil {
		return lease, err
	}
	lease.Operation = updated
	return lease, nil
}

func (s *server) restoreCheckpointPostgresSource(ctx context.Context, lease state.ProjectEnvironmentCloneLease, driver cloneCheckpointPostgresRestoreDriver, restores state.ProjectEnvironmentClonePostgresSnapshotRestoreStore, snapshots state.ProjectEnvironmentClonePostgresSnapshotStore, releases state.ProjectEnvironmentCloneCheckpointReleaseStore, plan capturedProjectEnvironmentDatabasePlan, limit int) (bool, error) {
	ctx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
	defer cancel()
	release, err := releases.ProjectEnvironmentCloneCheckpointReleaseForLease(ctx, lease)
	if err != nil {
		return false, err
	}
	snapshot, err := snapshots.ProjectEnvironmentClonePostgresSnapshotForLease(ctx, lease, plan.source.ID)
	if err != nil {
		return false, err
	}
	if err := validateClonePostgresSnapshotPlan(lease.Operation, plan, snapshot); err != nil {
		return false, err
	}
	receipt, err := restores.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(ctx, lease, plan.source.ID)
	if errors.Is(err, state.ErrNotFound) {
		receipt, err = restores.ReserveProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, plan.source.ID, limit)
	}
	if err != nil {
		return false, err
	}
	if receipt.State == "adopted" {
		// The lease reader authenticates the original unready catalogue owner.
		// SQL closure is independently rechecked by the isolation coordinator.
		return true, nil
	}
	if receipt.State == "deleting" || receipt.State == "deleted" {
		return false, state.ErrConflict
	}
	if err := s.cloneWorkerAdmission(ctx); err != nil {
		return false, err
	}
	receipt, dispatch, err := restores.ClaimProjectEnvironmentClonePostgresSnapshotRestoreRequest(ctx, lease, plan.source.ID)
	if err != nil {
		return false, err
	}
	if dispatch {
		if err := s.cloneWorkerAdmission(ctx); err != nil {
			return false, err
		}
		if err := driver.RestoreCheckpointPostgres(ctx, cloneCommonCheckpointPlanCopy(release.Plan), snapshot, receipt); err != nil {
			return false, err
		}
	}
	if err := s.cloneWorkerAdmission(ctx); err != nil {
		return false, err
	}
	observed, err := driver.ObserveCheckpointPostgresRestore(ctx, cloneCommonCheckpointPlanCopy(release.Plan), snapshot, receipt)
	if err != nil {
		return false, err
	}
	if observed.OwnerID != receipt.TargetOwnerID {
		return false, state.ErrConflict
	}
	if _, err := s.authenticateCheckpointRestoreRetention(ctx, lease, driver, releases); err != nil {
		return false, err
	}
	receipt, err = restores.RecordProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, plan.source.ID, observed.Restore)
	return receipt.State == "restored", err
}
