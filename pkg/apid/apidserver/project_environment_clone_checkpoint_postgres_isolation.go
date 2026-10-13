package apidserver

import (
	"context"
	"github.com/onebox-faas/faas/pkg/state"
	"time"
)

// Qualified native drivers isolate only this operation-owned fork, without
// modifying its source or any shared authentication scope. They must close all
// inherited logins, scheduled/background writers and SQL write paths, preserving
// original data. Observe authenticates the original terminal request and current
// complete writer closure through retainUntil; it cannot apply/retry SQL, reopen
// grants or restore a replacement. No built-in provider implements this protocol.
type cloneCheckpointPostgresIsolationDriver interface {
	cloneCheckpointReleaseDriver
	IsolateCheckpointPostgres(context.Context, state.ProjectEnvironmentCloneCheckpointPostgresIsolationPlan) error
	ObserveCheckpointPostgresIsolation(context.Context, state.ProjectEnvironmentCloneCheckpointPostgresIsolationPlan, time.Time) (state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation, error)
}

func (s *server) isolateCheckpointPostgres(ctx context.Context, lease state.ProjectEnvironmentCloneLease, driver cloneCheckpointPostgresIsolationDriver) (state.ProjectEnvironmentCloneLease, error) {
	store, ok := s.store.(state.ProjectEnvironmentCloneCheckpointPostgresIsolationStore)
	leases, leaseOK := s.store.(state.ProjectEnvironmentCloneWorkerLeaseStore)
	releases, releaseOK := s.store.(state.ProjectEnvironmentCloneCheckpointReleaseStore)
	if !ok || !leaseOK || !releaseOK || driver == nil || s.cloneWorkerAdmission == nil {
		return lease, errCloneCheckpointUnavailable
	}
	if err := s.cloneWorkerAdmission(ctx); err != nil {
		return lease, err
	}
	var err error
	lease, err = renewCloneObjectWorkerLease(ctx, leases, lease, state.CloneOperationCapturing)
	if err != nil {
		return lease, err
	}
	lease, err = s.authenticateCheckpointRestoreRetention(ctx, lease, driver, releases)
	if err != nil {
		return lease, err
	}
	plans, err := s.capturedProjectEnvironmentDatabasePlans(ctx, lease.Operation)
	if err != nil {
		return lease, err
	}
	for _, plan := range plans {
		lease, err = renewCloneObjectWorkerLease(ctx, leases, lease, state.CloneOperationCapturing)
		if err != nil {
			return lease, err
		}
		if err := s.isolateCheckpointPostgresSource(ctx, lease, driver, store, plan.source.ID); err != nil {
			return lease, err
		}
	}
	return lease, nil
}
func (s *server) isolateCheckpointPostgresSource(ctx context.Context, lease state.ProjectEnvironmentCloneLease, driver cloneCheckpointPostgresIsolationDriver, store state.ProjectEnvironmentCloneCheckpointPostgresIsolationStore, id string) error {
	ctx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
	defer cancel()
	isolation, err := store.ReserveProjectEnvironmentCloneCheckpointPostgresIsolation(ctx, lease, id)
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
	isolation, dispatch, err := store.ClaimProjectEnvironmentCloneCheckpointPostgresIsolation(ctx, lease, id, retention)
	if err != nil {
		return err
	}
	if dispatch {
		if err := s.cloneWorkerAdmission(ctx); err != nil {
			return err
		}
		if err := driver.IsolateCheckpointPostgres(ctx, isolation.Plan); err != nil {
			return err
		}
	}
	if err := s.cloneWorkerAdmission(ctx); err != nil {
		return err
	}
	observed, err := driver.ObserveCheckpointPostgresIsolation(ctx, isolation.Plan, lease.ExpiresAt)
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
	_, err = store.FinishProjectEnvironmentCloneCheckpointPostgresIsolation(ctx, lease, id, observed, retention)
	return err
}
