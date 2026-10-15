package appevents

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) EventAcknowledgmentPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventSubscriptionControlTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.EventAcknowledgmentStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event acknowledgments"))
		return
	}
	var seconds *int
	if r.Method == http.MethodPut {
		var req api.EventAcknowledgmentPolicyRequest
		if httpkit.DecodeJSONSized(r, &req, 4<<10) != nil || req.LeaseSeconds == nil {
			api.WriteProblem(w, api.ErrValidation("lease_seconds must be explicit: zero disables deferred acknowledgment, 1–300 enables it"))
			return
		}
		seconds = req.LeaseSeconds
	}
	out, err := store.EventAcknowledgmentPolicy(ctx, acct.ID, app.ID, r.PathValue("subscriptionID"), seconds)
	if writeAcknowledgmentError(w, err) {
		return
	}
	if seconds != nil {
		s.Host.EmitAudit(ctx, "event.subscription.acknowledgment_policy.updated", &acct.ID, map[string]any{"app_id": app.ID, "subscription_id": out.SubscriptionID, "lease_seconds": out.LeaseSeconds})
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}
func (s *Service) EventAcknowledgment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventSubscriptionControlTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.EventAcknowledgmentStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event acknowledgments"))
		return
	}
	action := "get"
	var req api.EventAcknowledgmentRequest
	if r.Method == http.MethodPost {
		action = r.PathValue("ackAction")
		if action != "renew" && action != "complete" {
			httpkit.NotFound(w, "unknown acknowledgment action")
			return
		}
		if httpkit.DecodeJSONSized(r, &req, api.EventAcknowledgmentResultMaxBytes+4096) != nil || (action == "renew" && len(req.Result) > 0) {
			api.WriteProblem(w, api.ErrValidation("provide the delivery token and an optional JSON result for completion only"))
			return
		}
	}
	out, err := store.EventAcknowledgment(ctx, acct.ID, app.ID, r.PathValue("invocationID"), req.Token, action, req.Result)
	if writeAcknowledgmentError(w, err) {
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}
func writeAcknowledgmentError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("provide valid scope IDs, a bounded acknowledgment policy and a canonical delivery token"))
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "no retained acknowledgment belongs to this application")
	case errors.Is(err, state.ErrEventAcknowledgmentStale):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "event_acknowledgment_stale", "Acknowledgment no longer valid", "the lease expired, the attempt was replaced, or completion inputs differ"))
	case errors.Is(err, context.DeadlineExceeded):
		api.WriteProblem(w, api.NewProblem(http.StatusGatewayTimeout, api.CodeInternal, "Acknowledgment request timed out", "retry with the same token and completion result"))
	default:
		api.WriteProblem(w, api.ErrInternal("event acknowledgment"))
	}
	return true
}

func (s *Service) RenewEventAcknowledgment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	r.SetPathValue("ackAction", "renew")
	s.EventAcknowledgment(w, r, acct)
}
func (s *Service) CompleteEventAcknowledgment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	r.SetPathValue("ackAction", "complete")
	s.EventAcknowledgment(w, r, acct)
}

func (s *Service) ListEventAcknowledgmentHistory(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventSubscriptionControlTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.EventAcknowledgmentHistoryStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("acknowledgment history"))
		return
	}
	out, err := store.ListEventAcknowledgmentHistory(ctx, acct.ID, app.ID, r.PathValue("subscriptionID"), r.URL.Query().Get("invocation_id"))
	if writeAcknowledgmentError(w, err) {
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}
