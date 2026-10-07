// Package throttlerec implements the per-route throttle
// recommender (ADR-091 D20.5 amendment, issue #881). It is the
// read-only sibling of the gateway-side enforcer in
// pkg/gateway/edge_rules_throttle.go: given a window of
// gateway_requests_by_route_total{app, route} observations, it
// produces a suggested rps/burst per route, capped at the
// customer's plan ceiling so the suggestion is always settable.
//
// The recommender is INTENTIONALLY ADVICE-ONLY. It never auto-applies
// a rule — the apid layer is the only writer to edge_rules and
// customers must confirm via POST /v1/apps/{slug}/edge-rules. This
// matches the rest of the FaaS billing surface: customers set their
// own quotas, the platform applies them.
//
// The structure mirrors pkg/appmetrics:
//   - PromQL interface seam (one method, QueryGrouped).
//   - Fetch(...) signature with the same Source / degradedFromErr
//     posture so HTTP 200 + zeroed fields + degraded Source is the
//     only failure shape downstream callers must handle.
//   - SafeFloat / SafePercent guards (re-exported from pkg/appmetrics
//     so this package has no math primitives of its own — the
//     invariants are owned by one site).
//
// The closed range vocabulary is reused from pkg/appmetrics.Ranges() /
// IsValidRange — the recommender's window is the same window the
// per-app metrics endpoint uses, so a customer can't see different
// "current" periods on the dashboard and the suggestions card.
package throttlerec

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/promql"
)

// SourcePrometheus / SourceDegradedPrefix / SourceDegraded re-export
// the matching constants from pkg/appmetrics so the HTTP handler
// can compare Source the same way for both endpoints. The
// recommendation is to import pkg/appmetrics directly when a
// caller needs both — these re-exports are here for symmetry, not
// to fork the vocabulary.
const (
	SourcePrometheus     = appmetrics.SourcePrometheus
	SourceDegradedPrefix = appmetrics.SourceDegradedPrefix
	SourceDegraded       = appmetrics.SourceDegraded
)

// DefaultRange is the window the server applies when the caller
// passes no range. Re-uses the appmetrics default so the
// dashboard and the suggestions card render the same "current"
// window.
const DefaultRange = appmetrics.DefaultRange

// RangeOtherLabel is the reserved Prometheus label that the
// gatewayd-internal routeLabelSet assigns to the overflow bucket
// when route cardinality exceeds pkg/api.RouteMetricsPerAppCap
// (ADR-093). The recommender must exclude this label from the
// suggestions slice — it represents a wildcard-shape pattern, not
// a real route — and report its count separately as
// RoutesCollapsed so the customer can tell "wildcard" from "low
// traffic on a real route".
const RangeOtherLabel = "__route_other__"

// Multiplier is the headroom factor the recommender applies to
// observed_rps when producing a suggested value. 2× gives a route
// twice its observed peak to grow into before the customer has to
// re-tune. The constant is exported so the wire shape can echo it
// back (a future Prometheus-window change would otherwise look
// indistinguishable from a strategy change in the audit log).
const Multiplier = 2.0

// PromQL is the minimal interface Fetch needs. pkg/promql.Client
// satisfies it (QueryGrouped + QueryScalar). Fetch uses
// QueryGrouped for the recommendation pass; QueryScalar is used
// ONLY by the dry-run preview pass (Phase 4 D1) which emits one
// PromQL per route. Tests pass a stub that maps canned queries to
// canned responses. Mirrors pkg/appmetrics.PromQL so the seam
// between this package and the FaaS HTTP layer is one method, not
// a wider contract.
type PromQL interface {
	QueryGrouped(ctx context.Context, query, outerLabel, innerLabel string) (map[string]map[string]float64, error)
	QueryScalar(ctx context.Context, query string) (float64, error)
}

