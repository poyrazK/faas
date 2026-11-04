package apidserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getServiceAbortReceipt(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	attempt := r.PathValue("attempt_id")
	if !api.ValidDeploymentAttemptID(attempt) {
		api.WriteProblem(w, api.ErrValidation("attempt_id must be 32 lowercase hexadecimal characters"))
		return
	}
	store, ok := s.store.(state.ServiceAbortReceiptStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("service abort receipts unavailable"))
		return
	}
	receipt, err := store.ServiceAbortReceipt(r.Context(), app.ID, attempt)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no retained service abort receipt; acceptance remains uncertain")
	} else if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read service abort receipt"))
	} else {
		writeJSON(w, http.StatusOK, receipt)
	}
}

// Preflight accepted attempts before mutable predecessor/review/approval checks.
// The store repeats this comparison under its normal app lock to close races.
func (s *server) serviceAbortAttempt(ctx context.Context, r *http.Request, app state.App, req api.RecoverRolloutRequest) (string, *api.Problem) {
	values := r.Header.Values(api.ServiceAbortAttemptHeader)
	if len(values) == 0 {
		return "", nil
	}
	if len(values) != 1 || !api.ValidDeploymentAttemptID(values[0]) || req.Action != "abort" || req.ExpectedReview == nil || !api.ValidServiceAbortScope(req.ExpectedReview.Scope, req.ExpectedReview.ProjectID, req.ExpectedReview.EnvironmentID) {
		return "", api.ErrValidation("Service-Abort-Attempt-ID requires an exact reviewed service abort with immutable scope identities")
	}
	candidate, e1 := uuid.Parse(req.DeploymentID)
	predecessor, e2 := uuid.Parse(req.ExpectedPredecessorDeploymentID)
	if e1 != nil || e2 != nil || candidate == predecessor {
		return "", api.ErrValidation("service abort attempts require distinct exact deployment UUIDs")
	}
	store, ok := s.store.(state.ServiceAbortAttemptStore)
	if !ok {
		return "", api.ErrCapacity("service abort attempts unavailable")
	}
	err := store.CheckServiceAbortAttempt(ctx, app.ID, values[0], api.ServiceAbortRequestHash(app.ID, req))
	if p := serviceAbortAttemptProblem(err); p != nil {
		return "", p
	}
	if !errors.Is(err, state.ErrNotFound) {
		return "", api.ErrCapacity("could not verify service abort attempt")
	}
	return values[0], nil
}

func serviceAbortAttemptProblem(err error) *api.Problem {
	if errors.Is(err, state.ErrServiceAbortAttemptAccepted) {
		return api.NewProblem(http.StatusConflict, "service_abort_attempt_accepted", "Service abort already accepted", "Read its immutable acceptance receipt and compare the native request ID separately with current handoff state; never replay this attempt.")
	}
	if errors.Is(err, state.ErrServiceAbortAttemptConflict) {
		return api.NewProblem(http.StatusConflict, "service_abort_attempt_conflict", "Service abort attempt conflict", "This attempt identity belongs to a different original reviewed service abort.")
	}
	return nil
}
