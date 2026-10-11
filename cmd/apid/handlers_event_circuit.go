package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) eventSubscriptionCircuit(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventSubscriptionControlTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.EventCircuitBreakerStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event circuit breakers"))
		return
	}
	out, err := applyEventCircuit(ctx, store, r, acct.ID, app.ID)
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("subscription ID must be a UUID; supply a bounded circuit breaker policy"))
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "no matching subscription or enabled circuit breaker")
	case errors.Is(err, context.DeadlineExceeded):
		api.WriteProblem(w, api.NewProblem(http.StatusGatewayTimeout, api.CodeInternal, "Circuit breaker request timed out", "retry the request"))
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("event circuit breakers"))
	default:
		if r.Method != http.MethodGet {
			s.audit.Emit(ctx, "event.subscription.circuit.updated", &acct.ID, map[string]any{"app_id": app.ID, "subscription_id": out.SubscriptionID, "method": r.Method, "enabled": out.Enabled, "state": out.State, "reason": out.Reason, "policy": out.Policy})
		}
		writeJSON(w, http.StatusOK, out)
	}
}
func applyEventCircuit(ctx context.Context, store state.EventCircuitBreakerStore, r *http.Request, account, app string) (api.EventCircuitBreakerResponse, error) {
	sub := r.PathValue("subscriptionID")
	switch r.Method {
	case http.MethodGet:
		return store.GetEventCircuitBreaker(ctx, account, app, sub)
	case http.MethodPost:
		return store.ResetEventCircuitBreaker(ctx, account, app, sub)
	case http.MethodDelete:
		return store.SetEventCircuitBreaker(ctx, account, app, sub, nil)
	default:
		p := api.DefaultEventCircuitBreakerPolicy()
		if err := decodeJSONSized(r, &p, 4<<10); err != nil {
			return api.EventCircuitBreakerResponse{}, state.ErrInvalidArgument
		}
		if err := p.Validate(); err != nil {
			return api.EventCircuitBreakerResponse{}, state.ErrInvalidArgument
		}
		return store.SetEventCircuitBreaker(ctx, account, app, sub, &p)
	}
}