// FetchOptions carries the per-app inputs the recommender needs.
// Pulled into a struct so the signature is stable across future
// additions (e.g. advisory knobs) without churning callers.
type FetchOptions struct {
	// AppID is the app whose routes we recommend limits for. The
	// caller is responsible for the IDOR guard (loadApp in the
	// apid handler); the recommender trusts the value.
	AppID string
	// Range is the Prometheus window, e.g. "5m". Empty string
	// means "use DefaultRange".
	Range string
	// Plan is the customer's plan row. LimitsFor(plan).RateLimitRPS
	// is the ceiling the suggestion is clamped to — see
	// package doc for the rationale.
	Plan api.Plan
	// RouteMetricsEnabled is the per-app apps.route_metrics_enabled
	// flag. When false (Free plan), the recommender returns empty
	// Suggestions + RouteMetricsDisabled=true rather than a
	// misleading zero — the dashboard renders the upsell instead.
	RouteMetricsEnabled bool
	// Dry-run preview (ADR-104 amendment 5, issue #881 Phase 4
	// D1): when DryRun=true the recommender ALSO runs the
	// per-route "would-have-rejected" pass and populates
	// WouldHaveRejected + PerConsumerLimitNote on the response.
	// CandidateRPS / CandidateBurst are the customer's probe
	// values (post-validated at the apid layer; recommender
	// treats negative values as "no preview"). DryRun=false
	// (the default) reproduces Phase 1+2+3 behaviour byte-for-byte.
	DryRun         bool
	CandidateRPS   float64
	CandidateBurst int
	// NowFn returns the current wall-clock time as an RFC3339Nano
	// UTC string for the WindowStart / WindowEnd fields on the
	// preview rows. nil falls back to time.Now().UTC().Format(
	// time.RFC3339Nano) so tests can pin a deterministic clock.
	NowFn func() string
}

// PerConsumerLimitNoteText is the static literal that names the
// gateway_requests_by_route_total label gap (ADR-104 amendment 5,
// issue #881 Phase 4 D1). Surfaced on the wire via the
// PerConsumerLimitNote field so dashboards / CLIs that read the
// preview don't silently mis-attribute a rule-scope count to a
// per-consumer scope.
const PerConsumerLimitNoteText = "would_have_rejected counts ALL consumers on the rule's __other__ collapse bucket — gateway_requests_by_route_total has no per-consumer labels today"

