package main

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// uploadCommitRecoveryProblem recovers a retained outcome only after deriving
// its owner from the deployment. A missing session row no longer supplies the
// account boundary, and the outcome itself contains no account ID.
func (s *server) uploadCommitRecoveryProblem(ctx context.Context, accountID, uploadID string) *api.Problem {
	outcome, err := s.store.GetUploadCommitOutcome(ctx, uploadID)
	if errors.Is(err, state.ErrNotFound) {
		return api.ErrUploadSessionExpired(uploadID)
	}
	if err != nil {
		return api.ErrCapacity("could not load upload commit outcome")
	}
	deployment, err := s.store.DeploymentByID(ctx, outcome.DeploymentID)
	if errors.Is(err, state.ErrNotFound) {
		return api.ErrUploadSessionExpired(uploadID)
	}
	if err != nil {
		return api.ErrCapacity("could not load upload deployment")
	}
	app, err := s.store.AppByID(ctx, deployment.AppID)
	if errors.Is(err, state.ErrNotFound) {
		return api.ErrUploadSessionExpired(uploadID)
	}
	if err != nil {
		return api.ErrCapacity("could not load upload app")
	}
	if app.AccountID != accountID {
		return api.ErrUploadSessionNotFound(uploadID)
	}
	return api.ErrUploadSessionAlreadyCommitted(uploadID, outcome.DeploymentID)
}
