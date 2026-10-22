package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/rolloutevidence"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// rolloutEvidence explains rollouts with request evidence (ADR-127
// amendment following ADR-1447), reusing the debugger's response shapes.
func (s *server) rolloutEvidence() rolloutevidence.Reader {
	r := rolloutevidence.Reader{Store: s.store, Regressions: s.activeRegressionItems, RequestItem: debugTelemetryRowToItem}
	if holds, ok := s.store.(state.RolloutDecisionStore); ok {
		r.Holds = holds
	}
	return r
}

func (s *server) activeRegressionItems(ctx context.Context, appID string, since time.Duration) ([]api.DebugRegressionItem, error) {
	rows, err := s.store.ListActiveRegressionsByApp(ctx, sqlc.ListActiveRegressionsByAppParams{
		AppID:   stringToPgUUID(appID),
		Column2: pgtype.Interval{Microseconds: int64(since / time.Microsecond), Valid: true},
	})
	if err != nil {
		return nil, err
	}
	items := make([]api.DebugRegressionItem, len(rows))
	for i, row := range rows {
		items[i] = debugRegressionRowToItem(row)
	}
	return items, nil
}

// debugRetention is the plan's debugger retention, the bound on every
// evidence window.
func debugRetention(limits api.Limits) time.Duration {
	return time.Duration(limits.DebugTelemetryRetentionDays) * 24 * time.Hour
}

// debugRolloutHandler — GET /v1/apps/{slug}/debug/rollout. Explains why the
// app's newest canary rollout (or ?deployment_id) was rolled back or is held.
func (s *server) debugRolloutHandler(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("debugger", acct.Plan))
		return
	}
	deploymentID := strings.TrimSpace(r.URL.Query().Get("deployment_id"))
	if deploymentID != "" {
		id, err := uuid.Parse(deploymentID)
		if err != nil {
			api.WriteProblem(w, api.ErrValidation("deployment_id must be a UUID"))
			return
		}
		deploymentID = id.String()
	}
	out, err := s.rolloutEvidence().Explain(r.Context(), app, deploymentID, debugRetention(limits))
	if errors.Is(err, rolloutevidence.ErrDeploymentNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Deployment not found", "No such deployment for this app."))
		return
	}
	if err != nil {
		writeCustomerInternalProblem(w, r, s.log, "explain rollout",
			"Gregale could not read the evidence for this rollout.", "Retry in a moment; if it continues, contact support.", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// dashboardDebugStatusView projects the debug status onto the dashboard's
// status card; each request links to its evidence on the same page.
func dashboardDebugStatusView(status api.DebugStatusResponse, slug string) *dashboard.DebugStatusView {
	view := &dashboard.DebugStatusView{
		Verdict: status.Verdict, VerdictLabel: strings.ReplaceAll(status.Verdict, "_", " "), Headline: status.Headline,
		Since: status.Since, Requests: status.Health.Requests, ServerErrors: status.Health.ServerErrors, P95MS: status.Health.P95MS,
		Regressions: status.RegressionsTotal, TracingEnabled: status.TracingEnabled,
		CLICommand: "gregale debug status " + slug,
	}
	link := func(item api.DebugTelemetryRequestItem) dashboard.DebugStatusRequestView {
		values := url.Values{}
		values.Set("since", status.Since)
		values.Set("request_id", item.ID)
		return dashboard.DebugStatusRequestView{
			ReceivedAt: item.ReceivedAt,
			Label:      fmt.Sprintf("%s %d · %d ms", requestLabel(item.Method, item.Route), item.Status, item.LatencyMS),
			URL:        "/dashboard/apps/" + url.PathEscape(slug) + "/debug?" + values.Encode(),
		}
	}
	if status.Rollout != nil {
		view.Release = status.Rollout.Headline
		for _, item := range status.Rollout.Examples {
			view.Examples = append(view.Examples, link(item))
		}
		if status.Rollout.State != api.DebugRolloutRollingOut {
			view.CLICommand = "gregale debug rollout " + slug
		}
	}
	for _, item := range status.RecentFailures {
		view.Failures = append(view.Failures, link(item))
	}
	return view
}

// requestLabel renders "METHOD /path" without repeating a method the route
// already carries.
func requestLabel(method, route string) string {
	if method == "" || strings.HasPrefix(route, method+" ") {
		return route
	}
	return method + " " + route
}

// debugStatusHandler — GET /v1/apps/{slug}/debug/status. One summary of the
// app's health, release, regressions, and recent failures over ?since
// (default 1h, clamped to the plan's debugger retention).
func (s *server) debugStatusHandler(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("debugger", acct.Plan))
		return
	}
	sinceRaw := r.URL.Query().Get("since")
	since, err := parseDebugSinceStrict(sinceRaw, time.Hour)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	retention := debugRetention(limits)
	if retention > 0 && since > retention {
		since = retention
	}
	out, err := s.rolloutEvidence().Status(r.Context(), app, since, retention)
	if err != nil {
		writeCustomerInternalProblem(w, r, s.log, "debug status",
			"Gregale could not summarize this app's debugger evidence.", "Retry in a moment; if it continues, contact support.", err)
		return
	}
	out.Since = rolloutevidence.FormatWindow(since)
	if sinceRaw != "" {
		out.Since = echoDebugSince(sinceRaw, since)
	}
	writeJSON(w, http.StatusOK, out)
}