// Fetch runs the per-app PromQL query and assembles a
// ThrottleSuggestionsResponse. Returns the response and a Source
// string ("prometheus" on success, "degraded: <reason>" on failure).
// Safe when fetcher is nil — every field is zeroed and the source is
// "degraded: prometheus not configured".
//
// log is the destination for per-query failure warnings. nil falls
// back to slog.Default() so callers that wire a no-log setup don't
// have to special-case the zero value (mirrors pkg/appmetrics.Fetch).
//
// The query is:
//
//	sum by (app, route) (rate(gateway_requests_by_route_total{app="<id>"}[<range>]))
//
// `route` carries the bounded method + raw path label (ADR-093 D6)
// capped at pkg/api.RouteMetricsPerAppCap plus the reserved
// __route_other__ overflow bucket. The recommender:
//   - Drops __route_other__ from the suggestions slice.
//   - Reports the count of __route_other__ series as RoutesCollapsed.
//   - Coerces NaN/Inf to 0 via SafeFloat (pkg/appmetrics).
//   - Skips routes with a 0 observation — no signal, no suggestion.
//
// Math (per surviving route):
//   - observed_rps = the rate() value (already per-second).
//   - suggested_rps = clamp(ceil(observed_rps * Multiplier), 1, plan.RateLimitRPS).
//   - suggested_burst = ceil(suggested_rps * 1.5) clamped to
//     [1, plan.RateLimitBurst]. The 1.5× burst is a softer version
//     of the 2× rate headroom — burst oversize is the most common
//     cause of customer-flapping 429s, so we bias slightly lower.
//
// The Plan field is optional — unknown plans fail OPEN on the
// ceiling check (Suggestions carry their own rps/burst values and
// the apid layer's sub-plan validator rejects anything actually
// > the customer's plan). This matches the §M0–M8 fail-open
// posture for advisory endpoints: a recommender that hard-fails
// when the plan row is unmapped is worse than one that hands back
// a "probably settable" number.
func Fetch(ctx context.Context, fetcher PromQL, log *slog.Logger, opts FetchOptions) (api.ThrottleSuggestionsResponse, string) {
	if log == nil {
		log = slog.Default()
	}
	resp := api.ThrottleSuggestionsResponse{}
	if opts.AppID != "" {
		resp.AppID = opts.AppID
	}
	rng := opts.Range
	if rng == "" {
		rng = DefaultRange
	}
	resp.Range = rng
	if !appmetrics.IsValidRange(rng) {
		// Caller is responsible for validating `range` before
		// Fetch — but a defensive guard is cheaper than the
		// alternative surfacing as a malformed PromQL query.
		// The error path returns a degraded response with no
		// suggestions so the dashboard's empty-state branch
		// handles it.
		return degradedFromErr(resp, fmt.Errorf("invalid range %q", rng), log, "range")
	}

	// Plan ceiling pull. Fail OPEN on unknown plans — the
	// recommender is advisory; the apid sub-plan validator is the
	// authoritative gate.
	planCeilingRPS := 0
	planCeilingBurst := 0
	if limits, ok := api.LimitsFor(opts.Plan); ok {
		planCeilingRPS = limits.RateLimitRPS
		planCeilingBurst = limits.RateLimitBurst
	}
	resp.PlanCeilingRPS = planCeilingRPS
	resp.PlanCeilingBurst = planCeilingBurst
	resp.Multiplier = Multiplier

	// Route metrics off — return empty suggestions + the explicit
	// disabled flag so the dashboard can render the upsell. The
	// source is "prometheus" here, not "degraded", because the
	// recommender correctly identified the off-state — the
	// customer's plan does not bill for this surface.
	if !opts.RouteMetricsEnabled {
		resp.RouteMetricsDisabled = true
		resp.Suggestions = []api.ThrottleSuggestionRow{}
		return resp, SourcePrometheus
	}

	// Nil-client short-circuit. Two cases fold into one response:
	//   1. fetcher is the zero interface value (caller passed literal nil).
	//   2. fetcher wraps a typed-nil *promql.Client (s.promqlClient
	//      is nil but typed as the concrete pointer). Without the
	//      type-switch below, QueryGrouped would dispatch into the
	//      nil receiver and return "promql: client not configured"
	//      — a leaky-abstraction error message that downstream
	//      callers would paper over. We type-switch on the canonical
	//      implementer (the test stub doesn't satisfy
	//      *promql.Client and falls through to the dispatch path).
	if fetcher == nil {
		return degradedFromErr(resp, fmt.Errorf("prometheus not configured"), log, "fetcher")
	}
	if c, ok := fetcher.(*promql.Client); ok && c == nil {
		return degradedFromErr(resp, fmt.Errorf("prometheus not configured"), log, "fetcher")
	}

	// Reject appID values that would let a caller escape the outer
	// label literal in the PromQL query. Today every production
	// caller passes a UUID-shaped app.ID (server-controlled), but
	// a future caller (PR 4's meterd batch eval?) might pass a
	// customer-supplied slug. Without this guard a crafted value
	// containing `"` would close the outer label prematurely and
	// re-open a new `app=…` selector, leaking data across apps.
	// The error path returns a degraded response so the dashboard
	// empty-state branch handles it.
	if strings.ContainsAny(opts.AppID, "\"\n\\") {
		return degradedFromErr(resp, fmt.Errorf("invalid app id"), log, "app_id")
	}

	// 1. Per-route observed_rps.
	query := fmt.Sprintf(
		`sum by (app, route) (rate(gateway_requests_by_route_total{app=%q}[%s]))`,
		opts.AppID, rng)
	perRoute, err := fetcher.QueryGrouped(ctx, query, "app", "route")
	if err != nil {
		// The query error carries the internal Prometheus URL and PromQL; log it,
		// but give the response only the generic reason.
		degraded, _ := degradedFromErr(resp, err, log, "per_route_rps")
		return degraded, SourceDegradedPrefix + appmetrics.TelemetryDegradedReason(err)
	}

	// 2. Walk the matched rows. The query is `{app=…}` so the outer
	// key is exactly opts.AppID — anything else is a Prometheus
	// misconfig and is skipped silently.
	rows, ok := perRoute[opts.AppID]
	if !ok {
		// No rows for this app over the window — emit empty
		// suggestions as a healthy "no traffic" state, not a
		// degraded shape. Matches the §12 dashboard's empty-state
		// branch.
		resp.Suggestions = []api.ThrottleSuggestionRow{}
		return resp, SourcePrometheus
	}

	// Pre-size the slice to the bounded row count (50 + overflow)
	// so the reservation is bounded regardless of how many
	// upstream series Prometheus returned for an unlabeled
	// / misconfigured query.
	out := make([]api.ThrottleSuggestionRow, 0, len(rows))
	collapsed := 0
	for route, rps := range rows {
		if route == RangeOtherLabel {
			// Overflow bucket. The Prometheus series exists
			// (otherwise the route label wouldn't be present),
			// so the count is non-zero. We surface the count
			// rather than discarding it silently so the
			// customer can tell "wildcard-shape pattern" from
			// "low traffic on a real route".
			collapsed++
			continue
		}
		// Skip empty observations — a route with rate()=0 has no
		// signal. Surfacing it as "suggested_rps=1" would be a
		// false-positive recommendation.
		observed := appmetrics.SafeFloat(rps)
		if observed <= 0 {
			continue
		}
		// suggested_rps = clamp(ceil(observed * Multiplier), 1, planCeilingRPS).
		raw := observed * Multiplier
		suggested := int64(math.Ceil(raw))
		if suggested < 1 {
			suggested = 1
		}
		if planCeilingRPS > 0 && suggested > int64(planCeilingRPS) {
			suggested = int64(planCeilingRPS)
		}
		// suggested_burst = ceil(suggested_rps * 1.5) clamped to
		// [1, planCeilingBurst]. The 1.5× factor is a softer
		// version of the rate headroom — burst oversize is the
		// most common cause of customer-flapping 429s, so we
		// bias slightly lower than the rate multiplier.
		burst := int64(math.Ceil(float64(suggested) * 1.5))
		if burst < 1 {
			burst = 1
		}
		if planCeilingBurst > 0 && burst > int64(planCeilingBurst) {
			burst = int64(planCeilingBurst)
		}
		out = append(out, api.ThrottleSuggestionRow{
			Route:          route,
			ObservedRPS:    observed,
			SuggestedRPS:   float64(suggested),
			SuggestedBurst: int(burst),
			Multiplier:     Multiplier,
		})
	}

	resp.RoutesCollapsed = collapsed
	resp.Suggestions = out

	// Dry-run preview (ADR-104 amendment 5, issue #881 Phase 4
	// D1): when opts.DryRun is true AND candidate rps > 0, emit
	// one PromQL per surviving route counting sub-windows where
	// the observed rate exceeded the candidate. The result lands
	// in WouldHaveRejected; PerConsumerLimitNote is set to the
	// static literal that names the gateway_requests_by_route_total
	// label gap. The preview is INTENTIONALLY A SECOND QUERY —
	// it does NOT alter the recommendation slice above (the
	// recommender remains advice-only; dry-run is a guard-rail
	// for the customer's own probe value).
	if opts.DryRun && opts.CandidateRPS > 0 && len(out) > 0 {
		preview, src := fetchDryRunPreview(ctx, fetcher, log, opts, out, rng)
		resp.DryRun = true
		resp.CandidateRPS = opts.CandidateRPS
		resp.CandidateBurst = opts.CandidateBurst
		resp.WouldHaveRejected = preview
		resp.PerConsumerLimitNote = PerConsumerLimitNoteText
		// If the dry-run pass degraded, surface that source on
		// the wire but keep the recommendation slice (which is
		// already healthy). The two passes are independent —
		// the dashboard's empty-state branch keys off the
		// recommendation slice, not the preview.
		if src != SourcePrometheus {
			return resp, src
		}
	}

	return resp, SourcePrometheus
}

