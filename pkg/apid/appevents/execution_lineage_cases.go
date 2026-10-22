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

func writeLineageCaseError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("invalid subscription, pagination or retry guard"))
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "no retained lineage case or subscription")
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(409, "event_lineage_case_changed", "Lineage case changed", "refresh the case revision and diagnostic identity; a request UUID may only be reused with its original guards"))
	case errors.Is(err, context.DeadlineExceeded):
		api.WriteProblem(w, api.NewProblem(504, api.CodeInternal, "Lineage request timed out", "retry with the same request UUID and expected revision"))
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("event execution lineage case"))
	default:
		return false
	}
	return true
}
func (s *Service) ListEventExecutionLineageCases(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventSubscriptionControlTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.EventExecutionLineageCasesStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("lineage cases unavailable"))
		return
	}
	after := int64(0)
	limit := 0
	var err error
	if raw := r.URL.Query().Get("after"); raw != "" {
		after, err = strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeLineageCaseError(w, state.ErrInvalidArgument)
			return
		}
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil {
			writeLineageCaseError(w, state.ErrInvalidArgument)
			return
		}
	}
	out, err := store.ListEventExecutionLineageCases(ctx, acct.ID, app.ID, r.PathValue("subscriptionID"), after, limit)
	if !writeLineageCaseError(w, err) {
		httpkit.WriteJSON(w, 200, out)
	}
}
func (s *Service) RetryEventExecutionLineageCase(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventSubscriptionControlTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.EventExecutionLineageCasesStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("lineage cases unavailable"))
		return
	}
	id, err := strconv.ParseInt(r.PathValue("outboxID"), 10, 64)
	if err != nil {
		writeLineageCaseError(w, state.ErrInvalidArgument)
		return
	}
	var req api.RetryEventExecutionLineageCaseRequest
	if !httpkit.DecodeJSONLimit(w, r, &req, 4096) {
		return
	}
	out, err := store.RetryEventExecutionLineageCase(ctx, acct.ID, app.ID, r.PathValue("subscriptionID"), id, req)
	if !writeLineageCaseError(w, err) {
		httpkit.WriteJSON(w, 200, out)
	}
}
