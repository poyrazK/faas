package apidserver

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/state"
)

// Qualified drivers permanently fence the ORIGINAL restore owner request and
// any recorded isolation request before deleting only that owned native target.
// They authenticate unknown outcomes by the saved request and physical backend,
// never by resource-name discovery or generic not-found. Absence covers the
// entire target owner, including its runtime dataset. Source data and the original
// retained snapshot are unchanged. Observe is read-only and cannot retry, repair,
// restore, reopen credentials or manufacture terminal evidence from absence.
// There is no built-in qualified driver or public full-stage admission.
type cloneCheckpointPostgresTargetCompensationDriver interface {
	CompensateCheckpointPostgresTarget(context.Context, state.ProjectEnvironmentCloneCheckpointPostgresTargetCompensationPlan) error
	ObserveCheckpointPostgresTargetCompensation(context.Context, state.ProjectEnvironmentCloneCheckpointPostgresTargetCompensationPlan) (state.ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation, error)
}

func (s *server) compensateCheckpointPostgresTargets(ctx context.Context, l state.ProjectEnvironmentCloneLease, d cloneCheckpointPostgresTargetCompensationDriver) (state.ProjectEnvironmentCloneLease, error) {
	store, ok := s.store.(state.ProjectEnvironmentCloneCheckpointPostgresTargetCompensationStore)
	leases, leaseOK := s.store.(state.ProjectEnvironmentCloneWorkerLeaseStore)
	if !ok || !leaseOK || d == nil || s.cloneWorkerAdmission == nil {
		return l, errCloneCheckpointUnavailable
	}
	if l.Operation.Status != state.CloneOperationCompensating {
		return l, state.ErrConflict
	}
	if err := s.cloneWorkerAdmission(ctx); err != nil {
		return l, err
	}
	var err error
	l, err = renewCloneObjectWorkerLease(ctx, leases, l, state.CloneOperationCompensating)
	if err != nil {
		return l, err
	}
	plans, err := s.capturedProjectEnvironmentDatabasePlans(ctx, l.Operation)
	if err != nil {
		return l, err
	}
	for _, plan := range plans {
		if err = s.cloneWorkerAdmission(ctx); err != nil {
			return l, err
		}
		l, err = renewCloneObjectWorkerLease(ctx, leases, l, state.CloneOperationCompensating)
		if err != nil {
			return l, err
		}
		if err = s.compensateCheckpointPostgresTarget(ctx, l, d, store, plan.source.ID); err != nil {
			return l, err
		}
	}
	return l, nil
}

func (s *server) compensateCheckpointPostgresTarget(ctx context.Context, l state.ProjectEnvironmentCloneLease, d cloneCheckpointPostgresTargetCompensationDriver, store state.ProjectEnvironmentCloneCheckpointPostgresTargetCompensationStore, id string) error {
	ctx, cancel := context.WithDeadline(ctx, l.ExpiresAt)
	defer cancel()
	_, err := store.ReserveProjectEnvironmentCloneCheckpointPostgresTargetCompensation(ctx, l, id)
	// Only an absent original restore intent can be skipped. Missing proof or
	// provenance is a conflict in the authenticated store, not successful cleanup.
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = s.cloneWorkerAdmission(ctx); err != nil {
		return err
	}
	in, dispatch, err := store.ClaimProjectEnvironmentCloneCheckpointPostgresTargetCompensation(ctx, l, id)
	if err != nil {
		return err
	}
	if in.State == "compensated" {
		return store.FinishProjectEnvironmentCloneCheckpointPostgresTargetCompensation(ctx, l, id, in.Observation)
	}
	if dispatch {
		if err = s.cloneWorkerAdmission(ctx); err != nil {
			return err
		}
		if err = d.CompensateCheckpointPostgresTarget(ctx, cloneCheckpointPostgresTargetCompensationPlanCopy(in.Plan)); err != nil {
			return errCloneCheckpointUnavailable
		}
	}
	if err = s.cloneWorkerAdmission(ctx); err != nil {
		return err
	}
	observed, err := d.ObserveCheckpointPostgresTargetCompensation(ctx, cloneCheckpointPostgresTargetCompensationPlanCopy(in.Plan))
	if err != nil {
		return err
	}
	if err = s.cloneWorkerAdmission(ctx); err != nil {
		return err
	}
	return store.FinishProjectEnvironmentCloneCheckpointPostgresTargetCompensation(ctx, l, id, observed)
}

func cloneCheckpointPostgresTargetCompensationPlanCopy(p state.ProjectEnvironmentCloneCheckpointPostgresTargetCompensationPlan) state.ProjectEnvironmentCloneCheckpointPostgresTargetCompensationPlan {
	if p.Isolation != nil {
		copied := *p.Isolation
		p.Isolation = &copied
	}
	return p
}
