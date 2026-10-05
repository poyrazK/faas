package main

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apphealth"
	"github.com/onebox-faas/faas/pkg/state"
)

// Read-only evidence projection. Ownership is resolved before any telemetry
// fetch. It never calls a scheduler, VM manager, or customer endpoint.
func (s *server) getAppHealth(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug")) //nolint:contextcheck // loadApp uses r.Context().
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.AppHealthCollectionTimeout)
	defer cancel()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, apphealth.Collect(ctx, s.store, s.promqlClient, app, acct.Plan.PerAppMetricsAllowed()))
}
