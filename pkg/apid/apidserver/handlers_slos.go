package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// sloStore resolves the ADR-747 store or writes a 503. The interface is
// optional on state.Store so pre-ADR-747 fakes keep compiling.
func (s *server) sloStore(w http.ResponseWriter) (state.SLOStore, bool) {
	store, ok := s.store.(state.SLOStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("SLO definitions are unavailable on this deployment"))
	}
	return store, ok
}

// sloApp applies the shared SLO gates: the Hobby+ per-app metrics plan
// (ADR-082's gate), the store, and the IDOR-safe app lookup.
func (s *server) sloApp(w http.ResponseWriter, r *http.Request, acct state.Account) (state.SLOStore, state.App, bool) {
	if !acct.Plan.PerAppMetricsAllowed() {
		api.WriteProblem(w, api.ErrPlanPerAppMetricsNotAllowed(acct.Plan))
		return nil, state.App{}, false
	}
	store, ok := s.sloStore(w)
	if !ok {
		return nil, state.App{}, false
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug")) //nolint:contextcheck // loadApp uses r.Context().
	return store, app, ok
}

// listSLOs serves GET /v1/apps/{slug}/slos (ADR-747).
func (s *server) listSLOs(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.sloApp(w, r, acct)
	if !ok {
		return
	}
	rows, err := store.ListSLOs(r.Context(), app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list SLOs"))
		return
	}
	out := make([]api.SLOResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, sloResponse(row))
	}
	writeJSON(w, http.StatusOK, out)
}

// createSLO serves POST /v1/apps/{slug}/slos (ADR-747).
func (s *server) createSLO(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.CreateSLORequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	objective, prob := api.ValidateCreateSLO(req)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	store, app, ok := s.sloApp(w, r, acct)
	if !ok {
		return
	}
	row, err := store.CreateSLO(r.Context(), state.SLO{
		AccountID: acct.ID, AppID: app.ID, Name: req.Name, SLI: req.SLI,
		LatencyThresholdMS: req.LatencyThresholdMS, ObjectiveBP: objective, WindowDays: req.WindowDays,
	}, api.MaxSLOsPerApp)
	switch {
	case errors.Is(err, state.ErrSLOLimit):
		api.WriteProblem(w, api.ErrSLOLimitReached(api.MaxSLOsPerApp))
		return
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeSLOInvalid, "SLO name already exists", "this app already has an SLO with that name"))
		return
	case err != nil:
		api.WriteProblem(w, api.ErrCapacity("could not create SLO"))
		return
	}
	s.audit.Emit(r.Context(), "slo.created", &acct.ID, map[string]any{
		"slo_id": row.ID, "app_id": app.ID, "name": row.Name, "sli": row.SLI,
		"latency_threshold_ms": row.LatencyThresholdMS, "objective_bp": row.ObjectiveBP, "window_days": row.WindowDays,
	})
	writeJSON(w, http.StatusCreated, sloResponse(row))
}

// getSLO serves GET /v1/apps/{slug}/slos/{id} (ADR-747): the definition
// plus its error-budget status.
func (s *server) getSLO(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.sloApp(w, r, acct)
	if !ok {
		return
	}
	row, err := store.GetSLO(r.Context(), app.ID, r.PathValue("id"))
	if err != nil {
		s.notFound(w, "no such SLO")
		return
	}
	resp := sloResponse(row)
	resp.Status = s.sloStatus(r.Context(), row, time.Now())
	writeJSON(w, http.StatusOK, resp)
}

// deleteSLO serves DELETE /v1/apps/{slug}/slos/{id} (ADR-747).
func (s *server) deleteSLO(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.sloApp(w, r, acct)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if p := s.sloGateProblem(r.Context(), app, id); p != nil {
		api.WriteProblem(w, p)
		return
	}
	if err := store.DeleteSLO(r.Context(), app.ID, id); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such SLO")
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not delete SLO"))
		return
	}
	s.audit.Emit(r.Context(), "slo.deleted", &acct.ID, map[string]any{"slo_id": id, "app_id": app.ID})
	w.WriteHeader(http.StatusNoContent)
}

// sloGateProblem refuses to delete an SLO that release gates judge
// (ADR-20261019071734 amendment 2). Postgres cascaded the delete to those
// gates with no audit, so a release lost a guard without anyone removing it.
func (s *server) sloGateProblem(ctx context.Context, app state.App, sloID string) *api.Problem {
	gates, ok := s.store.(state.ReleaseGateStore)
	if !ok {
		return nil
	}
	rows, err := gates.ListReleaseGates(ctx, app.ID)
	if err != nil {
		return api.ErrCapacity("could not read the app's release gates")
	}
	var names []string
	for _, g := range rows {
		if g.SLOID == sloID {
			names = append(names, g.Name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	return api.NewProblem(http.StatusConflict, api.CodeConflict, "SLO used by release gates",
		fmt.Sprintf("Release gates judge releases of %s on this SLO: %s.", app.Slug, strings.Join(names, ", "))).
		WithHint(fmt.Sprintf("Remove those gates first with `gregale release-gates rm NAME --app %s`, then delete the SLO.", app.Slug))
}

func sloResponse(row state.SLO) api.SLOResponse {
	return api.SLOResponse{
		ID: row.ID, Name: row.Name, SLI: row.SLI, LatencyThresholdMS: row.LatencyThresholdMS,
		ObjectivePct: api.SLOObjectivePct(row.ObjectiveBP), WindowDays: row.WindowDays,
		CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339),
	}
}
