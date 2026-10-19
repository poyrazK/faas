package apidserver

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/workerrecovery"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getAppOperationalSummary(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	app, env, scope, selected, problem := s.telemetryEnvironment(w, r, acct, app)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if selected {
		out := s.scopedOperationalSummary(r.Context(), acct, app, env, scope)
		if problem := s.scopedTelemetryIdentity(r.Context(), acct, app, env); problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, out)
		return
	}
	writeJSON(w, http.StatusOK, s.appOperationalSummary(r.Context(), acct, app))
}

// Both the API and dashboard call this collector. Reads have a shared deadline
// and do not call provisioning, wake, monitor-worker or recovery mutation paths.
func (s *server) appOperationalSummary(ctx context.Context, acct state.Account, app state.App) api.AppOperationalSummary {
	ctx, cancel := context.WithTimeout(ctx, api.AppOperationalReadTimeout)
	defer cancel()
	out := api.AppOperationalSummary{Version: 1, AppID: app.ID, CheckedAt: time.Now().UTC()}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		out.Monitoring = s.appOperationalMonitoring(ctx, acct, app)
	}()
	go func() {
		defer wg.Done()
		out.Recovery = s.appOperationalRecovery(ctx, acct, app)
	}()
	wg.Wait()
	out.Recommendations = appOperationalRecommendations(app.Slug, out)
	return out
}

func (s *server) appOperationalMonitoring(ctx context.Context, acct state.Account, app state.App) api.AppOperationalMonitoring {
	out := api.AppOperationalMonitoring{Status: "unknown", Reason: "monitor_unavailable", Coverage: "observed_only"}
	store, ok := s.store.(state.RouteMonitorStore)
	if !ok {
		return out
	}
	report, err := store.GetRouteMonitorReport(ctx, acct.ID, app.ID)
	if err != nil {
		if errors.Is(err, state.ErrRouteInvestigationPlan) {
			out.Reason = "plan_unavailable"
		}
		return out
	}
	out.Available, out.Status, out.Reason = true, report.Status, report.Reason
	out.Coverage, out.DeploymentID = report.Coverage, report.DeploymentID
	out.CheckedAt = &report.CheckedAt
	appOperationalWindows(&out, report)
	if incidents, ok := s.store.(state.AppOperationalStore); ok && acct.Plan.DebugTelemetryEnabled() {
		out.Incident, err = incidents.GetAppOpenMonitorIncident(ctx, acct.ID, app.ID)
		out.IncidentsAvailable = err == nil
	}
	return out
}

func appOperationalWindows(out *api.AppOperationalMonitoring, report api.RouteMonitorReport) {
	for _, route := range report.Routes {
		for _, window := range route.Windows {
			if out.WindowStart == nil || window.Start.Before(*out.WindowStart) {
				at := window.Start
				out.WindowStart = &at
			}
			if out.WindowEnd == nil || window.End.After(*out.WindowEnd) {
				at := window.End
				out.WindowEnd = &at
			}
		}
	}
}

func (s *server) appOperationalRecovery(ctx context.Context, acct state.Account, app state.App) api.AppOperationalRecovery {
	out := api.AppOperationalRecovery{Rollbacks: []api.AppOperationalRollback{}, Restarts: []api.RuntimeConfigRestartStatusResponse{}}
	out.Workers = workerrecovery.Collect(ctx, s.store, app, acct, "", time.Now().UTC())
	if store, ok := s.store.(state.AppOperationalStore); ok {
		rows, err := store.ListAppPendingRollbacks(ctx, acct.ID, app.ID)
		out.RollbacksAvailable = err == nil
		if err != nil {
			rows = nil
		}
		out.RollbacksTruncated = len(rows) > api.AppOperationalRecoveryLimit
		if out.RollbacksTruncated {
			rows = rows[:api.AppOperationalRecoveryLimit]
		}
		for _, operation := range rows {
			out.Rollbacks = append(out.Rollbacks, api.AppOperationalRollback{ID: operation.ID, Scope: operation.Scope,
				Status: operation.Status, Code: operation.Code, TargetDeploymentID: operation.TargetDeploymentID,
				CurrentDeploymentID: operation.CurrentDeploymentID, UpdatedAt: operation.UpdatedAt})
		}
	}
	if store, ok := s.store.(state.AppOperationalRestartStore); ok {
		rows, err := store.ListAppPendingRestarts(ctx, acct.ID, app.ID)
		out.RestartsAvailable = err == nil
		if err == nil {
			out.RestartsTruncated = len(rows) > api.AppOperationalRecoveryLimit
			if out.RestartsTruncated {
				rows = rows[:api.AppOperationalRecoveryLimit]
			}
			out.Restarts = append(out.Restarts, rows...)
		}
	}
	return out
}
