package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) eventSubscriptionRetryPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventSubscriptionControlTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.EventRoutingRetryPolicyStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event routing retry policies"))
		return
	}
	out, err := applyEventSubscriptionRetryPolicy(ctx, store, r, acct.ID, app.ID)
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("subscription ID must be a UUID; provide bounded attempts, duration, backoff and delivery age; ordered subscriptions cannot expire"))
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "no matching app subscription")
	case errors.Is(err, context.DeadlineExceeded):
		api.WriteProblem(w, api.NewProblem(http.StatusGatewayTimeout, api.CodeInternal, "Retry policy request timed out", "retry the request"))
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("event routing retry policies"))
	default:
		if r.Method != http.MethodGet {
			s.audit.Emit(ctx, "event.subscription.retry_policy.updated", &acct.ID, map[string]any{"app_id": app.ID, "subscription_id": out.SubscriptionID, "configured": out.Configured, "policy": out.Policy})
		}
		writeJSON(w, http.StatusOK, out)
	}
}
func applyEventSubscriptionRetryPolicy(ctx context.Context, store state.EventRoutingRetryPolicyStore, r *http.Request, account, app string) (api.EventRoutingRetryPolicyResponse, error) {
	sub := r.PathValue("subscriptionID")
	if r.Method == http.MethodGet {
		return store.GetEventSubscriptionRetryPolicy(ctx, account, app, sub)
	}
	var policy *api.EventRoutingRetryPolicy
	if r.Method == http.MethodPut {
		policy = &api.EventRoutingRetryPolicy{}
		if err := decodeJSONSized(r, policy, 4<<10); err != nil {
			return api.EventRoutingRetryPolicyResponse{}, state.ErrInvalidArgument
		}
		if err := policy.Validate(); err != nil {
			return api.EventRoutingRetryPolicyResponse{}, state.ErrInvalidArgument
		}
	}
	if _, err := store.SetEventSubscriptionRetryPolicy(ctx, account, app, sub, policy); err != nil {
		return api.EventRoutingRetryPolicyResponse{}, err
	}
	canonical, _ := uuid.Parse(sub)
	out := api.EventRoutingRetryPolicyResponse{SubscriptionID: canonical.String(), Configured: policy != nil, Policy: api.DefaultEventRoutingRetryPolicy()}
	if policy != nil {
		out.Policy = *policy
	}
	return out, nil
}
