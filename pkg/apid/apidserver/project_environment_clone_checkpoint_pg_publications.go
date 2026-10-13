package apidserver

import (
	"context"
	"github.com/onebox-faas/faas/pkg/state"
)

// Materialize all original PostgreSQL bindings atomically inside the private
// target. Current activation and retained capture are checked at final commit.
func (s *server) publishCheckpointPgBindings(ctx context.Context, l state.ProjectEnvironmentCloneLease, d cloneCheckpointPgActivationDriver) (state.ProjectEnvironmentCloneLease, error) {
	store, ok := s.store.(state.ProjectEnvironmentCloneCheckpointPgPublicationStore)
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
	inputs, err := store.ProjectEnvironmentCloneCheckpointPgPublicationInputs(ctx, l)
	if err != nil {
		return l, err
	}
	var secrets []state.AppSecret
	if inputs.Publication == nil {
		if len(inputs.Connections) != len(inputs.Activation.Plan.Bindings) || len(inputs.Credentials) != len(inputs.Activation.Plan.Bindings) {
			return l, state.ErrConflict
		}
		for i, member := range inputs.Activation.Plan.Bindings {
			if err = s.cloneWorkerAdmission(ctx); err != nil {
				return l, err
			}
			secret, err := sealCheckpointPgAppSecret(member, inputs.Connections[i], inputs.Credentials[i])
			if err != nil {
				return l, err
			}
			secrets = append(secrets, secret)
		}
	}
	if err = s.cloneWorkerAdmission(ctx); err != nil {
		return l, err
	}
	observed, err := d.ObserveCheckpointPgActivation(ctx, inputs.Activation.Plan, l.ExpiresAt)
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
	_, err = store.PublishProjectEnvironmentCloneCheckpointPgBindings(ctx, l, secrets, observed, retention)
	return l, err
}
