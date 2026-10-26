package apidserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) rollbackAttempt(ctx context.Context, r *http.Request, acct state.Account, app state.App, req api.RollbackRequest) (string, *api.Problem) {
	values := r.Header.Values(api.RollbackAttemptHeader)
	if len(values) == 0 {
		return "", nil
	}
	if len(values) != 1 || !api.ValidDeploymentAttemptID(values[0]) || req.TargetDeploymentID == nil || req.ExpectedCurrentDeploymentID == nil || req.AlertRuleID != nil || req.AcknowledgeReleases || app.Manifest.ExecutionMode != "" && app.Manifest.ExecutionMode != api.ExecutionModeRequest {
		return "", api.ErrValidation("Rollback-Attempt-ID requires an exact manual request-deployment rollback without release acknowledgement")
	}
	target, e1 := uuid.Parse(*req.TargetDeploymentID)
	current, e2 := uuid.Parse(*req.ExpectedCurrentDeploymentID)
	if e1 != nil || e2 != nil || target == current {
		return "", api.ErrValidation("rollback attempts require distinct exact deployment UUIDs")
	}
	store, ok := s.store.(state.CheckedRollbackStore)
	if !ok {
		return "", api.ErrCapacity("checked rollback store is unavailable")
	}
	prior, err := store.GetCheckedRollback(ctx, acct.ID, app.ID, uuid.MustParse(values[0]).String())
	if err == nil {
		original := api.RollbackRequest{TargetDeploymentID: &prior.TargetDeploymentID, ExpectedCurrentDeploymentID: &prior.CurrentDeploymentID, Reason: prior.Reason}
		if prior.AppID != app.ID || api.RollbackRequestHash(prior.AppID, original) != api.RollbackRequestHash(app.ID, req) || prior.Scope != api.DefaultEnvScope || prior.Service {
			return "", rollbackAttemptProblem(state.ErrRollbackAttemptConflict)
		}
		return "", rollbackAttemptProblem(state.ErrRollbackAttemptAccepted)
	}
	if !errors.Is(err, state.ErrNotFound) {
		return "", api.ErrCapacity("could not verify rollback attempt")
	}
	return values[0], nil
}
func rollbackAttemptProblem(err error) *api.Problem {
	if errors.Is(err, state.ErrRollbackAttemptAccepted) {
		return api.NewProblem(http.StatusConflict, "rollback_attempt_accepted", "Rollback already accepted", "Read the original operation and current traffic separately; never replay the accepted attempt.")
	}
	if errors.Is(err, state.ErrRollbackAttemptConflict) {
		return api.NewProblem(http.StatusConflict, "rollback_attempt_conflict", "Rollback attempt conflict", "The attempt identity belongs to a different original request.")
	}
	return nil
}
