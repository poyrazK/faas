package apidserver

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// uploadCommitRecoveryProblem recovers a retained outcome only after deriving
// its app from the deployment and authorizing the caller for it, as the
// session's own routes do. A missing session row no longer supplies the
// account boundary, and the outcome itself contains no account ID.
func (s *server) uploadCommitRecoveryProblem(r *http.Request, acct state.Account, uploadID string) *api.Problem {
	ctx := r.Context()
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
	if _, prob := s.authorizeUploadApp(r, acct, app); prob != nil {
		return api.ErrUploadSessionNotFound(uploadID)
	}
	return api.ErrUploadSessionAlreadyCommitted(uploadID, outcome.DeploymentID)
}
