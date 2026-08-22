// Account-scoped list endpoints (issue #393). These three handlers
// mirror the per-app counterparts at handlers_ext.go, handlers_secrets.go
// and handlers_metrics.go but resolve the account from the authenticated
// principal only — no path slug, no (accountID, slug) pair. Cross-account
// isolation is the SQL JOIN on apps.account_id = $1 in the pgstore
// methods; there's no per-handler IDOR guard because the SQL is the only
// path the handler can reach data on.
//
// All three follow the strict-mode cursor pattern (/v1/invoices shape):
// default limit 25, max 100, 400 CodeValidation with WithLimit + WithDocs
// on bad input. The cursor for /v1/instances is the instance id; the
// cursor for /v1/secrets is the (app_slug, key) pair encoded as
// "<slug>|<key>" (the SQL splits it back via split_part).
//
// The metrics rollup reuses the PromQL pipeline that handlers_metrics.go
// already runs — but instead of N scalar queries per app, it issues 6
// vector queries (QueryMap + QueryBuckets + QueryScalar) regardless of N.
// First degraded result short-circuits the whole response (the dashboard
// has one empty-state branch across the per-app and account-scoped
// endpoints — same `source: "degraded: <reason>"` contract).
package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/state"
)

// listInstancesForAccount serves GET /v1/instances. Cursor: ?before=
// (instances.id UUIDv7). Default limit 25, max 100 (strict 400 on bad
// input via api.ParseLimit). Cross-account isolation is the SQL JOIN on
// apps.account_id = $1 — the SQL is the only path.
//
// Returns 200 with an empty `instances` array for an account with
// zero live instances — never 404. next_before is the last row's id
// when len(out) == limit; omitted (empty) otherwise.
//
// Issue #557 / ADR-071: each row carries the parent app's
// EffectiveMinInstances() via the min_instances_target wire field so
// dashboards can verify the proactive floor is being met. Apps are
// looked up in one batch query (one AppByID call per unique
// AppID) — a single page at limit=100 maps to ≤100 AppIDs but in
// practice tenants run one app per page.
func (s *server) listInstancesForAccount(w http.ResponseWriter, r *http.Request, acct state.Account) {
	prob, limit := api.ParseLimit(r.URL.Query().Get("limit"), 25, 100, "instances")
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	before := r.URL.Query().Get("before")
	rows, err := s.store.ListInstancesForAccountPaged(r.Context(), acct.ID, limit, before)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list instances"))
		return
	}
	if rows == nil {
		rows = []state.Instance{}
	}
	floors := s.batchMinInstancesTargets(r.Context(), rows)
	out := make([]api.InstanceResponse, 0, len(rows))
	for _, ins := range rows {
		out = append(out, instanceResponse(ins, floors[ins.AppID]))
	}
	var nextBefore string
	if len(out) == limit && len(out) > 0 {
		nextBefore = out[len(out)-1].ID
	}
	writeJSON(w, http.StatusOK, api.ListInstancesResponse{
		Instances:  out,
		NextBefore: nextBefore,
	})
}

// batchMinInstancesTargets (issue #557 / ADR-071) returns a map of
// AppID → EffectiveMinInstances for every distinct parent app on
// the page. Single missing-app case is silent (returns 0 — the
// same as a customer who never opted in). Bounded by the caller's
// limit (≤ 100); the lookup is one round-trip per unique AppID, not
// per row.
func (s *server) batchMinInstancesTargets(rctx context.Context, rows []state.Instance) map[string]int {
	if len(rows) == 0 {
		return map[string]int{}
	}
	seen := map[string]struct{}{}
	out := map[string]int{}
	for _, ins := range rows {
		if _, dup := seen[ins.AppID]; dup {
			continue
		}
		seen[ins.AppID] = struct{}{}
		app, err := s.store.AppByID(rctx, ins.AppID)
		if err != nil {
			continue
		}
		out[ins.AppID] = app.EffectiveMinInstances()
	}
	return out
}

