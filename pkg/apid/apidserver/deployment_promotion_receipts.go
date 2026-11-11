package apidserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getDeploymentPromotionReceipt(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	attempt := r.PathValue("attempt_id")
	if !api.ValidDeploymentAttemptID(attempt) {
		api.WriteProblem(w, api.ErrValidation("attempt_id must be 32 lowercase hexadecimal characters"))
		return
	}
	store, ok := s.store.(state.DeploymentPromotionReceiptStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("promotion receipts unavailable"))
		return
	}
	receipt, err := store.DeploymentPromotionReceipt(r.Context(), app.ID, attempt)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no retained promotion receipt; acceptance remains uncertain")
	} else if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read promotion receipt"))
	} else {
		writeJSON(w, http.StatusOK, receipt)
	}
}

func (s *server) promotionAttempt(ctx context.Context, r *http.Request, app state.App, d state.Deployment, req api.UpdateDeploymentTrafficRequest) (string, *api.Problem) {
	values := r.Header.Values(api.DeploymentPromotionAttemptHeader)
	if len(values) == 0 {
		return "", nil
	}
	if len(values) != 1 || !api.ValidDeploymentAttemptID(values[0]) || req.TrafficPercent != 100 || req.ExpectedServingDeploymentID == nil {
		return "", api.ErrValidation("Deployment-Promotion-Attempt-ID requires 32 lowercase hexadecimal characters and a guarded 100% traffic request")
	}
	store, ok := s.store.(state.DeploymentPromotionReceiptStore)
	if !ok {
		return "", api.ErrCapacity("promotion receipts unavailable")
	}
	receipt, err := store.DeploymentPromotionReceipt(ctx, app.ID, values[0])
	if err == nil {
		hash, hashErr := api.DeploymentPromotionRequestHash(d.ID, req)
		if hashErr != nil || receipt.RequestHash != hash || receipt.DeploymentID != d.ID {
			return "", promotionAttemptProblem(state.ErrDeploymentPromotionAttemptConflict)
		}
		return "", promotionAttemptProblem(state.ErrDeploymentPromotionAttemptAccepted)
	}
	if !errors.Is(err, state.ErrNotFound) {
		return "", api.ErrCapacity("could not verify promotion receipt")
	}
	return values[0], nil
}
func promotionAttemptProblem(err error) *api.Problem {
	if errors.Is(err, state.ErrDeploymentPromotionAttemptAccepted) {
		return api.NewProblem(http.StatusConflict, "deployment_promotion_attempt_accepted", "Promotion already accepted", "The original promotion was accepted. Read its retained receipt and inspect current traffic separately; do not replay it.")
	}
	if errors.Is(err, state.ErrDeploymentPromotionAttemptConflict) {
		return api.NewProblem(http.StatusConflict, "deployment_promotion_attempt_conflict", "Promotion attempt conflict", "This attempt identity belongs to a different original promotion request.")
	}
	return nil
}
