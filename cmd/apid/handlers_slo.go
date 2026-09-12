package main

// Per-app and account-scoped SLO handlers (issue #696 / ADR-082).
//
// GET /v1/apps/{slug}/slo?window=24h
// GET /v1/account/slo?window=24h
//
// These are the customer-facing SLO surface — a closed-set
// windowed panel (1h | 24h | 7d) that mirrors the AWS
// CloudWatch per-function / GCP Cloud Run per-service shape.
// Distinct from the existing /v1/apps/{slug}/metrics and
// /v1/apps/metrics endpoints (issue #273 / ADR-042, issue
// #393), which are the 5m-window dashboard panel. The /slo
// surface is the "yesterday's SLO" / "this week's SLO"
// summary, with the customer-facing SLO signals co-located
// with the billing-derivable instance_hours / gb_hours
// fields.
//
// Window vocabulary is a closed subset of
// pkg/appmetrics.Ranges(): {1h, 24h, 7d}. Anything else
// fires 400 CodeValidation. Default window is 24h (the
// canonical SLO lookback).
//
// Auth chain asymmetry (per user decision):
//   - /v1/apps/{slug}/slo: authLimited + requireScope(ScopesReadSurface)
//     NO MFA. Mirrors /v1/apps/{slug}/metrics. Primary caller is an API key.
//   - /v1/account/slo: authLimited + requireMFA + requireScope(ScopesUsageReadSurface).
//     MFA-gated. Mirrors /v1/usage/summary. The flat account rollup
//     includes billing-derivable fields, so the usage:read precedent
//     is the right fit.
//
// IDOR safety: the per-app handler calls s.loadApp which returns
// 404 (never 403) on a cross-account slug lookup, same as
// getAppMetrics at handlers_metrics.go:38.
//
// Source contract: "degraded: <reason>" when Prometheus is
// unreachable (the per-app and account DTOs zero every numeric
// field in this case). When the PromQL pass succeeds but the
// Postgres usage-minutes rollup fails, only instance_hours /
// gb_hours are zeroed and Source is "degraded: postgres
// unavailable" so the latency/error/cold-boot numbers stay
// non-zero. This pattern mirrors
// pkg/appmetrics.SourceDegradedPrefix / degradedFromErr.
//
// No new Prometheus metrics are introduced — every field is
// already emitted:
//   - gateway_request_duration_seconds_bucket{app,class="2xx"}
//   - gateway_requests_total{app}
//   - gateway_cold_boot_total{app}
//   - gateway_rate_limited_total{app, plan}
//
// And no new sqlc queries — the usage_minutes rollup is a
// direct SELECT against the existing table.

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/state"
)

// getAppSLO serves GET /v1/apps/{slug}/slo?window=.
// Mirrors getAppMetrics's auth chain (no MFA, read-only,
// primary caller is an API key with ScopesReadSurface).
// IDOR-safe via loadApp — cross-account slug → 404.
func (s *server) getAppSLO(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug")) //nolint:contextcheck // loadApp takes r and uses r.Context() for its own DB calls; the helper is shared across every per-app handler.
	if !ok {
		return
	}

	window := r.URL.Query().Get("window")
	if window == "" {
		window = api.SLODefaultWindow
	}
	if !api.IsValidSLORange(window) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"invalid window",
			fmt.Sprintf("window must be one of: %s", strings.Join(api.SLORanges(), ", "))))
		return
	}

	resp, src := s.fetchAppSLO(r.Context(), app, acct, window)
	resp.AppID = app.ID
	resp.AppSlug = app.Slug
	resp.Window = window
	resp.Source = src
	resp.AsOf = time.Now().UTC().Format(time.RFC3339Nano)
	writeJSON(w, http.StatusOK, resp)
}

// getAccountSLO serves GET /v1/account/slo?window=.
// Account-scoped rollup (no slug path). Cross-account isolation
// is the SQL JOIN on apps.account_id=$1 in the pgstore helper
// — there's no (accountID, slug) pair to IDOR-check.
func (s *server) getAccountSLO(w http.ResponseWriter, r *http.Request, acct state.Account) {
	window := r.URL.Query().Get("window")
	if window == "" {
		window = api.SLODefaultWindow
	}
	if !api.IsValidSLORange(window) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"invalid window",
			fmt.Sprintf("window must be one of: %s", strings.Join(api.SLORanges(), ", "))))
		return
	}

	resp, src := s.fetchAccountSLO(r.Context(), acct, window)
	resp.Window = window
	resp.Source = src
	resp.AsOf = time.Now().UTC().Format(time.RFC3339Nano)
	writeJSON(w, http.StatusOK, resp)
}

