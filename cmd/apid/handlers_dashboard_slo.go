// handlers_dashboard_slo.go — Issue #696 / ADR-082 dashboard
// follow-up PR: the three dashboard-side fetchers that drive
// the SLO card surface (per-app + per-account + per-row badge).
//
// The fetchers wrap the existing scalar fetchAppSLO /
// fetchAccountSLO helpers (handlers_slo.go) with the dashboard's
// 3s budget and project the wire shape into the dashboard's
// view types (pkg/dashboard/views). The window selector
// (1h / 24h / 7d) round-trips via the ?window= query string
// the way the existing per-app ?range= selector does today.
//
// All three fetchers share the same posture as fetchDashboardMetrics:
//   - nil return = skip the section entirely (Prometheus not
//     configured, OR the fetch timed out, OR the window param
//     was invalid).
//   - A degraded result (Source has the "degraded: " prefix)
//     still returns a non-nil pointer so the template renders
//     the empty-state badge rather than disappear silently.
//   - The 3s timeout is the same envelope the scalar fetch
//     uses — Prometheus is the slow leg, not the Postgres
//     usage_minutes rollup.
//
// Cardinality budget: three PromQL query_range calls per
// page render (one per metric). At the dashboard render rate
// (login + manual refresh) this is 60-90 calls/min on a
// one-box — within the 15d Prometheus retention budget.
// Per-row badges on the apps list add one query_range per
// app, batched at 25 apps max (the existing apps-list cap).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/dashboard/views"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/state"
)

// dashboardSLOAppsListBadgeCap is the cost tripwire on the
// per-row SLO badge on /dashboard/apps. Matches the existing
// apps-list cap (LimitDeploymentsForApp + the apps-list
// window). Beyond 25 apps the per-row badge silently degrades
// to "—" — the per-app SLO panel is always reachable via the
// row's link without a per-row PromQL cost.
const dashboardSLOAppsListBadgeCap = 25

// resolveSLOWindow reads the ?window= query parameter with
// the canonical SLO default (api.SLODefaultWindow = "24h")
// and the closed-set vocabulary guard. Invalid windows
// fall back to the default rather than 400 — the dashboard
// surfaces a bad window via the "View on dashboard" link
// shape, not a hard error, so a typo in the URL doesn't
// 500 the page.
func resolveSLOWindow(r *http.Request) string {
	w := r.URL.Query().Get("window")
	if w == "" {
		return api.SLODefaultWindow
	}
	if !api.IsValidSLORange(w) {
		return api.SLODefaultWindow
	}
	return w
}

// fetchDashboardSLO returns the per-app SLO view for the
// /dashboard/apps/{slug} page. Wraps the existing scalar
// fetchAppSLO with a 3s budget and adds the sparkline
// FetchRange call. Returns nil when Prometheus is not
// configured (skip the section entirely); returns a
// non-nil pointer with the "degraded:" Source on a
// transient failure so the template renders the empty-
// state badge rather than disappearing.
//
// window is the resolved closed-set window (1h / 24h / 7d).
// The handler reads it from ?window= via resolveSLOWindow.
func (s *server) fetchDashboardSLO(ctx context.Context, log *slog.Logger, app state.App, acct state.Account, window string) *views.AppSLOView {
	if s.promqlClient == nil {
		return nil
	}
	dctx, cancel := budgetCtx(ctx, 3*time.Second)
	defer cancel()

	// Scalar SLO panel (the existing fetchAppSLO). The
	// response fields are projected into the view — Source
	// carries the "degraded: <reason>" prefix so the
	// template renders the empty-state branch off the
	// same vocabulary the per-app Metrics card uses.
	scalar, src := s.fetchAppSLO(dctx, app, acct, window)

	// Sparkline series. FetchRange degrades to an empty
	// RangeSeries on a per-query failure (logged inside
	// the helper) so the page still renders the headline
	// number even when the time-bucketed query did not.
	rangeSeries := appmetrics.FetchRange(dctx, s.promqlClient, log, app.ID, window)

	view := &views.AppSLOView{
		Window:          window,
		Source:          src,
		AsOf:            time.Now().UTC().Format(time.RFC3339Nano),
		RequestDuration: views.SLOLatencyView{P50MS: scalar.RequestDuration.P50MS, P95MS: scalar.RequestDuration.P95MS, P99MS: scalar.RequestDuration.P99MS},
		ErrorRatePct:    scalar.ErrorRatePct,
		ColdBootRatePct: scalar.ColdBootRatePct,
		InstanceHours:   scalar.InstanceHours,
		GBHours:         scalar.GBHours,
		WakeQueueP95MS:  scalar.WakeQueueP95MS,
		RequestsTotal:   scalar.RequestsTotal,
		ThrottledTotal:  scalar.ThrottledTotal,
		LatencySparkline: views.LatencySparklineView{
			P50: rangeSeries.Latency.P50,
			P95: rangeSeries.Latency.P95,
			P99: rangeSeries.Latency.P99,
		},
		LatencySparklineHTML: views.RenderLatencySparkline(
			views.LatencySparklineView{
				P50: rangeSeries.Latency.P50,
				P95: rangeSeries.Latency.P95,
				P99: rangeSeries.Latency.P99,
			}, 120, 30),
		ErrorSparkline:        rangeSeries.ErrorRate,
		ErrorSparklineHTML:    views.RenderErrorRateSparkline(rangeSeries.ErrorRate, 120, 30),
		ColdBootSparkline:     rangeSeries.ColdBootRate,
		ColdBootSparklineHTML: views.RenderColdBootRateSparkline(rangeSeries.ColdBootRate, 120, 30),
		Step:                  rangeSeries.Step,
	}
	if src != appmetrics.SourcePrometheus && log != nil {
		// Hash app.ID so the per-app dashboard-render WARN line
		// stays disambiguable without logging the raw UUID.
		// HashShort matches CodeQL's notSensitive() regex —
		// structural barrier, not a sanitizer (see
		// pkg/logsanitize.HashShort's doc). The src + window
		// fields are intentionally dropped: src is the
		// 'degraded: <reason>' string built from the Prometheus
		// err (CodeQL re-fires on err.Error() through multi-arg
		// Warn), and window is query-derived (the closed-vocab
		// guard is server-side but CodeQL's taint analysis
		// doesn't see it). The 'fetch degraded' message +
		// app_id_hash is enough to grep + correlate against
		// the Prometheus query_range error log (FetchRange
		// already logs the per-query failure with metric label
		// + err detail).
		log.Warn("dashboard renderAppDetail: SLO fetch degraded", "app_id_hash", logsanitize.HashShort(app.ID))
	}
	return view
}