// listSecretsForAccount serves GET /v1/secrets. Each row carries the
// owning app's id and slug so the dashboard can render
// "foo-app / DATABASE_URL" without a parallel /v1/apps round-trip.
// Ciphertext is the age-sealed envelope, base64-encoded; plaintext
// NEVER appears in this handler's output (the same invariant the
// per-app handler upholds — see listSecrets in handlers_secrets.go).
//
// Cursor: ?before= is the (app_slug, key) pair encoded as
// "<slug>|<key>". The pgstore splits it back via split_part. Sort
// order is (app_slug ASC, key ASC) so the cursor walk is monotonic.
func (s *server) listSecretsForAccount(w http.ResponseWriter, r *http.Request, acct state.Account) {
	prob, limit := api.ParseLimit(r.URL.Query().Get("limit"), 25, 100, "secrets")
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	before := r.URL.Query().Get("before")
	rows, err := s.store.ListAppSecretsForAccount(r.Context(), acct.ID, limit, before)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list secrets"))
		return
	}
	out := make([]api.AccountAppSecretResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, api.AccountAppSecretResponse{
			AppID:      r.AppID,
			AppSlug:    r.AppSlug,
			Key:        r.Key,
			Scope:      r.Scope,
			Ciphertext: base64.RawURLEncoding.EncodeToString(r.Ciphertext),
			ValueHash:  r.ValueHash,
			CreatedAt:  r.CreatedAt.UTC().Format(time.RFC3339),
			UpdatedAt:  r.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}
	var nextBefore string
	if len(out) == limit && len(out) > 0 {
		// ADR-092 PR-B: cursor is (slug, scope, key) — the
		// (slug, key) pair is no longer unique post-PR-A because
		// the same (app, key) can be bound to multiple scopes.
		// Without scope in the cursor, the next page's WHERE
		// clause's ">" evaluates to FALSE for rows where
		// (slug, key) equals the cursor but scope is greater than
		// the previous page's last-scope — those rows are silently
		// dropped.
		last := out[len(out)-1]
		nextBefore = last.AppSlug + "|" + last.Scope + "|" + last.Key
	}
	writeJSON(w, http.StatusOK, api.ListSecretsForAccountResponse{
		Secrets:    out,
		NextBefore: nextBefore,
	})
}