// fetchAppSLO runs the per-app PromQL pipeline + the
// usage_minutes rollup. Returns the response and a Source
// string ("prometheus" on success, "degraded: <reason>" on
// failure). Safe when fetcher is nil — every Prometheus
// field is zeroed and the source carries the reason.
func (s *server) fetchAppSLO(ctx context.Context, app state.App, acct state.Account, window string) (api.AppSLOResponse, string) {
	resp := api.AppSLOResponse{}
	if s.promqlClient == nil {
		// Try the Postgres rollup regardless — the dashboard
		// benefits from showing instance_hours even when
		// Prometheus is down. The partial-population path is
		// the right UX (Source has two distinguishable
		// "degraded:" reasons).
		if s.store != nil {
			start, end := windowToRange(window)
			instH, gbH, err := s.store.UsageSLOForApp(ctx, app.ID, acct.ID, start, end)
			if err == nil {
				resp.InstanceHours = instH
				resp.GBHours = gbH
				return resp, appmetrics.SourceDegradedPrefix + "prometheus not configured (usage fields populated)"
			}
		}
		return resp, appmetrics.SourceDegradedPrefix + "prometheus not configured"
	}

	// 1. requests_total.
	reqCountQ := fmt.Sprintf(`sum(increase(gateway_request_duration_seconds_count{app=%q}[%s]))`, app.ID, window)
	if v, err := s.promqlClient.QueryScalar(ctx, reqCountQ); err == nil {
		resp.RequestsTotal = int64(appmetrics.SafeRoundNonNeg(v))
	} else {
		return degradedAppSLO(err, s.log, "requests_total", app.ID, window)
	}

	// 2-4. latency percentiles (2xx class only).
	for _, p := range []struct {
		q     float64
		dest  *float64
		label string
	}{
		{q: 0.50, dest: &resp.RequestDuration.P50MS, label: "p50"},
		{q: 0.95, dest: &resp.RequestDuration.P95MS, label: "p95"},
		{q: 0.99, dest: &resp.RequestDuration.P99MS, label: "p99"},
	} {
		q := appmetrics.HistogramQuantileMSQuery(
			p.q,
			fmt.Sprintf(`sum by (le)(rate(gateway_request_duration_seconds_bucket{app=%q,class="2xx"}[%s]))`, app.ID, window),
			fmt.Sprintf(`sum(rate(gateway_request_duration_seconds_count{app=%q,class="2xx"}[%s]))`, app.ID, window))
		v, err := s.promqlClient.QueryScalar(ctx, q)
		if err != nil {
			return degradedAppSLO(err, s.log, p.label, app.ID, window)
		}
		*p.dest = appmetrics.SafeFloat(v)
	}

	// 5. error_rate_pct.
	errQ := appmetrics.PercentRatioQuery(
		fmt.Sprintf(`sum(rate(gateway_request_duration_seconds_count{app=%q,class=~"[45]xx"}[%s]))`, app.ID, window),
		fmt.Sprintf(`sum(rate(gateway_request_duration_seconds_count{app=%q}[%s]))`, app.ID, window))
	if v, err := s.promqlClient.QueryScalar(ctx, errQ); err == nil {
		resp.ErrorRatePct = appmetrics.SafePercent(v)
	} else {
		return degradedAppSLO(err, s.log, "error_rate", app.ID, window)
	}

	// 6. cold_boot_rate_pct.
	coldQ := appmetrics.PercentRatioQuery(
		fmt.Sprintf(`sum(rate(gateway_cold_boot_total{app=%q}[%s]))`, app.ID, window),
		fmt.Sprintf(`sum(rate(gateway_request_duration_seconds_count{app=%q}[%s]))`, app.ID, window))
	if v, err := s.promqlClient.QueryScalar(ctx, coldQ); err == nil {
		resp.ColdBootRatePct = appmetrics.SafePercent(v)
	} else {
		return degradedAppSLO(err, s.log, "cold_boot", app.ID, window)
	}

	// wake_queue_p95 remains zero because its source histogram has no app
	// label. Returning the fleet value here would leak other tenants' load.

	// 8. throttled_total — vector query (gateway_rate_limited_total
	// is labelled {app, plan}; QueryMap collapses two label
	// dimensions into one per-app key).
	thrQ := fmt.Sprintf(`sum by (app)(increase(gateway_rate_limited_total{app=%q}[%s]))`, app.ID, window)
	thrByApp, err := s.promqlClient.QueryMap(ctx, thrQ)
	if err != nil {
		return degradedAppSLO(err, s.log, "throttled_total", app.ID, window)
	}
	resp.ThrottledTotal = int64(appmetrics.SafeRoundNonNeg(thrByApp[app.ID]))

	// 9. usage_minutes rollup (instance_hours / gb_hours).
	// Postgres hiccup here does NOT flip the whole response to
	// degraded — the PromQL fields are still useful. Only the
	// two billing fields stay zeroed and Source flips to the
	// partial-degraded form.
	if s.store != nil {
		start, end := windowToRange(window)
		instH, gbH, err := s.store.UsageSLOForApp(ctx, app.ID, acct.ID, start, end)
		if err == nil {
			resp.InstanceHours = instH
			resp.GBHours = gbH
		} else {
			s.log.Warn("handlers_slo: usage_minutes rollup failed", "app_id", app.ID, "err", err.Error())
			return resp, appmetrics.SourceDegradedPrefix + "postgres unavailable"
		}
	}

	return resp, appmetrics.SourcePrometheus
}

