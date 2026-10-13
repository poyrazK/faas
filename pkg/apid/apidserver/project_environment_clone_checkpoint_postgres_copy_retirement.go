package apidserver

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/state"
)

// Qualified adapters permanently close the ORIGINAL reader and target owners,
// including unknown requests, queued reader operations, writers and grants.
// Observe is read-only and cannot retry cleanup or infer closure from not-found.
// No built-in provider adapter or public full-stage admission is enabled.
type cloneCheckpointPostgresCopyRetirementDriver interface {
	RetireCheckpointPostgresCopy(context.Context, state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlan) error
	ObserveCheckpointPostgresCopyRetirement(context.Context, state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlan) (state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation, error)
}

type cloneCheckpointPostgresCopyRetirementStore interface {
	state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementStore
	ProjectEnvironmentClonePostgresCopyReaderForLease(context.Context, state.ProjectEnvironmentCloneLease, string) (state.ProjectEnvironmentClonePostgresCopyReader, error)
	ProjectEnvironmentClonePostgresCopyTargetForLease(context.Context, state.ProjectEnvironmentCloneLease, string) (state.ProjectEnvironmentClonePostgresCopyTarget, error)
}

func (s *server) retireCheckpointPostgresCopies(ctx context.Context, l state.ProjectEnvironmentCloneLease, d cloneCheckpointPostgresCopyRetirementDriver) (state.ProjectEnvironmentCloneLease, error) {
	store, ok := s.store.(cloneCheckpointPostgresCopyRetirementStore)
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
		if err = s.retireCheckpointPostgresCopy(ctx, l, d, store, plan.source.ID); err != nil {
			return l, err
		}
	}
	return l, nil
}

func reserveCheckpointPostgresCopyRetirement(ctx context.Context, l state.ProjectEnvironmentCloneLease, store cloneCheckpointPostgresCopyRetirementStore, id string) error {
	// Read the authenticated retirement first: legacy borrower readers cannot
	// interpret unknown outcomes settled by the new proof after parent cleanup.
	if _, err := store.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementForLease(ctx, l, id); !errors.Is(err, state.ErrNotFound) {
		return err
	}
	_, readerErr := store.ProjectEnvironmentClonePostgresCopyReaderForLease(ctx, l, id)
	if readerErr != nil && !errors.Is(readerErr, state.ErrNotFound) {
		return readerErr
	}
	_, targetErr := store.ProjectEnvironmentClonePostgresCopyTargetForLease(ctx, l, id)
	if targetErr != nil && !errors.Is(targetErr, state.ErrNotFound) {
		return targetErr
	}
	if errors.Is(readerErr, state.ErrNotFound) && errors.Is(targetErr, state.ErrNotFound) {
		return state.ErrNotFound
	}
	_, err := store.ReserveProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx, l, id)
	return err
}

func (s *server) retireCheckpointPostgresCopy(ctx context.Context, l state.ProjectEnvironmentCloneLease, d cloneCheckpointPostgresCopyRetirementDriver, store cloneCheckpointPostgresCopyRetirementStore, id string) error {
	ctx, cancel := context.WithDeadline(ctx, l.ExpiresAt)
	defer cancel()
	if err := reserveCheckpointPostgresCopyRetirement(ctx, l, store, id); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil
		}
		return err
	}
	if err := s.cloneWorkerAdmission(ctx); err != nil {
		return err
	}
	in, dispatch, err := store.ClaimProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx, l, id)
	if err != nil {
		return err
	}
	if in.State == "retired" {
		return store.FinishProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx, l, id, in.Observation)
	}
	if dispatch {
		if err = s.cloneWorkerAdmission(ctx); err != nil {
			return err
		}
		if err = d.RetireCheckpointPostgresCopy(ctx, cloneCheckpointPostgresCopyRetirementPlanCopy(in.Plan)); err != nil {
			return errCloneCheckpointUnavailable
		}
	}
	if err = s.cloneWorkerAdmission(ctx); err != nil {
		return err
	}
	o, err := d.ObserveCheckpointPostgresCopyRetirement(ctx, cloneCheckpointPostgresCopyRetirementPlanCopy(in.Plan))
	if err != nil {
		return err
	}
	if err = s.cloneWorkerAdmission(ctx); err != nil {
		return err
	}
	return store.FinishProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx, l, id, o)
}

func cloneCheckpointPostgresCopyRetirementPlanCopy(p state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlan) state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlan {
	p.Capture = cloneCheckpointPostgresTargetCompensationPlanCopy(p.Capture)
	if p.Reader != nil {
		reader := *p.Reader
		p.Reader = &reader
	}
	if p.Target != nil {
		target := *p.Target
		p.Target = &target
	}
	return p
}
