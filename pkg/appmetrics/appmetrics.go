// Package appmetrics extracts the per-app Prometheus fetch used by
// cmd/apid/handlers_metrics.go (issue #273 / ADR-042) and the
// dashboard's renderAppDetail so meterd (issue #396 / ADR-045) can
// call the same implementation from cmd/meterd/... in PR 4. Zero
// behaviour change for the apid caller; the public surface is the
// function signatures and the closed-set range vocabulary.
//
// The seven PromQL builders + percentile loop + degraded-source
// helpers were lifted verbatim from cmd/apid/handlers_metrics.go.
// The CodeQL go/log-injection sanitiser pattern (two-call
// strings.ReplaceAll for CR then LF, inline at the log call site)
// is preserved per the precedent at handlers_metrics.go:189-193
// (alert #117) — the dataflow path stays unambiguous to CodeQL.
package appmetrics

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

// SourcePrometheus is the canonical "Source" value emitted on a
// healthy Prometheus response. The three production emitters
// (handlers_metrics, handlers_dashboard, status) plus the future
// meterd caller all compare against this constant — goconst catches
// drift if a future rename happens in only one place.
const SourcePrometheus = "prometheus"

// SourceDegradedPrefix is the prefix every degraded response carries.
// The dashboard and the public /status/slo.json both render the
// "degraded:" branch off this prefix.
const SourceDegradedPrefix = "degraded: "

// SourceDegraded is the bare "degraded:" prefix WITHOUT the trailing
// space, exported so alert evaluators and other callers that gate on
// "is this a degraded source?" can compare against the canonical
// prefix without slicing SourceDegradedPrefix[:len(...)-1] in their
// hot path. Pair with IsDegradedSource for the idiomatic check.
const SourceDegraded = "degraded:"

// IsDegradedSource returns true iff source has the "degraded: "
// prefix. The dashboard's empty-state branch, the public
// /status/slo.json renderer, and the alert evaluator (issue #396 /
// ADR-045 PR 4) all share this check; centralising it here means a
// future change to the prefix (e.g. swapping ":" for ";") only
// touches one site.
func IsDegradedSource(source string) bool {
	return strings.HasPrefix(source, SourceDegradedPrefix)
}

// DefaultRange is the range the server applies when the caller passes
// no range. Matches the §12 status-page window so customers don't see
// two different "current" periods on the same dashboard.
const DefaultRange = "5m"

// metricsRanges is the closed vocabulary for the range argument.
// Bounded by Prometheus retention (`prom_retention_days: 15` in
// deploy/ansible/roles/prometheus/defaults/main.yml). 5m is the
// default the server applies when the client omits the param.
var metricsRanges = []string{"5m", "15m", "1h", "6h", "24h", "7d", "15d"}

// PromQL is the minimal interface Fetch needs. pkg/promql.Client
// satisfies it; tests pass a stub. Mirrors the testable surface
// pkg/promql exposes (HTTPDoer) so the seam is one method, not two.
type PromQL interface {
	QueryScalar(ctx context.Context, query string) (float64, error)
}