// fetchAccountSLO is the account-scoped rollup. The account is resolved to
// its app IDs before any Prometheus query, then every selector uses that
// closed set. The Postgres rollup is scoped by account ID as before.
func (s *server) fetchAccountSLO(ctx context.Context, acct state.Account, window string) (api.AccountSLOResponse, string) {
	resp := api.AccountSLOResponse{}
	if s.promqlClient == nil {
		// Best-effort: still try the Postgres rollup.
		if s.store != nil {
			start, end := windowToRange(window)
			instH, gbH, err := s.store.UsageSLOForAccount(ctx, acct.ID, start, end)
			if err == nil {
				resp.InstanceHours = instH
				resp.GBHours = gbH
				return resp, appmetrics.SourceDegradedPrefix + "prometheus not configured (usage fields populated)"
			}
		}
		return resp, appmetrics.SourceDegradedPrefix + "prometheus not configured"
	}
	if s.store == nil {
		return resp, appmetrics.SourceDegradedPrefix + "account app scope unavailable"
	}
	apps, err := s.store.ListApps(ctx, acct.ID)
	if err != nil {
		return resp, appmetrics.SourceDegradedPrefix + "account app scope unavailable"
	}
	appIDs := make([]string, 0, len(apps))
	for _, app := range apps {
		appIDs = append(appIDs, app.ID)
	}
	if len(appIDs) == 0 {
		start, end := windowToRange(window)
		if instH, gbH, usageErr := s.store.UsageSLOForAccount(ctx, acct.ID, start, end); usageErr == nil {
			resp.InstanceHours, resp.GBHours = instH, gbH
		}
		return resp, appmetrics.SourcePrometheus
	}
	appMatcher := appmetrics.AppIDMatcher(appIDs)

	// 1. requests_total.
	reqCountQ := fmt.Sprintf(`sum(increase(gateway_requests_total{%s}[%s]))`, appMatcher, window)
	if v, err := s.promqlClient.QueryScalar(ctx, reqCountQ); err == nil {
		resp.RequestsTotal = int64(appmetrics.SafeRoundNonNeg(v))
	} else {
		return degradedAccountSLO(err, s.log, "requests_total", acct.ID, window)
	}

	// 2-4. latency percentiles (2xx class).
	for _, p := range []struct {
		q     float64
		dest  *float64
		label string
	}{
		{q: 0.50, dest: &resp.RequestDuration.P50MS, label: "p50"},
		{q: 0.95, dest: &resp.RequestDuration.P95MS, label: "p95"},
		{q: 0.99, dest: &resp.RequestDuration.P99MS, label: "p99"},
	} {
		q := appmetrics.HistogramQuantileMSQuery(
			p.q,
			fmt.Sprintf(`sum by (le)(rate(gateway_request_duration_seconds_bucket{%s,class="2xx"}[%s]))`, appMatcher, window),
			fmt.Sprintf(`sum(rate(gateway_request_duration_seconds_count{%s,class="2xx"}[%s]))`, appMatcher, window))
		v, err := s.promqlClient.QueryScalar(ctx, q)
		if err != nil {
			return degradedAccountSLO(err, s.log, p.label, acct.ID, window)
		}
		*p.dest = appmetrics.SafeFloat(v)
	}

	// 5. error_rate_pct.
	errQ := appmetrics.PercentRatioQuery(
		fmt.Sprintf(`sum(rate(gateway_requests_total{%s,code=~"[45].."}[%s]))`, appMatcher, window),
		fmt.Sprintf(`sum(rate(gateway_requests_total{%s}[%s]))`, appMatcher, window))
	if v, err := s.promqlClient.QueryScalar(ctx, errQ); err == nil {
		resp.ErrorRatePct = appmetrics.SafePercent(v)
	} else {
		return degradedAccountSLO(err, s.log, "error_rate", acct.ID, window)
	}

	// 6. cold_boot_rate_pct.
	coldQ := appmetrics.PercentRatioQuery(
		fmt.Sprintf(`sum(rate(gateway_cold_boot_total{%s}[%s]))`, appMatcher, window),
		fmt.Sprintf(`sum(rate(gateway_requests_total{%s}[%s]))`, appMatcher, window))
	if v, err := s.promqlClient.QueryScalar(ctx, coldQ); err == nil {
		resp.ColdBootRatePct = appmetrics.SafePercent(v)
	} else {
		return degradedAccountSLO(err, s.log, "cold_boot", acct.ID, window)
	}

	// wake_queue_p95 remains zero because its source histogram has no app or
	// account label and therefore cannot be exposed as a tenant projection.

	// 8. throttled_total. An absent rate-limit counter is a
	// healthy zero, not a missing SLO panel. Keep the fallback in PromQL so
	// QueryScalar receives a finite sample when no throttling series exists.
	thrQ := fmt.Sprintf(`sum(increase(gateway_rate_limited_total{%s}[%s])) or vector(0)`, appMatcher, window)
	if v, err := s.promqlClient.QueryScalar(ctx, thrQ); err == nil {
		resp.ThrottledTotal = int64(appmetrics.SafeRoundNonNeg(v))
	} else {
		// This is an optional component. Preserve the request and latency
		// fields collected above so a real Prometheus failure is visibly
		// partial degradation instead of an all-zero account panel.
		return degradedAccountSLOPartial(resp, err, s.log, "throttled_total", acct.ID, window)
	}

	// 9. usage_minutes rollup (instance_hours / gb_hours).
	if s.store != nil {
		start, end := windowToRange(window)
		instH, gbH, err := s.store.UsageSLOForAccount(ctx, acct.ID, start, end)
		if err == nil {
			resp.InstanceHours = instH
			resp.GBHours = gbH
		} else {
			s.log.Warn("handlers_slo: usage_minutes rollup failed", "account_id", acct.ID, "err", err.Error())
			return resp, appmetrics.SourceDegradedPrefix + "postgres unavailable"
		}
	}

	return resp, appmetrics.SourcePrometheus
}

