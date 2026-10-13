package apidserver

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/state"
)

// A qualified adapter permanently fences the ORIGINAL create and optional copy
// requests and every writer before deleting the exact private native target.
// Observe independently authenticates that closure, target/grant absence and
// unchanged source. Local withdrawal and a generic bucket-not-found result are
// insufficient. No built-in R2/OVH adapter is yet qualified for this protocol.
type cloneCheckpointObjectRetirementDriver interface {
	RetireCheckpointObjectTarget(context.Context, state.ProjectEnvironmentCloneCheckpointObjectRetirementPlan) error
	ObserveCheckpointObjectRetirement(context.Context, state.ProjectEnvironmentCloneCheckpointObjectRetirementPlan) (state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation, error)
}

func (s *server) retireCheckpointObjectTargets(ctx context.Context, l state.ProjectEnvironmentCloneLease, d cloneCheckpointObjectRetirementDriver) (state.ProjectEnvironmentCloneLease, error) {
	store, ok := s.store.(state.ProjectEnvironmentCloneCheckpointObjectRetirementStore)
	leases, leaseOK := s.store.(state.ProjectEnvironmentCloneWorkerLeaseStore)
	captures, captureOK := s.store.(state.ProjectEnvironmentCloneBindingCaptureStore)
	if !ok || !leaseOK || !captureOK || d == nil || s.cloneWorkerAdmission == nil {
		return l, errCloneCheckpointUnavailable
	}
	if l.Operation.Status != state.CloneOperationCompensating {
		return l, state.ErrConflict
	}
	// Withdrawal is mandatory even when there is no local publication. The store
	// reauthenticates it under the operation lock on reserve, claim and finish.
	var err error
	l, err = s.withdrawCheckpointObjects(ctx, l)
	if err != nil {
		return l, err
	}
	views, err := captures.ProjectEnvironmentCloneBindings(ctx, l.Operation.AccountID, l.Operation.ProjectID, l.Operation.ID)
	if err != nil {
		return l, err
	}
	for _, view := range views {
		for _, bucket := range view.Buckets {
			if err = s.cloneWorkerAdmission(ctx); err != nil {
				return l, err
			}
			l, err = renewCloneObjectWorkerLease(ctx, leases, l, state.CloneOperationCompensating)
			if err != nil {
				return l, err
			}
			if err = s.retireCheckpointObjectSource(ctx, l, d, store, bucket.ID); err != nil {
				return l, err
			}
		}
	}
	return l, nil
}
func (s *server) retireCheckpointObjectSource(ctx context.Context, l state.ProjectEnvironmentCloneLease, d cloneCheckpointObjectRetirementDriver, store state.ProjectEnvironmentCloneCheckpointObjectRetirementStore, id string) error {
	ctx, cancel := context.WithDeadline(ctx, l.ExpiresAt)
	defer cancel()
	_, err := store.ReserveProjectEnvironmentCloneCheckpointObjectRetirement(ctx, l, id)
	if errors.Is(err, state.ErrNotFound) {
		if err = s.cloneWorkerAdmission(ctx); err != nil {
			return err
		}
		return store.ReleaseProjectEnvironmentCloneCheckpointUnusedObjectReservation(ctx, l, id)
	}
	if err != nil {
		return err
	}
	if err = s.cloneWorkerAdmission(ctx); err != nil {
		return err
	}
	in, dispatch, err := store.ClaimProjectEnvironmentCloneCheckpointObjectRetirement(ctx, l, id)
	if err != nil {
		return err
	}
	if dispatch {
		if err = s.cloneWorkerAdmission(ctx); err != nil {
			return err
		}
		if err = d.RetireCheckpointObjectTarget(ctx, in.Plan); err != nil {
			return errCloneCheckpointUnavailable
		}
	}
	if err = s.cloneWorkerAdmission(ctx); err != nil {
		return err
	}
	observed, err := d.ObserveCheckpointObjectRetirement(ctx, in.Plan)
	if err != nil {
		return err
	}
	if err = s.cloneWorkerAdmission(ctx); err != nil {
		return err
	}
	return store.FinishProjectEnvironmentCloneCheckpointObjectRetirement(ctx, l, id, observed)
}
