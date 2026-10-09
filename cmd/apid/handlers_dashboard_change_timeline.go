package main

import (
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/dashboard/views"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
)

// changeTimelineDashboardRanges are the windows the page offers. Both end
// now, so the change strip shares the sparklines' time axis.
var changeTimelineDashboardRanges = map[string]time.Duration{"24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour}

const changeTimelineChartWidth, changeTimelineChartHeight = 720, 48

// renderAppChangesDashboard serves GET /dashboard/apps/{slug}/changes
// (ADR-741): the change timeline as markers under the app's error-rate and
// p95 charts, plus the event table.
func (s *server) renderAppChangesDashboard(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	app, err := s.store.AppBySlug(r.Context(), r.PathValue("slug"))
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}
	data := s.dashboardAppChangesData(r, acct, app)
	page := dashboard.Page{Title: "What changed", Body: "app_changes", Account: dashboardAccountView(acct, 0), Data: data}
	if err := dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, s.log, err)
	}
}

func (s *server) dashboardAppChangesData(r *http.Request, acct state.Account, app state.App) dashboard.AppChangesData {
	data := dashboard.AppChangesData{Enabled: s.changeTimelineEnabled, AppSlug: app.Slug, Range: "24h", Ranges: []string{"24h", "7d"}}
	if !data.Enabled {
		return data
	}
	if _, ok := changeTimelineDashboardRanges[r.URL.Query().Get("range")]; ok {
		data.Range = r.URL.Query().Get("range")
	}
	now := time.Now().UTC()
	since := now.Add(-changeTimelineDashboardRanges[data.Range])
	timeline := s.appChangeTimeline(r.Context(), acct, app, since, now, now)
	data.Timeline = &timeline
	data.MarkersSVG = views.RenderChangeMarkerStrip(since, now, timeline.Events, changeTimelineChartWidth, changeTimelineChartHeight/2)

	switch {
	case !acct.Plan.PerAppMetricsAllowed():
		data.MetricsNote = "Error-rate and latency charts start on the Hobby plan; the change list below is complete."
	case s.promqlClient == nil:
		data.MetricsNote = "Metrics are unavailable right now; the change list below is complete."
	default:
		ctx, cancel := budgetCtx(r.Context(), 3*time.Second)
		defer cancel()
		series := appmetrics.FetchRange(ctx, s.promqlClient, s.log, app.ID, data.Range)
		data.ErrorRateSVG = views.RenderErrorRateSparkline(series.ErrorRate, changeTimelineChartWidth, changeTimelineChartHeight)
		data.P95SVG = views.RenderAreaSparkline(series.Latency.P95, changeTimelineChartWidth, changeTimelineChartHeight, "#1a4480", "")
		if strings.TrimSpace(string(data.ErrorRateSVG)) == "" && strings.TrimSpace(string(data.P95SVG)) == "" {
			data.MetricsNote = "No request metrics in this window."
		}
	}
	return data
}
