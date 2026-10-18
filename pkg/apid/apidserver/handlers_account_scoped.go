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
// already runs — but instead of N scalar queries per app, it issues one
// bounded account snapshot regardless of N.
// First degraded result short-circuits the whole response (the dashboard
// has one empty-state branch across the per-app and account-scoped
// endpoints — same `source: "degraded: <reason>"` contract).
package apidserver

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/state"
)

// listInstancesForAccount serves GET /v1/instances. Cursor: ?before=
// (instances.id). Default limit 25, max 100 (strict 400 on bad
// input via api.ParseLimit). Cross-account isolation is the SQL JOIN on
// apps.account_id = $1 — the SQL is the only path.
//
// Returns only waking, cold_booting, running, and snapshotting instances.
// An account with zero live instances gets a 200 with an empty array — never
// 404. next_before is the last row's id
// when len(out) == limit; omitted (empty) otherwise.
//
// Issue #557 / ADR-071: each row carries the parent app's
// EffectiveMinInstances() via the min_instances_target wire field so
// dashboards can verify the proactive floor is being met. Apps are
// looked up in one batch query (one AppByID call per unique
// AppID) — a single page at limit=100 maps to ≤100 AppIDs but in
// practice tenants run one app per page.
func (s *server) listInstancesForAccount(w http.ResponseWriter, r *http.Request, acct state.Account) {
	requestCtx := r.Context()
	prob, limit := api.ParseLimit(r.URL.Query().Get("limit"), 25, 100, "instances")
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	before := r.URL.Query().Get("before")
	rows, nextBefore, err := appaccess.VisiblePage(requestCtx, before, limit,
		func(cursor string) ([]state.Instance, error) {
			return s.store.ListInstancesForAccountPaged(requestCtx, acct.ID, limit, cursor)
		},
		func(rows []state.Instance) ([]state.Instance, error) {
			return appaccess.VisibleResources(r, s.store, actorAccount(r, acct), rows, func(row state.Instance) string { return row.AppID })
		},
		func(row state.Instance) string { return row.ID })
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list instances"))
		return
	}
	if rows == nil {
		rows = []state.Instance{}
	}
	floors := s.batchMinInstancesTargets(requestCtx, rows)
	out := make([]api.InstanceResponse, 0, len(rows))
	for _, ins := range rows {
		out = append(out, instanceResponse(ins, floors[ins.AppID]))
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
	requestCtx := r.Context()
	prob, limit := api.ParseLimit(r.URL.Query().Get("limit"), 25, api.SecretsListPageMax, "secrets")
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	before := r.URL.Query().Get("before")
	rows, nextBefore, err := appaccess.VisiblePage(requestCtx, before, limit,
		func(cursor string) ([]state.AccountAppSecret, error) {
			return s.store.ListAppSecretsForAccount(requestCtx, acct.ID, limit, cursor)
		},
		func(rows []state.AccountAppSecret) ([]state.AccountAppSecret, error) {
			return appaccess.VisibleResources(r, s.store, actorAccount(r, acct), rows, func(row state.AccountAppSecret) string { return row.AppID })
		},
		func(row state.AccountAppSecret) string { return row.AppSlug + "|" + row.Scope + "|" + row.Key })
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list secrets"))
		return
	}
	out := make([]api.AccountAppSecretResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, api.AccountAppSecretResponse{
			AppID:       r.AppID,
			AppSlug:     r.AppSlug,
			Key:         r.Key,
			Scope:       r.Scope,
			SecretClass: r.SecretClass,
			Ciphertext:  base64.RawURLEncoding.EncodeToString(r.Ciphertext),
			ValueHash:   r.ValueHash,
			CreatedAt:   r.CreatedAt.UTC().Format(time.RFC3339),
			UpdatedAt:   r.UpdatedAt.UTC().Format(time.RFC3339),
		})
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
// The rollup runs 4 PromQL round-trips regardless of N apps:
//
//   - 1 vector query grouped by (app, class) supplies request_count and
//     error_rate without rescanning the same counters.
//   - 1 vector query supplies cold_start counts.
//   - 1 vector query via QueryBuckets supplies latency percentiles
//     (sum by (app, le) (increase(...))). The histogram_quantile call
//     runs in-process per app — see histogramQuantile below.
//   - 1 scalar query via QueryScalar for the FLEET wake p95
//     (gateway_wake_latency_seconds is unlabeled, same as the per-app
//     handler).
//
// Every tenant-bearing selector is constrained to the account's closed app
// set. The old global selectors scanned every customer and timed out on 24h
// windows as the beta fleet grew.
//
// First degraded result short-circuits the whole response (never
// partial-populated). The dashboard renders the same empty-state
// message as the per-app endpoint because both share the
// "degraded: <reason>" Source contract.
func (s *server) getAppsMetrics(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !acct.Plan.PerAppMetricsAllowed() {
		api.WriteProblem(w, api.ErrPlanPerAppMetricsNotAllowed(acct.Plan))
		return
	}
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
	if err == nil {
		apps, err = appaccess.VisibleApps(r, s.store, actorAccount(r, acct), apps)
	}
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

	appIDs := make([]string, 0, len(apps))
	for _, app := range apps {
		appIDs = append(appIDs, app.ID)
	}
	snapshot := fetchAccountMetricsSnapshot(r.Context(), s.promqlClient, appIDs, rng, false, true)
	if snapshot.requestsErr != nil {
		writeMetricsDegraded(w, s, resp, snapshot.requestsErr, "request_count")
		return
	}
	if snapshot.coldBootsErr != nil {
		writeMetricsDegraded(w, s, resp, snapshot.coldBootsErr, "cold_start")
		return
	}
	if snapshot.latencyErr != nil {
		writeMetricsDegraded(w, s, resp, snapshot.latencyErr, "p50")
		return
	}
	if snapshot.wakeErr != nil {
		writeMetricsDegraded(w, s, resp, snapshot.wakeErr, "wake_p95")
		return
	}

	for _, app := range apps {
		requests := snapshot.requestsByApp[app.ID]
		appBuckets := snapshot.latencyBucketsByApp[app.ID]
		single := api.AppMetricsResponse{
			AppID:        app.ID,
			Range:        rng,
			Source:       appmetrics.SourcePrometheus,
			AsOf:         now,
			RequestCount: int64(appmetrics.SafeRoundNonNeg(requests)),
			ErrorRatePct: appmetrics.SafePercent(percentOf(snapshot.errorsByApp[app.ID], snapshot.eligibleByApp[app.ID])),
			ColdStartPct: appmetrics.SafePercent(percentOf(snapshot.coldBootsByApp[app.ID], requests)),
			LatencyP50MS: appmetrics.SafeFloat(histogramQuantile(0.50, appBuckets) * 1000),
			LatencyP95MS: appmetrics.SafeFloat(histogramQuantile(0.95, appBuckets) * 1000),
			LatencyP99MS: appmetrics.SafeFloat(histogramQuantile(0.99, appBuckets) * 1000),
			WakeP95MS:    appmetrics.SafeFloat(snapshot.wakeP95MS),
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
	resp.Source = appmetrics.SourceDegradedPrefix + appmetrics.TelemetryDegradedReason(err)
	writeJSON(w, http.StatusOK, resp)
}

// histogramQuantile is appmetrics.HistogramQuantile, kept under its old name
// for the rollup handlers and their tests.
func histogramQuantile(q float64, buckets map[string]float64) float64 {
	return appmetrics.HistogramQuantile(q, buckets)
}

// ctx returns r.Context() — declared in server.go (the rest of the
// cmd/apid package reuses that single helper).
