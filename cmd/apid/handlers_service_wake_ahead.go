package main

import (
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// serviceWakeAheadRequestMaxBytes bounds the one-field PUT body.
const serviceWakeAheadRequestMaxBytes = 1024

// getServiceWakeAhead serves GET /v1/apps/{slug}/service-wake-ahead (ADR-956).
func (s *server) getServiceWakeAhead(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.store.(state.ServiceWakeAheadStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("service wake-ahead"))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	setting, err := store.GetServiceWakeAhead(r.Context(), acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("service wake-ahead"))
		return
	}
	writeJSON(w, http.StatusOK, serviceWakeAheadResponse(app.Slug, setting))
}

// putServiceWakeAhead serves PUT /v1/apps/{slug}/service-wake-ahead. apid is
// the only writer; gatewayd-internal picks the change up within its cache TTL.
func (s *server) putServiceWakeAhead(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.store.(state.ServiceWakeAheadStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("service wake-ahead"))
		return
	}
	var req api.SetServiceWakeAheadRequest
	if err := decodeJSONSized(r, &req, serviceWakeAheadRequestMaxBytes); err != nil || req.Enabled == nil {
		api.WriteProblem(w, api.ErrValidation("body must be {\"enabled\": true} or {\"enabled\": false}"))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	setting, err := store.SetServiceWakeAhead(r.Context(), acct.ID, app.ID, *req.Enabled)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such app")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("service wake-ahead"))
		return
	}
	s.audit.Emit(r.Context(), "service_wake_ahead.updated", &acct.ID, map[string]any{"app_id": app.ID, "enabled": setting.Enabled})
	writeJSON(w, http.StatusOK, serviceWakeAheadResponse(app.Slug, setting))
}

func serviceWakeAheadResponse(slug string, setting state.ServiceWakeAheadSetting) api.ServiceWakeAheadResponse {
	out := api.ServiceWakeAheadResponse{Slug: slug, Enabled: setting.Enabled}
	if !setting.UpdatedAt.IsZero() {
		out.UpdatedAt = setting.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	return out
}
