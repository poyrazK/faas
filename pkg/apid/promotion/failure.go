package promotion

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// FailGraph keeps health-owned status transitions in the health transaction.
func FailGraph(ctx context.Context, store state.Store, accountID string, p state.ProjectEnvironmentPromotion, message string) *api.Problem {
	var err error
	var h *api.ProjectPromotionHealth
	if health, ok := store.(state.ProjectPromotionHealthStore); ok {
		h, err = health.ProjectEnvironmentPromotionHealth(ctx, accountID, p.ID)
		if err == nil && h != nil {
			_, err = health.AdvanceProjectEnvironmentPromotionHealth(ctx, accountID, p.ID, state.PromotionHealthObservation{Generation: h.Generation, Action: "conflict"})
		}
	}
	if err == nil && h == nil {
		_, err = store.UpdateProjectEnvironmentPromotion(ctx, accountID, p.ID, "failed", message, nil)
	}
	if err != nil {
		return api.ErrCapacity("release graph activation failed and its promotion status could not be saved")
	}
	return api.NewProblem(http.StatusConflict, api.CodeProjectEnvironmentApprovalInvalid, "Target release graph changed", message)
}