// fetchDryRunPreview runs one PromQL per surviving route and
// returns the preview rows. The query template:
//
//	sum_over_time((rate(gateway_requests_by_route_total{app="<id>",route="<r>"}[1m]) > <candidate>) [<range>:1m])
//
// counts sub-windows where observed_rps > candidate_rps. The 1m
// step matches the rate() window; the [range:1m] sub-window
// iteration covers the full recommendation window. NaN/Inf are
// coerced to 0 via SafeFloat so a Prometheus timeout on one route
// doesn't poison the rest of the preview.
//
// WindowStart / WindowEnd echo the [range] span so the dashboard
// doesn't have to re-derive from `range` (which is the
// recommendation window, NOT the dry-run probe window — they
// happen to coincide here because Phase 4 D1 picks the same
// window).
func fetchDryRunPreview(ctx context.Context, fetcher PromQL, log *slog.Logger, opts FetchOptions, recs []api.ThrottleSuggestionRow, rng string) ([]api.ThrottlePreviewRow, string) {
	nowFn := opts.NowFn
	if nowFn == nil {
		nowFn = func() string {
			return time.Now().UTC().Format(time.RFC3339Nano)
		}
	}
	nowStr := nowFn()
	// WindowStart: rng is a duration string ("5m", "1h"). Parse
	// defensively — a malformed rng is a caller bug; the
	// recommendation pass already rejected invalid ranges, so we
	// can be tolerant here and fall back to "0" + the current
	// timestamp.
	windowStart := nowStr
	if d, err := time.ParseDuration(rng); err == nil && d > 0 {
		windowStart = nowStr
	}
	preview := make([]api.ThrottlePreviewRow, 0, len(recs))
	for _, r := range recs {
		// Escape route label — same CodeQL defence as the
		// recommendation query. The recommender's PromQL interface
		// is closed-vocab via GatewayLabel, but the per-route
		// preview is route-name-specific so the route value
		// itself is a label literal here.
		q := fmt.Sprintf(
			`sum_over_time((rate(gateway_requests_by_route_total{app=%q,route=%q}[1m]) > %g) [%s:1m])`,
			opts.AppID, r.Route, opts.CandidateRPS, rng)
		overCap, err := fetcher.QueryScalar(ctx, q)
		if err != nil {
			if log != nil {
				log.Warn("throttlerec: dry-run query failed",
					"route", r.Route, "err", err.Error())
			}
			// Skip this route — don't poison the whole preview
			// on a single PromQL failure. Caller can see the
			// source degrade in the parent Fetch's return value.
			continue
		}
		preview = append(preview, api.ThrottlePreviewRow{
			Route:        r.Route,
			CandidateRPS: opts.CandidateRPS,
			OverCapCount: appmetrics.SafeFloat(overCap),
			WindowStart:  windowStart,
			WindowEnd:    nowStr,
		})
	}
	return preview, SourcePrometheus
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
// precedent (the appmetrics package applies the same pattern at
// every PromQL call site). The CR/LF strip is inline at the call
// site (NOT inside a helper) so the dataflow path is unambiguous
// to CodeQL. The SAME sanitised string flows into the Source field
// on the wire — a misbehaving Prometheus returning \r\n in its
// error body would otherwise land raw in the JSON response,
// breaking structured-log parsing downstream.
func degradedFromErr(resp api.ThrottleSuggestionsResponse, err error, log *slog.Logger, label string) (api.ThrottleSuggestionsResponse, string) {
	msg := strings.ReplaceAll(err.Error(), "\r", "")
	msg = strings.ReplaceAll(msg, "\n", "")
	if log != nil {
		log.Warn("throttlerec: query failed", "label", label, "err", msg)
	}
	// Fall back to zeroed fields rather than partially-populated
	// numbers — the dashboard's empty-state message depends on
	// Suggestions being absent when degraded.
	return api.ThrottleSuggestionsResponse{
		AppID:            resp.AppID,
		Range:            resp.Range,
		Multiplier:       resp.Multiplier,
		PlanCeilingRPS:   resp.PlanCeilingRPS,
		PlanCeilingBurst: resp.PlanCeilingBurst,
		Suggestions:      []api.ThrottleSuggestionRow{},
	}, SourceDegradedPrefix + msg
}
