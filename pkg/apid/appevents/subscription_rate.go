package appevents

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) PreviewEventSubscriptionRateLimit(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.subscriptionRateCommand(w, r, acct, "preview")
}

func (s *Service) SetEventSubscriptionRateLimit(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.subscriptionRateCommand(w, r, acct, "set")
}
func (s *Service) GetEventSubscriptionRateCommand(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.subscriptionRateCommand(w, r, acct, "get")
}
func (s *Service) ListEventSubscriptionRateCommands(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.subscriptionRateCommand(w, r, acct, "list")
}
func (s *Service) subscriptionRateCommand(w http.ResponseWriter, r *http.Request, acct state.Account, action string) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventSubscriptionControlTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.EventSubscriptionRateStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("subscription rate controls"))
		return
	}
	out, err := applySubscriptionRateCommand(ctx, store, r, acct.ID, app.ID, action)
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("invalid subscription, command UUID, revision, interval, burst or history cursor"))
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "no accessible subscription rate command evidence")
	case errors.Is(err, state.ErrConflict), errors.Is(err, state.ErrEventSubscriptionRateCommandConflict):
		api.WriteProblem(w, api.NewProblem(409, "event_subscription_rate_conflict", "Rate command conflict", "refresh configuration or replay the original command inputs"))
	case errors.Is(err, state.ErrEventSubscriptionRateCommandLimit):
		api.WriteProblem(w, api.NewProblem(429, "event_subscription_rate_command_limit", "Rate command history full", "known repeats remain available; expired receipts free slots").WithLimit(api.EventSubscriptionRateCommandMaxReceipts, api.EventSubscriptionRateCommandMaxReceipts).WithDocs("https://gregale.dev/docs/event-driven"))
	case errors.Is(err, context.DeadlineExceeded):
		detail := "look up or replay the same command UUID after an uncertain mutation"
		if action == "preview" {
			detail = "retry the read-only preview with a fresh observation"
		}
		api.WriteProblem(w, api.NewProblem(504, api.CodeInternal, "Rate control timed out", detail))
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("subscription rate controls"))
	default:
		httpkit.WriteJSON(w, http.StatusOK, out)
	}
}
func applySubscriptionRateCommand(ctx context.Context, store state.EventSubscriptionRateStore, r *http.Request, account, app, action string) (any, error) {
	sub := r.PathValue("subscriptionID")
	switch action {
	case "preview":
		preview, ok := store.(state.EventSubscriptionRatePreviewStore)
		if !ok {
			return nil, errors.New("subscription rate preview unavailable")
		}
		var req api.EventSubscriptionRatePreviewRequest
		if httpkit.DecodeJSONSized(r, &req, api.EventSubscriptionRateRequestMaxBytes) != nil || req.Validate() != nil {
			return nil, state.ErrInvalidArgument
		}
		return preview.PreviewEventSubscriptionRateLimit(ctx, account, app, sub, req)

	case "set":
		var req api.EventSubscriptionRateRequest
		if httpkit.DecodeJSONSized(r, &req, api.EventSubscriptionRateRequestMaxBytes) != nil || req.Validate() != nil {
			return nil, state.ErrInvalidArgument
		}
		return store.SetEventSubscriptionRateLimit(ctx, account, app, sub, req)
	case "get":
		return store.GetEventSubscriptionRateCommand(ctx, account, app, sub, r.PathValue("commandID"))
	default:
		after := int64(0)
		values := r.URL.Query()
		if raw, ok := values["after"]; ok {
			if len(raw) != 1 {
				return nil, state.ErrInvalidArgument
			}
			var err error
			after, err = strconv.ParseInt(raw[0], 10, 64)
			if err != nil || after < 0 {
				return nil, state.ErrInvalidArgument
			}
		}
		return store.ListEventSubscriptionRateCommands(ctx, account, app, sub, after)
	}
}