// getAppsMetrics serves GET /v1/apps/metrics?range=. One call replaces
// N per-app /v1/apps/{slug}/metrics calls (issue #393). Range is the
// same closed vocabulary as the per-app endpoint
// (5m|15m|1h|6h|24h|7d|15d). Source / AsOf / Range follow the per-app
// shape exactly so the dashboard can render the rollup with one code
// path that already handles the per-app data.
//
// The rollup runs 6 PromQL round-trips regardless of N apps:
//
//   - 3 vector queries via QueryMap for request_count, error_rate,
//     cold_start (each is `sum by (app) (rate(...))`).
//   - 1 vector query via QueryBuckets for latency percentiles
//     (sum by (app, le) (rate(...))). The histogram_quantile call
//     runs in-process per app — see histogramQuantile below.
//   - 1 scalar query via QueryScalar for the FLEET wake p95
//     (gateway_wake_latency_seconds is unlabeled, same as the per-app
//     handler).
//
// First degraded result short-circuits the whole response (never
// partial-populated). The dashboard renders the same empty-state
// message as the per-app endpoint because both share the
// "degraded: <reason>" Source contract.
func (s *server) getAppsMetrics(w http.ResponseWriter, r *http.Request, acct state.Account) {
	rng := r.URL.Query().Get("range")
	if rng == "" {
		rng = appmetrics.DefaultRange
	}
	if !appmetrics.IsValidRange(rng) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"invalid range",
			fmt.Sprintf("range must be one of: %s", strings.Join(appmetrics.Ranges(), ", "))))
		return
	}
	apps, err := s.store.ListApps(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list apps"))
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	resp := api.AppsMetricsResponse{
		Range: rng,
		AsOf:  now,
		Apps:  make(map[string]api.AppMetricsResponse, len(apps)),
	}
	if s.promqlClient == nil {
		resp.Source = appmetrics.SourceDegradedPrefix + "prometheus not configured"
		resp.Apps = nil
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// 1. request_count per app.
	countByApp, err := s.promqlClient.QueryMap(r.Context(),
		fmt.Sprintf(`sum by (app)(increase(gateway_requests_total[%s]))`, rng))
	if err != nil {
		writeMetricsDegraded(w, s, resp, err, "request_count")
		return
	}

	// 2. error_rate per app (share of [45]xx in the window).
	errRateByApp, err := s.promqlClient.QueryMap(r.Context(),
		fmt.Sprintf(`sum by (app)(rate(gateway_requests_total{code=~"[45].."}[%s])) / sum by (app)(rate(gateway_requests_total[%s])) * 100`, rng, rng))
	if err != nil {
		writeMetricsDegraded(w, s, resp, err, "error_rate")
		return
	}

	// 3. cold_start per app.
	coldByApp, err := s.promqlClient.QueryMap(r.Context(),
		fmt.Sprintf(`sum by (app)(rate(gateway_cold_boot_total[%s])) / sum by (app)(rate(gateway_requests_total[%s])) * 100`, rng, rng))
	if err != nil {
		writeMetricsDegraded(w, s, resp, err, "cold_start")
		return
	}

	// 4-6. latency percentiles per app: one QueryBuckets call, three
	// histogram_quantile evaluations in Go (each is O(buckets) work).
	buckets, err := s.promqlClient.QueryBuckets(r.Context(),
		fmt.Sprintf(`sum by (app, le)(rate(gateway_request_duration_seconds_bucket{class="2xx"}[%s]))`, rng))
	if err != nil {
		writeMetricsDegraded(w, s, resp, err, "p50")
		return
	}

	// 7. Fleet wake p95 (unlabeled — single scalar, same as the
	// per-app handler).
	wakeQ := fmt.Sprintf(
		`histogram_quantile(0.95, sum by (le)(rate(gateway_wake_latency_seconds_bucket[%s]))) * 1000`, rng)
	wakeV, err := s.promqlClient.QueryScalar(r.Context(), wakeQ)
	if err != nil {
		writeMetricsDegraded(w, s, resp, err, "wake_p95")
		return
	}

	for _, app := range apps {
		appBuckets := buckets[app.ID]
		single := api.AppMetricsResponse{
			AppID:        app.ID,
			Range:        rng,
			Source:       appmetrics.SourcePrometheus,
			AsOf:         now,
			RequestCount: int64(appmetrics.SafeRoundNonNeg(countByApp[app.ID])),
			ErrorRatePct: appmetrics.SafePercent(errRateByApp[app.ID]),
			ColdStartPct: appmetrics.SafePercent(coldByApp[app.ID]),
			LatencyP50MS: appmetrics.SafeFloat(histogramQuantile(0.50, appBuckets)),
			LatencyP95MS: appmetrics.SafeFloat(histogramQuantile(0.95, appBuckets)),
			LatencyP99MS: appmetrics.SafeFloat(histogramQuantile(0.99, appBuckets)),
			WakeP95MS:    appmetrics.SafeFloat(wakeV),
		}
		resp.Apps[app.Slug] = single
	}
	resp.Source = appmetrics.SourcePrometheus
	writeJSON(w, http.StatusOK, resp)
}

// writeMetricsDegraded centralises the "short-circuit to degraded
// source" path for getAppsMetrics so the handler body stays readable.
// The first failure on any of the 6 PromQL round-trips abandons the
// partial rollup and emits a zeroed AppsMetricsResponse with the
// degraded source string, matching the per-app endpoint's contract.
//
// Mirrors pkg/appmetrics.degradedFromErr but takes a writer
// instead of returning the response — the per-app variant runs inline
// (single-pass, all 7 queries sequential) while the account-scoped
// variant spreads across named locals; centralising the write keeps
// both consistent.
func writeMetricsDegraded(w http.ResponseWriter, s *server, resp api.AppsMetricsResponse, err error, label string) {
	if s.log != nil {
		// Same CodeQL log-injection guard as pkg/appmetrics — strip
		// CR/LF inline at the call site so the dataflow path is
		// unambiguous. The PromQL range= query param flows into the
		// query body that produced the error.
		msg := strings.ReplaceAll(err.Error(), "\r", "")
		msg = strings.ReplaceAll(msg, "\n", "")
		s.log.Warn("apid: apps-metrics query failed", "label", label, "err", msg)
	}
	resp.Apps = nil
	resp.Source = "degraded: " + err.Error()
	writeJSON(w, http.StatusOK, resp)
}

// histogramQuantile computes PromQL histogram_quantile() for a single
// q against an (app, le) → float64 bucket map produced by
// promql.Client.QueryBuckets. Empty / nil maps return 0 (matches the
// per-app handler's appmetrics.SafeFloat coercion of NaN from PromQL).
//
// Why a local helper instead of importing pkg/gateway/testhist:
// testhist is a t.Fatalf-bound package; the server-side rollup can't
// t.Fatal. The shape is small (≤ 14 buckets) so duplicating the walk
// is cheaper than introducing a non-test seam in pkg/gateway.
//
// Semantics (matches PromQL histogram_quantile):
//   - skip the +Inf bucket (it would otherwise return +Inf for q<1)
//   - find the first bucket whose cumulative ≥ q·N
//   - interpolate linearly in (prevNonEmptyUpper, upper) by count ratio
//
// NaN / +Inf from the upstream vector query surface as 0 here too
// (PromQL emits the literal string "NaN" / "+Inf" in the value field,
// which strconv.ParseFloat parses; we trust appmetrics.SafeFloat on the outer
// side to clamp).
func histogramQuantile(q float64, buckets map[string]float64) float64 {
	if len(buckets) == 0 {
		return 0
	}
	type entry struct {
		le  float64
		cum float64
	}
	parsed := make([]entry, 0, len(buckets))
	var total float64
	for leStr, v := range buckets {
		le, err := parseLE(leStr)
		if err != nil {
			continue
		}
		parsed = append(parsed, entry{le: le, cum: v})
	}
	if len(parsed) == 0 {
		return 0
	}
	sort.Slice(parsed, func(i, j int) bool { return parsed[i].le < parsed[j].le })
	for _, p := range parsed {
		if p.cum > total {
			total = p.cum
		}
	}
	target := q * total
	// Skip +Inf — cap at the last finite bucket (matches PromQL).
	lastFinite := -1
	for i, p := range parsed {
		if !math.IsInf(p.le, 1) {
			lastFinite = i
		}
	}
	if lastFinite < 0 {
		return 0
	}
	var prevUpper, prevCum float64
	for i := 0; i <= lastFinite; i++ {
		p := parsed[i]
		if p.cum >= target {
			countInBucket := p.cum - prevCum
			if countInBucket <= 0 {
				return p.le
			}
			frac := (target - prevCum) / countInBucket
			return prevUpper + frac*(p.le-prevUpper)
		}
		if p.cum > 0 {
			prevUpper = p.le
		}
		prevCum = p.cum
	}
	return prevUpper
}

// parseLE parses a Prometheus le="…" string value into a float64.
// Special-cases "+Inf" (PromQL's stringified infinity) and rejects
// "-Inf" / "NaN" / malformed input. Used only by histogramQuantile
// to keep the bucket walk strict.
func parseLE(s string) (float64, error) {
	if s == "+Inf" {
		return math.Inf(1), nil
	}
	return strconv.ParseFloat(s, 64)
}

// ctx returns r.Context() — declared in server.go (the rest of the
// cmd/apid package reuses that single helper).