// Fetch runs the per-app PromQL queries and assembles an
// AppMetricsResponse. Returns the response and a Source string
// ("prometheus" on success, "degraded: <reason>" on failure). Safe
// when fetcher is nil — every field is zeroed and the source is
// "degraded: prometheus not configured".
//
// log is the destination for per-query failure warnings. nil falls
// back to slog.Default() so callers that wire a no-log setup don't
// have to special-case the zero value (mirrors scheddgrpc/server.go
// nil coercion).
//
// The percentile window (5m by default) matches the public
// /status/slo.json. The error-rate and cold-start windows match the
// same window. The wake p95 is FLEET (gateway_wake_latency_seconds
// is unlabeled) and labelled as such in the UI.
func Fetch(ctx context.Context, fetcher PromQL, log *slog.Logger, appID, rng string) (api.AppMetricsResponse, string) {
	if log == nil {
		log = slog.Default()
	}
	resp := api.AppMetricsResponse{}
	// Nil-client short-circuit. Two cases fold into one response:
	//   1. fetcher is the zero interface value (caller passed literal nil).
	//   2. fetcher wraps a typed-nil *promql.Client (s.promqlClient is
	//      nil but typed as the concrete pointer). Without the type-
	//      switch below, QueryScalar would dispatch into the nil
	//      receiver and return "promql: client not configured" — a
	//      leaky-abstraction error message that the handler test
	//      TestAppMetrics_PrometheusDisabled pins against the
	//      canonical "prometheus not configured" form. We type-switch
	//      on the canonical implementer (the test stub doesn't satisfy
	//      *promql.Client and falls through to the dispatch path).
	if fetcher == nil {
		return resp, SourceDegradedPrefix + "prometheus not configured"
	}
	if c, ok := fetcher.(*promql.Client); ok && c == nil {
		return resp, SourceDegradedPrefix + "prometheus not configured"
	}
	// Reject appID values that would let a caller escape the outer
	// label literal in the PromQL query. Today every production
	// caller passes a UUID-shaped app.ID (server-controlled), but
	// PR 4's meterd caller will pass alert_rules.app_id — a slug the
	// customer supplies. Without this guard a crafted slug containing
	// `"` would close the outer label prematurely and re-open a new
	// `app=…` selector, leaking data across apps. The error path
	// returns a degraded response so the dashboard's empty-state
	// branch handles it.
	if strings.ContainsAny(appID, "\"\n\\") {
		return resp, SourceDegradedPrefix + "invalid app id"
	}

	// 1. Request count.
	countQ := fmt.Sprintf(`sum(increase(gateway_requests_total{app=%q}[%s]))`, appID, rng)
	if v, err := fetcher.QueryScalar(ctx, countQ); err == nil {
		resp.RequestCount = int64(SafeRoundNonNeg(v))
	} else {
		return degradedFromErr(resp, err, log, "request_count")
	}

	// 2-4. P50 / P95 / P99 over 2xx class only. histogram_quantile
	// returns NaN on an empty window — SafeFloat coerces to 0.
	for _, p := range []struct {
		q     float64
		dest  *float64
		label string
	}{
		{q: 0.50, dest: &resp.LatencyP50MS, label: "p50"},
		{q: 0.95, dest: &resp.LatencyP95MS, label: "p95"},
		{q: 0.99, dest: &resp.LatencyP99MS, label: "p99"},
	} {
		q := fmt.Sprintf(
			`histogram_quantile(%g, sum by (le) (rate(gateway_request_duration_seconds_bucket{app=%q,class="2xx"}[%s]))) * 1000`,
			p.q, appID, rng)
		v, err := fetcher.QueryScalar(ctx, q)
		if err != nil {
			return degradedFromErr(resp, err, log, p.label)
		}
		*p.dest = SafeFloat(v)
	}

	// 5. Error rate %.
	errQ := fmt.Sprintf(
		`sum(rate(gateway_requests_total{app=%q,code=~"[45].."}[%s])) / sum(rate(gateway_requests_total{app=%q}[%s])) * 100`,
		appID, rng, appID, rng)
	if v, err := fetcher.QueryScalar(ctx, errQ); err == nil {
		resp.ErrorRatePct = SafePercent(v)
	} else {
		return degradedFromErr(resp, err, log, "error_rate")
	}

	// 6. Cold start %.
	coldQ := fmt.Sprintf(
		`sum(rate(gateway_cold_boot_total{app=%q}[%s])) / sum(rate(gateway_requests_total{app=%q}[%s])) * 100`,
		appID, rng, appID, rng)
	if v, err := fetcher.QueryScalar(ctx, coldQ); err == nil {
		resp.ColdStartPct = SafePercent(v)
	} else {
		return degradedFromErr(resp, err, log, "cold_start")
	}

	// 7. Fleet wake p95 (the unlabeled gateway_wake_latency_seconds).
	wakeQ := fmt.Sprintf(
		`histogram_quantile(0.95, sum by (le) (rate(gateway_wake_latency_seconds_bucket[%s]))) * 1000`, rng)
	if v, err := fetcher.QueryScalar(ctx, wakeQ); err == nil {
		resp.WakeP95MS = SafeFloat(v)
	} else {
		return degradedFromErr(resp, err, log, "wake_p95")
	}

	// 7b. Issue #1233 / ADR-123 — per-app wake queue depth
	// (gateway_queue_depth{app}). The alert preset
	// queue_backlog_growing fires when this exceeds the threshold
	// over the window; the public metrics endpoint surfaces it
	// for dashboard parity. Querying the latest sample (not a
	// rate) since the gauge reflects current waiters, not a
	// delta — the alert path's windowed comparison uses the same
	// query so the gauge is the live value, not an average.
	qDepthQ := fmt.Sprintf(`gateway_queue_depth{app=%q, account_id=~".+"}`, appID)
	if v, err := fetcher.QueryScalar(ctx, qDepthQ); err == nil {
		resp.QueueDepth = int64(SafeRoundNonNeg(v))
	} else {
		// Best-effort: a Prometheus failure here degrades the
		// queue_depth field but does NOT flip the whole response
		// to degraded — the rest of the per-app panel is still
		// useful, mirroring the egress_bytes pattern below.
		resp.QueueDepth = 0
	}

	// 8. ADR-046 (step 10): per-app egress byte delta over
	// the window. Source: the schedd Prom rollup of
	// usage_minutes.net_tx_bytes (PR-2 wires the rollup;
	// until then the query returns 0 / errors and the
	// response degrades silently — the dashboard's
	// empty-state branch handles it). Best-effort:
	// unlike the seven core queries above, a Prometheus
	// failure here does NOT flip the response to
	// degraded — the rest of the per-app panel is still
	// useful, and the egress field's "not measured"
	// state is the correct UX (matches how the CPU
	// `instance_cpu_seconds_total` mirrors the same
	// additive-merge shape).
	egressQ := fmt.Sprintf(
		`sum(increase(schedd_egress_net_tx_bytes_total{app=%q}[%s]))`, appID, rng)
	if v, err := fetcher.QueryScalar(ctx, egressQ); err == nil {
		resp.EgressBytes = int64(SafeRoundNonNeg(v))
	} else {
		log.Warn("appmetrics: egress_bytes query failed", "app_id", appID, "err", err)
	}

	// 8b. ADR-046 PR-2 / issue #415 PR-2: gateway-side
	// tx_bytes mirror. Source:
	//
	//   gateway_egress_tx_bytes_total{app}
	//
	// The byte counter is emitted by pkg/gateway/egressgrpc
	// /server.go per-drain on every Sink.Record call; the
	// cmd/gatewayd-internal/egress_grpc.go consumer reads
	// the stream and the per-instance
	// pkg/gateway/egresssink.EgressSink populates the
	// counter on each raw-stream chunk. Mirrors EgressBytes
	// (the schedd-side mirror); the two are queried in
	// parallel so a divergence surfaces to the dashboard
	// immediately.
	//
	// Best-effort: same as the schedd-side query. A failure
	// here does NOT flip the response to "degraded" — the
	// rest of the panel is still useful, and the missing
	// tx_bytes field is the correct UX (matches how the
	// CPU/RSS/memory deltas degrade when their respective
	// Prom sources are down).
	txBytesQ := fmt.Sprintf(
		`sum(increase(gateway_egress_tx_bytes_total{app=%q}[%s]))`, appID, rng)
	if v, err := fetcher.QueryScalar(ctx, txBytesQ); err == nil {
		resp.TxBytes = int64(SafeRoundNonNeg(v))
	} else {
		log.Warn("appmetrics: tx_bytes query failed", "app_id", appID, "err", err)
	}

	return resp, SourcePrometheus
}

