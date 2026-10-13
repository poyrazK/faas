package apidserver

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/state"
)

// Qualified adapters permanently fence the ORIGINAL preparation request before
// deleting the named identity and closing its sessions. Generic absence is not
// proof: delayed preparation must never recreate authority after retirement.
// A named activation request must also be permanently fenced for this identity,
// including unknown/partial activation. No repair, source mutation, extra SQL
// authority or secret inputs. Removing the owned activation delta restores closure.
type cloneCheckpointPostgresCredentialRetirementDriver interface {
	RetireCheckpointPostgresCredential(context.Context, state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementPlan) error
	ObserveCheckpointPostgresCredentialRetirement(context.Context, state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementPlan) (state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementObservation, error)
}

func (s *server) retireCheckpointPostgresCredentials(ctx context.Context, l state.ProjectEnvironmentCloneLease, d cloneCheckpointPostgresCredentialRetirementDriver) (state.ProjectEnvironmentCloneLease, error) {
	store, ok := s.store.(state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementStore)
	leases, leaseOK := s.store.(state.ProjectEnvironmentCloneWorkerLeaseStore)
	captures, captureOK := s.store.(state.ProjectEnvironmentCloneBindingCaptureStore)
	if !ok || !leaseOK || !captureOK || d == nil || s.cloneWorkerAdmission == nil {
		return l, errCloneCheckpointUnavailable
	}
	if l.Operation.Status != state.CloneOperationCompensating {
		return l, state.ErrConflict
	}
	if publications, ok := s.store.(state.ProjectEnvironmentCloneCheckpointPgPublicationStore); ok {
		var err error
		l, err = renewCloneObjectWorkerLease(ctx, leases, l, state.CloneOperationCompensating)
		if err != nil {
			return l, err
		}
		if err = s.cloneWorkerAdmission(ctx); err != nil {
			return l, err
		}
		if err = publications.WithdrawProjectEnvironmentCloneCheckpointPgBindings(ctx, l); err != nil {
			return l, err
		}
	}
	views, err := captures.ProjectEnvironmentCloneBindings(ctx, l.Operation.AccountID, l.Operation.ProjectID, l.Operation.ID)
	if err != nil {
		return l, err
	}
	for _, view := range views {
		for _, binding := range view.Postgres {
			if err = s.cloneWorkerAdmission(ctx); err != nil {
				return l, err
			}
			l, err = renewCloneObjectWorkerLease(ctx, leases, l, state.CloneOperationCompensating)
			if err != nil {
				return l, err
			}
			if err = s.retireCheckpointPostgresCredentialSource(ctx, l, d, store, binding.ID); err != nil {
				return l, err
			}
		}
	}
	return l, nil
}
func (s *server) retireCheckpointPostgresCredentialSource(ctx context.Context, l state.ProjectEnvironmentCloneLease, d cloneCheckpointPostgresCredentialRetirementDriver, store state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementStore, id string) error {
	ctx, cancel := context.WithDeadline(ctx, l.ExpiresAt)
	defer cancel()
	_, err := store.ReserveProjectEnvironmentCloneCheckpointPostgresCredentialRetirement(ctx, l, id)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = s.cloneWorkerAdmission(ctx); err != nil {
		return err
	}
	in, dispatch, err := store.ClaimProjectEnvironmentCloneCheckpointPostgresCredentialRetirement(ctx, l, id)
	if err != nil {
		return err
	}
	if dispatch {
		if err = s.cloneWorkerAdmission(ctx); err != nil {
			return err
		}
		if err = d.RetireCheckpointPostgresCredential(ctx, in.Plan); err != nil {
			return errCloneCheckpointUnavailable
		}
	}
	if err = s.cloneWorkerAdmission(ctx); err != nil {
		return err
	}
	observed, err := d.ObserveCheckpointPostgresCredentialRetirement(ctx, in.Plan)
	if err != nil {
		return err
	}
	if err = s.cloneWorkerAdmission(ctx); err != nil {
		return err
	}
	return store.FinishProjectEnvironmentCloneCheckpointPostgresCredentialRetirement(ctx, l, id, observed)
}
