package apidserver

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// Qualified adapters activate exactly the complete original allowlist. They may
// grant only its frozen access on its pinned datasets and enable only its fresh
// prepared identities. They never create identities, change passwords/endpoints,
// inherit source authority, enable original jobs/logins or publish app secrets.
// Independent observation authenticates the original intent and CURRENT exact
// SQL scope/health; original closed-roster/data equality is pre-activation proof.
// Retirement must permanently fence delayed activation for every owned identity.
type cloneCheckpointPgActivationDriver interface {
	cloneCheckpointPgConnectionDriver
	ActivateCheckpointPgCredentials(context.Context, state.ProjectEnvironmentCloneCheckpointPgActivationPlan) error
	ObserveCheckpointPgActivation(context.Context, state.ProjectEnvironmentCloneCheckpointPgActivationPlan, time.Time) (state.ProjectEnvironmentCloneCheckpointPgActivationObservation, error)
}

func (s *server) checkpointPgInactiveActivationProofs(ctx context.Context, l state.ProjectEnvironmentCloneLease, d cloneCheckpointPgActivationDriver) ([]state.ProjectEnvironmentCloneCheckpointPgConnectionProof, error) {
	credentials, ok := s.store.(state.ProjectEnvironmentCloneCheckpointPostgresCredentialStore)
	captures, captureOK := s.store.(state.ProjectEnvironmentCloneBindingCaptureStore)
	if !ok || !captureOK {
		return nil, errCloneCheckpointUnavailable
	}
	views, err := captures.ProjectEnvironmentCloneBindings(ctx, l.Operation.AccountID, l.Operation.ProjectID, l.Operation.ID)
	if err != nil {
		return nil, err
	}
	proofs := []state.ProjectEnvironmentCloneCheckpointPgConnectionProof{}
	for _, view := range views {
		for _, binding := range view.Postgres {
			if err = s.cloneWorkerAdmission(ctx); err != nil {
				return nil, err
			}
			credential, err := credentials.ReserveProjectEnvironmentCloneCheckpointPostgresCredential(ctx, l, binding.ID)
			if err != nil {
				return nil, err
			}
			proof, err := s.checkpointPgConnectionProof(ctx, l, d, credential)
			if err != nil {
				return nil, err
			}
			proofs = append(proofs, proof)
		}
	}
	return proofs, nil
}
func (s *server) activateCheckpointPgCredentials(ctx context.Context, l state.ProjectEnvironmentCloneLease, d cloneCheckpointPgActivationDriver) (state.ProjectEnvironmentCloneLease, error) {
	store, ok := s.store.(state.ProjectEnvironmentCloneCheckpointPgActivationStore)
	leases, leaseOK := s.store.(state.ProjectEnvironmentCloneWorkerLeaseStore)
	if !ok || !leaseOK || d == nil || s.cloneWorkerAdmission == nil {
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
	ctx, cancel := context.WithDeadline(ctx, l.ExpiresAt)
	defer cancel()
	in, err := store.ProjectEnvironmentCloneCheckpointPgActivationForLease(ctx, l)
	if errors.Is(err, state.ErrNotFound) {
		proofs, err := s.checkpointPgInactiveActivationProofs(ctx, l, d)
		if err != nil {
			return l, err
		}
		in, err = store.ReserveProjectEnvironmentCloneCheckpointPgActivation(ctx, l, proofs)
		if err != nil {
			return l, err
		}
	} else if err != nil {
		return l, err
	}
	var proofs []state.ProjectEnvironmentCloneCheckpointPgConnectionProof
	if in.State == "planned" {
		proofs, err = s.checkpointPgInactiveActivationProofs(ctx, l, d)
		if err != nil {
			return l, err
		}
	}
	if err = s.cloneWorkerAdmission(ctx); err != nil {
		return l, err
	}
	in, dispatch, err := store.ClaimProjectEnvironmentCloneCheckpointPgActivation(ctx, l, proofs)
	if err != nil {
		return l, err
	}
	if dispatch {
		if err = s.cloneWorkerAdmission(ctx); err != nil {
			return l, err
		}
		if err = d.ActivateCheckpointPgCredentials(ctx, in.Plan); err != nil {
			return l, errCloneCheckpointUnavailable
		}
	}
	if err = s.cloneWorkerAdmission(ctx); err != nil {
		return l, err
	}
	observed, err := d.ObserveCheckpointPgActivation(ctx, in.Plan, l.ExpiresAt)
	if err != nil {
		return l, err
	}
	retention, err := s.checkpointTargetRetention(ctx, l, d)
	if err != nil {
		return l, err
	}
	if err = s.cloneWorkerAdmission(ctx); err != nil {
		return l, err
	}
	return l, store.FinishProjectEnvironmentCloneCheckpointPgActivation(ctx, l, observed, retention)
}
