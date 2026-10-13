package apidserver

import (
	"context"
	"github.com/onebox-faas/faas/pkg/state"
	"time"
)

// Qualified drivers probe only the original verified fork and independently pin
// its exact dataset and frozen specification. Qualification uses existing private
// operator-only read access; it may not provision, replace or restore a database,
// change customer data, create credentials or open inherited SQL writers/logins.
// Only a known durable claim dispatches this original qualification request.
// Observe authenticates its terminal receipt and current private runtime health
// through the renewed lease without retrying probes or repairing the runtime.
// There is no built-in qualified native runtime driver.
type cloneCheckpointPostgresRuntimeDriver interface {
	cloneCheckpointPostgresVerificationDriver
	QualifyCheckpointPostgresRuntime(context.Context, state.ProjectEnvironmentCloneCheckpointPostgresRuntimePlan) error
	ObserveCheckpointPostgresRuntime(context.Context, state.ProjectEnvironmentCloneCheckpointPostgresRuntimePlan, time.Time) (state.ProjectEnvironmentCloneCheckpointPostgresRuntimeObservation, error)
}

func (s *server) prepareCheckpointPostgresRuntime(ctx context.Context, l state.ProjectEnvironmentCloneLease, d cloneCheckpointPostgresRuntimeDriver) (state.ProjectEnvironmentCloneLease, error) {
	store, ok := s.store.(state.ProjectEnvironmentCloneCheckpointPostgresRuntimeStore)
	leases, leaseOK := s.store.(state.ProjectEnvironmentCloneWorkerLeaseStore)
	releases, releaseOK := s.store.(state.ProjectEnvironmentCloneCheckpointReleaseStore)
	if !ok || !leaseOK || !releaseOK || d == nil || s.cloneWorkerAdmission == nil {
		return l, errCloneCheckpointUnavailable
	}
	if err := s.cloneWorkerAdmission(ctx); err != nil {
		return l, err
	}
	var err error
	l, err = renewCloneObjectWorkerLease(ctx, leases, l, state.CloneOperationCapturing)
	if err != nil {
		return l, err
	}
	l, err = s.authenticateCheckpointRestoreRetention(ctx, l, d, releases)
	if err != nil {
		return l, err
	}
	plans, err := s.capturedProjectEnvironmentDatabasePlans(ctx, l.Operation)
	if err != nil {
		return l, err
	}
	for _, p := range plans {
		l, err = renewCloneObjectWorkerLease(ctx, leases, l, state.CloneOperationCapturing)
		if err != nil {
			return l, err
		}
		if err = s.prepareCheckpointPostgresRuntimeSource(ctx, l, d, store, p.source.ID); err != nil {
			return l, err
		}
	}
	return l, nil
}
func (s *server) prepareCheckpointPostgresRuntimeSource(ctx context.Context, l state.ProjectEnvironmentCloneLease, d cloneCheckpointPostgresRuntimeDriver, store state.ProjectEnvironmentCloneCheckpointPostgresRuntimeStore, id string) error {
	ctx, cancel := context.WithDeadline(ctx, l.ExpiresAt)
	defer cancel()
	in, err := store.ReserveProjectEnvironmentCloneCheckpointPostgresRuntime(ctx, l, id)
	if err != nil {
		return err
	}
	verified, err := d.ObserveCheckpointPostgresVerification(ctx, in.Plan.Verification)
	if err != nil {
		return err
	}
	closed, err := d.ObserveCheckpointPostgresIsolation(ctx, in.Plan.Verification.Isolation, l.ExpiresAt)
	if err != nil {
		return err
	}
	retention, err := s.checkpointTargetRetention(ctx, l, d)
	if err != nil {
		return err
	}
	if err = s.cloneWorkerAdmission(ctx); err != nil {
		return err
	}
	in, dispatch, err := store.ClaimProjectEnvironmentCloneCheckpointPostgresRuntime(ctx, l, id, verified, closed, retention)
	if err != nil {
		return err
	}
	if dispatch {
		if err = s.cloneWorkerAdmission(ctx); err != nil {
			return err
		}
		if err = d.QualifyCheckpointPostgresRuntime(ctx, in.Plan); err != nil {
			return err
		}
	}
	if err = s.cloneWorkerAdmission(ctx); err != nil {
		return err
	}
	observed, err := d.ObserveCheckpointPostgresRuntime(ctx, in.Plan, l.ExpiresAt)
	if err != nil {
		return err
	}
	verified, err = d.ObserveCheckpointPostgresVerification(ctx, in.Plan.Verification)
	if err != nil {
		return err
	}
	closed, err = d.ObserveCheckpointPostgresIsolation(ctx, in.Plan.Verification.Isolation, l.ExpiresAt)
	if err != nil {
		return err
	}
	retention, err = s.checkpointTargetRetention(ctx, l, d)
	if err != nil {
		return err
	}
	if err = s.cloneWorkerAdmission(ctx); err != nil {
		return err
	}
	_, err = store.FinishProjectEnvironmentCloneCheckpointPostgresRuntime(ctx, l, id, observed, verified, closed, retention)
	return err
}
