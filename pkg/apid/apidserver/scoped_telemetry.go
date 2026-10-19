package apidserver

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/workerrecovery"
	"github.com/onebox-faas/faas/pkg/apphealth"
	"github.com/onebox-faas/faas/pkg/state"
)

// An absent selector retains existing account-wide CLI/dashboard behavior.
// An explicit default selector must never fall back to the aggregate.
func (s *server) telemetryEnvironment(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App) (state.App, state.ProjectEnvironment, string, bool, *api.Problem) {
	q := r.URL.Query()
	selected := r.URL.RawQuery != ""
	if !selected {
		return app, state.ProjectEnvironment{}, "", false, nil
	}
	for key, values := range q {
		if (key != "scope" && key != "environment") || len(values) != 1 || values[0] == "" {
			return app, state.ProjectEnvironment{}, "", true, api.ErrValidation("select exactly one telemetry environment or scope=default")
		}
	}
	if len(q) != 1 {
		return app, state.ProjectEnvironment{}, "", true, api.ErrValidation("select exactly one telemetry environment or scope=default")
	}
	if q.Get("scope") != "" {
		if q.Get("scope") != "default" {
			return app, state.ProjectEnvironment{}, "", true, api.ErrValidation("scope selector must be default; use environment for named scopes")
		}
		return app, state.ProjectEnvironment{}, api.DefaultEnvScope, true, nil
	}
	resolved, env, problem := s.appEnvironmentSettings(w, r, acct, app, false)
	return resolved, env, env.Slug, true, problem
}
func (s *server) scopedTelemetryIdentity(ctx context.Context, acct state.Account, app state.App, env state.ProjectEnvironment) *api.Problem {
	if env.ID == "" {
		return nil
	}
	current, err := s.store.ProjectEnvironmentBySlug(ctx, acct.ID, app.ProjectID, env.Slug)
	if err != nil || current.ID != env.ID {
		return api.NewProblem(409, api.CodeConflict, "Environment changed", "Inspect the environment again")
	}
	return nil
}
func (s *server) scopedRoutes(ctx context.Context, acct state.Account, app state.App, env state.ProjectEnvironment, scope string) api.AppRoutesResponse {
	end := time.Now().UTC()
	start := end.Add(-api.AppHealthMetricsWindow)
	out := api.AppRoutesResponse{AppID: app.ID, Slug: app.Slug, Scope: scope, ProjectID: env.ProjectID, EnvironmentID: env.ID, Routes: []string{}, Source: api.AppRoutesSourceUnavailable, Coverage: "deployment_attributed_history", WindowStart: &start, WindowEnd: &end}
	store, ok := s.store.(state.ScopedRouteStore)
	if !ok || !acct.Plan.DebugTelemetryEnabled() {
		return out
	}
	readCtx, cancel := context.WithTimeout(ctx, routesDialTimeout)
	defer cancel()
	rows, err := store.ListScopedObservedRoutes(readCtx, acct.ID, app.ID, scope, start, end)
	if err != nil {
		return out
	}
	out.Source = api.AppRoutesSourcePartial
	for _, route := range rows {
		if strings.HasSuffix(route, " __route_other__") || route == "__route_other__" {
			out.CapHit = true
			continue
		}
		out.Routes = append(out.Routes, route)
	}
	if len(out.Routes) > api.RouteMetricsPerAppCap {
		out.Routes = out.Routes[:api.RouteMetricsPerAppCap]
		out.CapHit = true
	}
	return out
}
func (s *server) scopedOperationalSummary(ctx context.Context, acct state.Account, app state.App, env state.ProjectEnvironment, scope string) api.AppOperationalSummary {
	ctx, cancel := context.WithTimeout(ctx, api.AppOperationalReadTimeout)
	defer cancel()
	out := api.AppOperationalSummary{Version: 1, AppID: app.ID, Scope: scope, ProjectID: env.ProjectID, EnvironmentID: env.ID, CheckedAt: time.Now().UTC(), Monitoring: api.AppOperationalMonitoring{Status: "unknown", Reason: "environment_monitor_unavailable", Coverage: "deployment_attributed_only"}}
	out.Monitoring = s.scopedRouteMonitoring(ctx, acct, app, env, scope)
	health := apphealth.CollectScope(ctx, s.store, s.promqlClient, app, acct.Plan.PerAppMetricsAllowed(), scope)
	health.ProjectID, health.EnvironmentID = env.ProjectID, env.ID
	out.Health = &health
	out.Recovery = s.scopedOperationalRecovery(ctx, acct, app, scope)
	if store, ok := s.store.(state.ScopedOperationalStore); ok && acct.Plan.DebugTelemetryEnabled() {
		incident, err := store.GetScopedOpenMonitorIncident(ctx, acct.ID, app.ID, scope)
		out.Monitoring.IncidentsAvailable = err == nil
		out.Monitoring.Incident = incident
	}
	out.Recommendations = scopedMonitorRecommendations(out.Monitoring)
	for _, r := range appOperationalRecommendations(app.Slug, out) {
		if !strings.HasPrefix(r.Code, "production_") {
			out.Recommendations = append(out.Recommendations, r)
		}
	}
	return out
}
func (s *server) scopedOperationalRecovery(ctx context.Context, acct state.Account, app state.App, scope string) api.AppOperationalRecovery {
	out := api.AppOperationalRecovery{Coverage: "explicit_scope_only", Rollbacks: []api.AppOperationalRollback{}, Restarts: []api.RuntimeConfigRestartStatusResponse{}}
	out.Workers = workerrecovery.Collect(ctx, s.store, app, acct, scope, time.Now().UTC())
	if store, ok := s.store.(state.ScopedOperationalStore); ok {
		rows, err := store.ListScopedPendingRollbacks(ctx, acct.ID, app.ID, scope)
		out.RollbacksAvailable = err == nil
		if err == nil {
			out.RollbacksTruncated = len(rows) > api.AppOperationalRecoveryLimit
			if out.RollbacksTruncated {
				rows = rows[:api.AppOperationalRecoveryLimit]
			}
			for _, r := range rows {
				out.Rollbacks = append(out.Rollbacks, api.AppOperationalRollback{ID: r.ID, Scope: r.Scope, Status: r.Status, Code: r.Code, TargetDeploymentID: r.TargetDeploymentID, CurrentDeploymentID: r.CurrentDeploymentID, UpdatedAt: r.UpdatedAt})
			}
		}
	}
	if store, ok := s.store.(state.ScopedRestartStore); ok {
		rows, err := store.ListScopedPendingRestarts(ctx, acct.ID, app.ID, scope)
		out.RestartsAvailable = err == nil
		if err == nil {
			out.RestartsTruncated = len(rows) > api.AppOperationalRecoveryLimit
			if out.RestartsTruncated {
				rows = rows[:api.AppOperationalRecoveryLimit]
			}
			out.Restarts = rows
		}
	}
	return out
}
func (s *server) getWorkerRolloutObservation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	app, env, scope, selected, problem := s.telemetryEnvironment(w, r, acct, app)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if !selected {
		api.WriteProblem(w, api.ErrValidation("select a telemetry scope"))
		return
	}
	d, err := s.store.DeploymentByID(r.Context(), r.PathValue("id"))
	if err != nil || d.AppID != app.ID || recoveryScope(d.Scope) != scope {
		s.notFound(w, "no such deployment in selected environment")
		return
	}
	resolved, err := state.ResolveAppForDeployment(r.Context(), s.store, app, d)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read deployment settings"))
		return
	}
	if resolved.Manifest.ExecutionMode != api.ExecutionModeWorker {
		api.WriteProblem(w, api.ErrValidation("observation requires a worker deployment"))
		return
	}
	store, ok := s.store.(state.WorkerRolloutObservationStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("worker observations unavailable"))
		return
	}
	out, err := store.ReadWorkerRolloutObservation(r.Context(), acct.ID, app.ID, d.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("worker observations unavailable"))
		return
	}
	if problem := s.scopedTelemetryIdentity(r.Context(), acct, app, env); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	out.ProjectID, out.EnvironmentID = env.ProjectID, env.ID
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}