// degradedFromErr returns the zeroed response with a
// "degraded: <err>" Source. Logs the failure so operators can tell
// which query failed (the dashboard shows the generic message; the
// server log has the detail).
//
// CodeQL go/log-injection (alert #117): the err string is
// user-controllable (the PromQL `range=` query param flows into the
// query body that produced the error). CodeQL's sanitiser model
// only recognises the two-call pattern below — see
// memory/codeql-go-log-injection-sanitisers.md for the full
// precedent. The CR/LF strip is inline at the call site (NOT inside
// a helper) so the dataflow path is unambiguous to CodeQL. The
// SAME sanitised string flows into the Source field on the wire —
// a misbehaving Prometheus returning \r\n in its error body would
// otherwise land raw in the JSON response, breaking structured-log
// parsing downstream.
func degradedFromErr(resp api.AppMetricsResponse, err error, log *slog.Logger, label string) (api.AppMetricsResponse, string) {
	msg := strings.ReplaceAll(err.Error(), "\r", "")
	msg = strings.ReplaceAll(msg, "\n", "")
	if log != nil {
		log.Warn("appmetrics: query failed", "label", label, "err", msg)
	}
	// Fall back to zeroed fields rather than partially-populated
	// numbers — the dashboard's empty-state message depends on
	// RequestCount being 0 when degraded.
	return api.AppMetricsResponse{}, SourceDegradedPrefix + msg
}

// Ranges returns a copy of the closed-set vocabulary for the range
// argument. A copy is returned so callers cannot mutate the package
// state (mirrors the pkg/oci counter-labels accessor pattern).
func Ranges() []string {
	out := make([]string, len(metricsRanges))
	copy(out, metricsRanges)
	return out
}

// IsValidRange returns true iff rng is in the closed set returned
// by Ranges(). The HTTP handler validates ?range= via this helper
// so the dashboard and any future caller share the same vocabulary.
func IsValidRange(rng string) bool {
	for _, r := range metricsRanges {
		if r == rng {
			return true
		}
	}
	return false
}

// SafeFloat coerces NaN/Inf to 0 and clamps negative values to 0.
// histogram_quantile on an empty window returns NaN; a custom
// histogram with negative bucket observations (impossible here but
// defensive) would return a negative result.
func SafeFloat(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0
	}
	return v
}

// SafePercent is SafeFloat plus a [0,100] clamp. A division-by-zero
// fallback yields 0 from promql; NaN from histogram_quantile yields 0
// from SafeFloat; over-100 from arithmetic wrap-around is clamped here.
func SafePercent(v float64) float64 {
	x := SafeFloat(v)
	if x > 100 {
		x = 100
	}
	return x
}

// SafeRoundNonNeg is SafeFloat under a name that documents intent:
// used for `request_count` which is integral on the wire but comes
// back as a float from promql (increase() returns a float). The
// call site does the int-conversion (int64(...)) so the rounding
// policy (currently a float-truncating cast) lives at the call
// site; keeping this as its own helper means a future change to
// banker's rounding has one site to touch. Issue #273 / ADR-042.
func SafeRoundNonNeg(v float64) float64 {
	return SafeFloat(v)
}
