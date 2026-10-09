package main

import (
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// syntheticApp applies the shared ADR-748 gates: the Hobby+ per-app
// observability plan, the store, and the IDOR-safe app lookup.
func (s *server) syntheticApp(w http.ResponseWriter, r *http.Request, acct state.Account) (state.SyntheticCheckStore, state.App, bool) {
	if !acct.Plan.PerAppMetricsAllowed() {
		api.WriteProblem(w, api.ErrPlanPerAppMetricsNotAllowed(acct.Plan))
		return nil, state.App{}, false
	}
	store, ok := s.store.(state.SyntheticCheckStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("synthetic checks are unavailable on this deployment"))
		return nil, state.App{}, false
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug")) //nolint:contextcheck // loadApp uses r.Context().
	return store, app, ok
}

// listSyntheticChecks serves GET /v1/apps/{slug}/synthetics.
func (s *server) listSyntheticChecks(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.syntheticApp(w, r, acct)
	if !ok {
		return
	}
	rows, err := store.ListSyntheticChecks(r.Context(), app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list synthetic checks"))
		return
	}
	out := make([]api.SyntheticCheckResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, s.syntheticCheckResponse(app, row))
	}
	writeJSON(w, http.StatusOK, out)
}

// createSyntheticCheck serves POST /v1/apps/{slug}/synthetics.
func (s *server) createSyntheticCheck(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.CreateSyntheticCheckRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	req, prob := api.NormalizeCreateSyntheticCheck(req)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	store, app, ok := s.syntheticApp(w, r, acct)
	if !ok {
		return
	}
	row, err := store.CreateSyntheticCheck(r.Context(), state.SyntheticCheck{
		AccountID: acct.ID, AppID: app.ID, Name: req.Name, Method: req.Method, Path: req.Path,
		ExpectedStatus: req.ExpectedStatus, TimeoutMS: req.TimeoutMS, IntervalSeconds: req.IntervalSeconds,
	}, api.MaxSyntheticChecksPerApp)
	switch {
	case errors.Is(err, state.ErrSyntheticCheckLimit):
		api.WriteProblem(w, api.ErrSyntheticCheckLimitReached(api.MaxSyntheticChecksPerApp))
		return
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeSyntheticCheckInvalid, "Synthetic check name already exists", "this app already has a synthetic check with that name"))
		return
	case err != nil:
		api.WriteProblem(w, api.ErrCapacity("could not create synthetic check"))
		return
	}
	s.audit.Emit(r.Context(), "synthetic_check.created", &acct.ID, map[string]any{
		"check_id": row.ID, "app_id": app.ID, "name": row.Name, "method": row.Method, "path": row.Path,
		"interval_seconds": row.IntervalSeconds,
	})
	writeJSON(w, http.StatusCreated, s.syntheticCheckResponse(app, row))
}

// getSyntheticCheck serves GET /v1/apps/{slug}/synthetics/{id}.
func (s *server) getSyntheticCheck(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.syntheticApp(w, r, acct)
	if !ok {
		return
	}
	row, err := store.GetSyntheticCheck(r.Context(), app.ID, r.PathValue("id"))
	if err != nil {
		s.notFound(w, "no such synthetic check")
		return
	}
	writeJSON(w, http.StatusOK, s.syntheticCheckResponse(app, row))
}

// updateSyntheticCheck serves PATCH /v1/apps/{slug}/synthetics/{id}: pause
// or resume. Other fields are fixed; delete and recreate to change them.
func (s *server) updateSyntheticCheck(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.UpdateSyntheticCheckRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	store, app, ok := s.syntheticApp(w, r, acct)
	if !ok {
		return
	}
	row, err := store.SetSyntheticCheckEnabled(r.Context(), app.ID, r.PathValue("id"), req.Enabled)
	if err != nil {
		s.notFound(w, "no such synthetic check")
		return
	}
	s.audit.Emit(r.Context(), "synthetic_check.updated", &acct.ID, map[string]any{"check_id": row.ID, "app_id": app.ID, "enabled": row.Enabled})
	writeJSON(w, http.StatusOK, s.syntheticCheckResponse(app, row))
}

// deleteSyntheticCheck serves DELETE /v1/apps/{slug}/synthetics/{id}.
func (s *server) deleteSyntheticCheck(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.syntheticApp(w, r, acct)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if err := store.DeleteSyntheticCheck(r.Context(), app.ID, id); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such synthetic check")
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not delete synthetic check"))
		return
	}
	s.audit.Emit(r.Context(), "synthetic_check.deleted", &acct.ID, map[string]any{"check_id": id, "app_id": app.ID})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) syntheticCheckResponse(app state.App, row state.SyntheticCheck) api.SyntheticCheckResponse {
	return api.SyntheticCheckResponse{
		ID: row.ID, Name: row.Name, Method: row.Method, Path: row.Path,
		URL:            appURLForDomain(app.Slug, s.domain) + row.Path,
		ExpectedStatus: row.ExpectedStatus, TimeoutMS: row.TimeoutMS, IntervalSeconds: row.IntervalSeconds,
		Enabled: row.Enabled, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339),
	}
}
