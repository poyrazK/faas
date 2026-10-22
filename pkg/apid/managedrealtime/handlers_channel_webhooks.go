package managedrealtime

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) loadRealtimeChannelWebhook(w http.ResponseWriter, r *http.Request, acct state.Account) (string, bool) {
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok || limits.WebhookPerApp == 0 {
		api.WriteProblem(w, api.ErrPlanWebhooksNotAllowed(acct.Plan))
		return "", false
	}
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return "", false
	}
	id := r.PathValue("id")
	hook, err := s.Host.Store().AppWebhookByID(r.Context(), id)
	if err != nil || hook.Scope != state.AppWebhookScopeApp || hook.AppID != app.ID || hook.AccountID != acct.ID {
		httpkit.NotFound(w, "webhook not found")
		return "", false
	}
	return id, true
}
func (s *Service) ManageRealtimeChannelWebhook(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, ok := s.loadRealtimeChannelWebhook(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.RealtimeChannelWebhookStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("channel webhook store unavailable"))
		return
	}
	var result state.RealtimeChannelWebhookFilter
	var err error
	switch r.Method {
	case http.MethodGet:
		result, err = store.ReadRealtimeChannelWebhookFilter(r.Context(), id)
	case http.MethodDelete:
		err = store.DeleteRealtimeChannelWebhookFilter(r.Context(), id)
	default:
		if httpkit.DecodeJSONSized(r, &result, 4096) != nil {
			api.WriteProblem(w, api.ErrAppWebhookInvalid("invalid channel webhook filter"))
			return
		}
		err = store.SetRealtimeChannelWebhookFilter(r.Context(), id, result)
	}
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrAppWebhookInvalid("invalid endpoint, channel or metadata filter"))
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "binding unavailable; select realtime.channel.published and an endpoint owned by this app")
	case err != nil:
		api.WriteProblem(w, api.ErrCapacity("channel webhook binding unavailable"))
	default:
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
		} else {
			httpkit.WriteJSON(w, http.StatusOK, result)
		}
		if r.Method != http.MethodGet {
			s.Host.EmitAudit(r.Context(), "realtime.channel_webhook_changed", &acct.ID, map[string]any{"webhook_id": id, "method": r.Method})
		}
	}
}
