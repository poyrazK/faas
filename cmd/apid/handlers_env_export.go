package main

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// exportAppEnv is deliberately separate from metadata GETs and idempotency
// middleware: plaintext must not enter persisted replay responses or caches.
func (s *server) exportAppEnv(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	var request api.ExportAppEnvRequest
	if err := decodeJSON(r, &request); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid JSON body"))
		return
	}
	if problem := request.Validate(); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	scope, _, problem := scopeFromQuery(r, false)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	rows, err := s.store.ListAppEnvInScope(r.Context(), acct.ID, app.ID, scope)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not export environment variables"))
		return
	}
	values := make(map[string]string, len(rows))
	for _, row := range rows {
		values[row.Key] = row.Value
	}
	s.audit.Emit(r.Context(), "env.exported", &acct.ID, map[string]any{"app_id": app.ID, "scope": scope, "count": len(values)})
	writeJSON(w, http.StatusOK, api.AppEnvExportResponse{AppSlug: app.Slug, Scope: scope, Values: values})
}