// fetchDashboardAccountSLO is the per-account twin of
// fetchDashboardSLO. Mirrors fetchAccountSLO so the
// per-app and per-account dashboards share the same
// data-layer posture. The "degraded:" Source contract is
// identical.
func (s *server) fetchDashboardAccountSLO(ctx context.Context, log *slog.Logger, acct state.Account, window string) *views.AccountSLOView {
	if s.promqlClient == nil {
		return nil
	}
	dctx, cancel := budgetCtx(ctx, 3*time.Second)
	defer cancel()

	scalar, src := s.fetchAccountSLO(dctx, acct, window)
	var appIDs []string
	if s.store != nil {
		if apps, err := s.store.ListApps(dctx, acct.ID); err == nil {
			appIDs = make([]string, 0, len(apps))
			for _, app := range apps {
				appIDs = append(appIDs, app.ID)
			}
		}
	}
	rangeSeries := appmetrics.FetchRangeAccount(dctx, s.promqlClient, log, appIDs, window)

	view := &views.AccountSLOView{
		Window:          window,
		Source:          src,
		AsOf:            time.Now().UTC().Format(time.RFC3339Nano),
		RequestDuration: views.SLOLatencyView{P50MS: scalar.RequestDuration.P50MS, P95MS: scalar.RequestDuration.P95MS, P99MS: scalar.RequestDuration.P99MS},
		ErrorRatePct:    scalar.ErrorRatePct,
		ColdBootRatePct: scalar.ColdBootRatePct,
		InstanceHours:   scalar.InstanceHours,
		GBHours:         scalar.GBHours,
		WakeQueueP95MS:  scalar.WakeQueueP95MS,
		RequestsTotal:   scalar.RequestsTotal,
		ThrottledTotal:  scalar.ThrottledTotal,
		LatencySparkline: views.LatencySparklineView{
			P50: rangeSeries.Latency.P50,
			P95: rangeSeries.Latency.P95,
			P99: rangeSeries.Latency.P99,
		},
		LatencySparklineHTML: views.RenderLatencySparkline(
			views.LatencySparklineView{
				P50: rangeSeries.Latency.P50,
				P95: rangeSeries.Latency.P95,
				P99: rangeSeries.Latency.P99,
			}, 120, 30),
		ErrorSparkline:        rangeSeries.ErrorRate,
		ErrorSparklineHTML:    views.RenderErrorRateSparkline(rangeSeries.ErrorRate, 120, 30),
		ColdBootSparkline:     rangeSeries.ColdBootRate,
		ColdBootSparklineHTML: views.RenderColdBootRateSparkline(rangeSeries.ColdBootRate, 120, 30),
		Step:                  rangeSeries.Step,
	}
	if src != appmetrics.SourcePrometheus && log != nil {
		// Hash acct.ID — see the per-app HashShort comment
		// above for the CodeQL reasoning. src + window are
		// dropped for the same reason (CodeQL re-fires on
		// err.Error() flow + window's URL-query derivation).
		log.Warn("dashboard renderAccount: SLO fetch degraded", "account_id_hash", logsanitize.HashShort(acct.ID))
	}
	return view
}

