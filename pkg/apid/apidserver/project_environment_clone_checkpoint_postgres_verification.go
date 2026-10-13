package apidserver

import (
	"context"
	"github.com/onebox-faas/faas/pkg/state"
)

// Qualified comparators read the original retained snapshot and exact isolated
// fork through pre-existing operator-only read access. They must enforce the
// immutable per-side byte cap, worker deadline and their qualified shared memory,
// spool and account admission bounds; refuse unsupported objects; compare the
// complete canonical logical inventory, data, sequence values and large objects;
// and durably record the exact request. They may not recapture current source,
// restore a replacement, alter data or open SQL login/write paths. Recovery only
// observes that original request. No built-in provider implements this protocol.
type cloneCheckpointPostgresVerificationDriver interface {
	cloneCheckpointPostgresIsolationDriver
	VerifyCheckpointPostgres(context.Context, state.ProjectEnvironmentCloneCheckpointPostgresVerificationPlan) error
	ObserveCheckpointPostgresVerification(context.Context, state.ProjectEnvironmentCloneCheckpointPostgresVerificationPlan) (state.ProjectEnvironmentCloneCheckpointPostgresVerificationObservation, error)
}

func (s *server) verifyCheckpointPostgres(ctx context.Context, l state.ProjectEnvironmentCloneLease, d cloneCheckpointPostgresVerificationDriver) (state.ProjectEnvironmentCloneLease, error) {
	store, ok := s.store.(state.ProjectEnvironmentCloneCheckpointPostgresVerificationStore)
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
		if err = s.verifyCheckpointPostgresSource(ctx, l, d, store, p.source.ID); err != nil {
			return l, err
		}
	}
	return l, nil
}
func (s *server) verifyCheckpointPostgresSource(ctx context.Context, l state.ProjectEnvironmentCloneLease, d cloneCheckpointPostgresVerificationDriver, store state.ProjectEnvironmentCloneCheckpointPostgresVerificationStore, id string) error {
	ctx, cancel := context.WithDeadline(ctx, l.ExpiresAt)
	defer cancel()
	in, err := store.ReserveProjectEnvironmentCloneCheckpointPostgresVerification(ctx, l, id)
	if err != nil {
		return err
	}
	closed, err := d.ObserveCheckpointPostgresIsolation(ctx, in.Plan.Isolation, l.ExpiresAt)
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
	in, dispatch, err := store.ClaimProjectEnvironmentCloneCheckpointPostgresVerification(ctx, l, id, closed, retention)
	if err != nil {
		return err
	}
	if dispatch {
		if err = s.cloneWorkerAdmission(ctx); err != nil {
			return err
		}
		if err = d.VerifyCheckpointPostgres(ctx, in.Plan); err != nil {
			return err
		}
	}
	if err = s.cloneWorkerAdmission(ctx); err != nil {
		return err
	}
	observed, err := d.ObserveCheckpointPostgresVerification(ctx, in.Plan)
	if err != nil {
		return err
	}
	closed, err = d.ObserveCheckpointPostgresIsolation(ctx, in.Plan.Isolation, l.ExpiresAt)
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
	return store.FinishProjectEnvironmentCloneCheckpointPostgresVerification(ctx, l, id, observed, closed, retention)
}
