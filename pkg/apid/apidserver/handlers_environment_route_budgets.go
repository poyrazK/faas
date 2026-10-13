package apidserver

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routemonitor"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *server) environmentBudgetTarget(w http.ResponseWriter, r *http.Request, a state.Account) (state.App, state.ProjectEnvironment, string, state.EnvironmentRouteBudgetStore, bool) {
	app, ok := s.loadApp(w, r, a, r.PathValue("slug"))
	if !ok {
		return app, state.ProjectEnvironment{}, "", nil, false
	}
	app, env, scope, selected, problem := s.telemetryEnvironment(w, r, a, app)
	if !selected && problem == nil {
		problem = api.ErrValidation("select environment or scope=default")
	}
	if problem != nil {
		api.WriteProblem(w, problem)
		return app, env, scope, nil, false
	}
	store, ok := s.store.(state.EnvironmentRouteBudgetStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("scoped route budget configuration unavailable"))
	}
	return app, env, scope, store, ok
}
func (s *server) getEnvironmentRouteBudgets(w http.ResponseWriter, r *http.Request, a state.Account) {
	app, env, scope, store, ok := s.environmentBudgetTarget(w, r, a)
	if !ok {
		return
	}
	out, err := store.GetEnvironmentRouteBudgets(r.Context(), a.ID, app.ID, scope)
	if err != nil {
		s.environmentBudgetError(w, err, a)
		return
	}
	if out.AppID != app.ID || out.Scope != scope || out.EnvironmentID != env.ID || out.ProjectID != env.ProjectID {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Environment changed", "Inspect again"))
		return
	}
	if problem := s.scopedTelemetryIdentity(r.Context(), a, app, env); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *server) putEnvironmentRouteBudgets(w http.ResponseWriter, r *http.Request, a state.Account) {
	var req api.SetEnvironmentRouteBudgetsRequest
	if err := decodeJSONSized(r, &req, api.RouteHealthRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid scoped route budgets"))
		return
	}
	if err := routemonitor.ValidateEnvironmentBudgets(req); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	app, env, scope, store, ok := s.environmentBudgetTarget(w, r, a)
	if !ok {
		return
	}
	if req.AppID != app.ID || req.EnvironmentID != env.ID || req.ProjectID != env.ProjectID {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "App or environment changed", "Inspect again"))
		return
	}
	out, err := store.SetEnvironmentRouteBudgets(r.Context(), a.ID, app.ID, scope, req)
	if err != nil {
		s.environmentBudgetError(w, err, a)
		return
	}
	s.audit.Emit(r.Context(), "environment_route_budgets.updated", &a.ID, map[string]any{"app_id": app.ID, "scope": scope, "environment_id": env.ID, "revision": out.Revision, "budget_source": out.BudgetSource, "request_id": req.RequestID})
	writeJSON(w, http.StatusOK, out)
}

func (s *server) environmentBudgetError(w http.ResponseWriter, err error, a state.Account) {
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Request ID reused", "The request ID was already bound to another proposal"))
		return
	}
	s.routeMonitorError(w, err, a)
}