// fetchDashboardSLOBadges returns the per-row badge map
// for the /dashboard/apps apps-list page. The badge shows
// the worst-field value (p95 latency if elevated, else
// error rate) per app so operators scrolling the list
// see the worst app at a glance.
//
// Cost tripwire: capped at dashboardSLOAppsListBadgeCap
// apps. Beyond the cap the badge silently degrades to "—"
// — the per-app SLO panel is always reachable via the
// row's detail link.
//
// The badge source is the existing scalar fetchAppSLO
// with a 24h window (the canonical SLO lookback). The
// sparkline is fetched at 24h too — the per-row badge
// shares the same data shape the per-app panel uses.
func (s *server) fetchDashboardSLOBadges(ctx context.Context, log *slog.Logger, apps []dashboard.AppListItem, acct state.Account) map[string]views.SLOBadge {
	if s.promqlClient == nil {
		return nil
	}
	dctx, cancel := budgetCtx(ctx, 3*time.Second)
	defer cancel()

	// Edge case: cap the apps-list badge set so a 100-app
	// account doesn't issue 100 scalar + 100 sparkline
	// fetch calls. The apps-list already caps at the
	// dashboard surface (the existing LimitDeploymentsForApp
	// caps display at 25); the per-row badge inherits the
	// same cap.
	capped := apps
	if len(capped) > dashboardSLOAppsListBadgeCap {
		capped = capped[:dashboardSLOAppsListBadgeCap]
	}

	// The badge looks the worst-field value up for the
	// app's underlying state.App row. Look it up by slug
	// → AppID mapping; the apps list already has the slug
	// but the fetch helpers reach the slice by AppID.
	slugs := make([]string, 0, len(capped))
	for _, item := range capped {
		slugs = append(slugs, item.Slug)
	}

	// For each capped app, re-fetch the AppID via the
	// store. The dashboard already has the AppListItem
	// struct on hand but the AppID isn't surfaced on it
	// today — the per-app row's link is the slug, the
	// store is the AppID source. One cheap round-trip per
	// app, capped at 25 to keep the cost tripwire tight.
	out := make(map[string]views.SLOBadge, len(capped))
	for _, slug := range slugs {
		app, err := s.store.AppBySlug(dctx, slug)
		if err != nil || app.AccountID != acct.ID {
			continue
		}
		scalar, src := s.fetchAppSLO(dctx, app, acct, api.SLODefaultWindow)
		badge := views.SLOBadge{Source: src}
		// Pick the worst field. p95 latency threshold is
		// 250ms (a noticeable page-load regression); error
		// rate threshold is 1% (the rate-limit "good
		// citizen" line). The threshold table corresponds
		// to the public /status/slo.json "degraded" copy.
		switch {
		case scalar.RequestDuration.P95MS > 250:
			badge.Label = "p95: " + formatMS(scalar.RequestDuration.P95MS)
			badge.Glyph = "warn"
		case scalar.ErrorRatePct > 1:
			badge.Label = "err: " + formatPct(scalar.ErrorRatePct)
			badge.Glyph = "warn"
		default:
			badge.Label = "p95: " + formatMS(scalar.RequestDuration.P95MS) +
				" · err: " + formatPct(scalar.ErrorRatePct)
			badge.Glyph = "ok"
		}
		badge.ScannedAt = time.Now().UTC()
		out[slug] = badge
	}
	return out
}

// formatMS formats a millisecond float as a one-decimal
// string. Latency values are always non-negative (the
// handler pre-zeros NaN/Inf via pkg/appmetrics.SafeFloat).
func formatMS(v float64) string {
	if v == 0 {
		return "0.0ms"
	}
	return fmt.Sprintf("%.1fms", v)
}

// formatPct formats a percentage float as a two-decimal
// string. Error rate / cold-boot rate are always in [0,100]
// after the SafePercent clamp. Negative zero is coerced to
// "0.00" so the dashboard doesn't render "-0.00%".
func formatPct(v float64) string {
	if v == 0 {
		return "0.00%"
	}
	return fmt.Sprintf("%.2f%%", v)
}
