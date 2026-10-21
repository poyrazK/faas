package appevents

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) EventExecutionReconciliation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventSubscriptionControlTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.EventExecutionReconciliationStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("execution reconciliation unavailable"))
		return
	}
	request := r.Method == http.MethodPost
	out, err := store.EventExecutionReconciliation(ctx, acct.ID, app.ID, r.PathValue("subscriptionID"), request)
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("subscription ID must be a UUID"))
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "no matching subscription or retained delivery control")
	case errors.Is(err, context.DeadlineExceeded):
		api.WriteProblem(w, api.NewProblem(http.StatusGatewayTimeout, api.CodeInternal, "Execution reconciliation timed out", "retry the same subscription request"))
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("execution reconciliation"))
	default:
		status := http.StatusOK
		if request && out.Status == "waiting_for_lineage" {
			status = http.StatusAccepted
		}
		httpkit.WriteJSON(w, status, out)
	}
}