// windowToRange converts a closed-set SLO window token
// ("1h" | "24h" | "7d") to a [start, end] time pair anchored
// at now() in UTC. The pgstore helper takes the half-open
// range [start, end) so the caller can index the
// usage_minutes.minute column directly.
func windowToRange(window string) (time.Time, time.Time) {
	end := time.Now().UTC()
	var start time.Time
	switch window {
	case "1h":
		start = end.Add(-1 * time.Hour)
	case "24h":
		start = end.Add(-24 * time.Hour)
	case "7d":
		start = end.Add(-7 * 24 * time.Hour)
	default:
		// Defensive: the handler validated this is in the
		// closed set, so default is unreachable. Fall back
		// to 24h to keep the Postgres query bounded.
		start = end.Add(-24 * time.Hour)
	}
	return start, end
}

// degradedAppSLO returns a zeroed per-app SLO with a
// "degraded: <reason>" Source. CR/LF sanitiser is inline at
// the call site (NOT in a helper) so the CodeQL go/log-
// injection dataflow path is unambiguous — same pattern the
// pkg/appmetrics package doc references (alert #117).
func degradedAppSLO(err error, log *slog.Logger, label, appID, window string) (api.AppSLOResponse, string) {
	msg := strings.ReplaceAll(err.Error(), "\r", "")
	msg = strings.ReplaceAll(msg, "\n", "")
	if log != nil {
		log.Warn("handlers_slo: query failed", "label", label, "app_id", appID, "window", window, "err", msg)
	}
	return api.AppSLOResponse{}, appmetrics.SourceDegradedPrefix + msg
}

// degradedAccountSLO mirrors degradedAppSLO for the
// account-scoped surface. Same CodeQL-safe sanitiser
// pattern.
func degradedAccountSLO(err error, log *slog.Logger, label, accountID, window string) (api.AccountSLOResponse, string) {
	return degradedAccountSLOPartial(api.AccountSLOResponse{}, err, log, label, accountID, window)
}

func degradedAccountSLOPartial(resp api.AccountSLOResponse, err error, log *slog.Logger, label, accountID, window string) (api.AccountSLOResponse, string) {
	msg := strings.ReplaceAll(err.Error(), "\r", "")
	msg = strings.ReplaceAll(msg, "\n", "")
	if log != nil {
		log.Warn("handlers_slo: query failed", "label", label, "account_id", accountID, "window", window, "err", msg)
	}
	return resp, appmetrics.SourceDegradedPrefix + msg
}
