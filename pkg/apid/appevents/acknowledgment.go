package appevents

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

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
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "event_acknowledgment_stale", "Acknowledgment no longer valid", "the lease expired, the attempt was replaced, or callback inputs differ"))
	case errors.Is(err, context.DeadlineExceeded):
		api.WriteProblem(w, api.NewProblem(http.StatusGatewayTimeout, api.CodeInternal, "Acknowledgment request timed out", "retry with the same token and callback inputs"))
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

func parseAcknowledgmentHistoryQuery(r *http.Request) (api.EventAcknowledgmentHistoryQuery, error) {
	v := r.URL.Query()
	q := api.EventAcknowledgmentHistoryQuery{InvocationID: v.Get("invocation_id"), Kind: v.Get("kind"), Cursor: v.Get("cursor")}
	if raw := v.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > api.EventAcknowledgmentHistoryLimit {
			return q, state.ErrInvalidArgument
		}
		q.Limit = n
	}
	for key, target := range map[string]**time.Time{"from": &q.From, "until": &q.Until} {
		if raw := v.Get(key); raw != "" {
			t, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil {
				return q, state.ErrInvalidArgument
			}
			*target = &t
		}
	}
	return q, nil
}
func (s *Service) ListEventAcknowledgmentHistoryPage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventSubscriptionControlTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.EventAcknowledgmentHistoryPageStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("acknowledgment history page"))
		return
	}
	query, err := parseAcknowledgmentHistoryQuery(r)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("provide a valid history cursor, limit, outcome kind and RFC3339 time range"))
		return
	}
	out, err := store.ListEventAcknowledgmentHistoryPage(ctx, acct.ID, app.ID, r.PathValue("subscriptionID"), query)
	if writeAcknowledgmentHistoryPageError(w, err) {
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

func writeAcknowledgmentHistoryPageError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("provide a valid, unexpired history cursor with matching scope, filters and page size"))
		return true
	case errors.Is(err, context.DeadlineExceeded):
		api.WriteProblem(w, api.NewProblem(http.StatusGatewayTimeout, api.CodeInternal, "History read timed out", "retry the page with the same filters and cursor"))
		return true
	default:
		return writeAcknowledgmentError(w, err)
	}
}

func (s *Service) RetryEventAcknowledgment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.rejectEventAcknowledgment(w, r, acct, "retry")
}
func (s *Service) DeadLetterEventAcknowledgment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.rejectEventAcknowledgment(w, r, acct, "dead-letter")
}
func (s *Service) rejectEventAcknowledgment(w http.ResponseWriter, r *http.Request, acct state.Account, action string) {
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
	var req api.EventAcknowledgmentFailureRequest
	if httpkit.DecodeJSONSized(r, &req, api.EventAcknowledgmentFailureRequestMaxBytes) != nil {
		api.WriteProblem(w, api.ErrValidation("provide the current delivery token and a bounded non-sensitive reason code"))
		return
	}
	payload, err := json.Marshal(req.Reason)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid reason code"))
		return
	}
	out, err := store.EventAcknowledgment(ctx, acct.ID, app.ID, r.PathValue("invocationID"), req.Token, action, payload)
	if writeAcknowledgmentError(w, err) {
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}
