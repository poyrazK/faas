// adr:1352
package bindings

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func QueueCanaryProblem(err error) *api.Problem {
	var capacity *state.QueueCanaryCapacityError
	if errors.As(err, &capacity) {
		return api.ErrPlanQueueDepth(capacity.Limit, capacity.Observed)
	}
	switch {
	case err == nil:
		return nil
	case errors.Is(err, state.ErrNotFound):
		return api.NewProblem(http.StatusNotFound, "not_found", "Not found", "Queue canary or eligible deployment/consumer not found.")
	case errors.Is(err, state.ErrInvalidArgument):
		return api.ErrValidation("Supply an exact deployment, binding, publication consent and 1–600 second TTL. The deployment must declare this binding in GREGALE_QUEUE_CANARY_BINDINGS as a JSON array in override_env or its frozen reviewed variables.")
	case errors.Is(err, state.ErrConflict):
		return api.NewProblem(http.StatusConflict, "queue_canary_changed", "Queue canary blocked", "Another canary is active or the consumer changed. Cancel the previous canary and verify the selected push consumer.")
	default:
		return api.ErrCapacity("queue canary is unavailable")
	}
}
func ApplyQueueCanaryInventory(ctx context.Context, store state.Store, account, app string, inventory *api.AppBindingInventory) {
	canaries, ok := store.(state.QueueBindingCanaryStore)
	if !ok || inventory.VerificationDeploymentID == "" {
		return
	}
	dep, err := store.DeploymentByID(ctx, inventory.VerificationDeploymentID)
	if err != nil {
		return
	}
	for i := range inventory.Bindings {
		item := &inventory.Bindings[i]
		if item.Type != api.BindingTypeQueue || !api.QueueBindingInScope(*item, inventory.VerificationScope) {
			continue
		}
		item.QueueCanarySupported = false
		if eligibility, ok := store.(state.QueueBindingCanaryEligibilityStore); ok {
			var err error
			item.QueueCanarySupported, item.QueueCanaryReason, err = eligibility.QueueBindingCanaryEligibility(ctx, account, app, dep.ID, item.Binding)
			if err != nil {
				inventory.Complete = false
				inventory.Issues = append(inventory.Issues, api.BindingInventoryIssue{Type: api.BindingTypeQueue, Code: "queue_canary_unavailable", Severity: "error", Message: "Queue canary eligibility could not be read."})
			}
		} else {
			item.QueueCanaryReason = "queue_canary_context_unavailable"
		}
		r, err := canaries.LatestQueueBindingCanary(ctx, account, app, inventory.VerificationDeploymentID, item.Binding)
		if errors.Is(err, state.ErrNotFound) {
			continue
		}
		if err != nil {
			inventory.Issues = append(inventory.Issues, api.BindingInventoryIssue{Type: api.BindingTypeQueue, Code: "queue_canary_unavailable", Severity: "error", Message: "Queue completion evidence could not be read."})
			continue
		}
		item.QueueCanary = &r
	}
}
