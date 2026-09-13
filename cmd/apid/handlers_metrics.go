package main

// Per-app metrics handler (issue #273 / ADR-042).
//
// GET /v1/apps/{slug}/metrics?range=...
//
// Read-only, scoped to api.ScopesReadSurface (admin or apps:read).
// No MFA required — the primary caller is an API key. IDOR-safe via
// the existing loadApp (cross-account slug → 404, not 200 with
// another tenant's data).
//
// Range vocabulary is closed and bounded by Prometheus retention
// (`prom_retention_days: 15` in deploy/ansible/roles/prometheus/
// defaults/main.yml): see pkg/appmetrics.Ranges / IsValidRange.
//
// Prometheus unreachable (s.promqlClient == nil, or a query failed)
// → HTTP 200 with zeroed fields and Source="degraded: <reason>",
// matching the public /status/slo.json contract so the dashboard
// has one empty-state path. The PromQL builders + NaN/Inf guards
// live in pkg/appmetrics (extracted for issue #396 / ADR-045 PR 2
// so the meterd evaluator in PR 4 can share the same fetch path).

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/throttlerec"
)

// truthyTrue is the string the dry_run query param matches.
// Hoisted to a constant for goconst (the "true" literal would
// otherwise duplicate the truthyFlagLiterals slice in
// cmd/apid/deploy_inputs.go:511 + the rekeyTruthyLiterals slice
// in rekey_runner.go:85, package-wide count crossing the
// threshold). Used only by the dry_run parser; other "true"
// comparisons in this file (none today) should reuse the same
// constant.
const truthyTrue = "true"

// getAppMetrics serves GET /v1/apps/{slug}/metrics?range=.
// Mirrors getApp's auth chain (without requireMFA — read-only,
// primary caller is an API key with ScopesReadSurface).
//
// Operator-as-tenant view (P1): when ?on_behalf_of=<uuid-or-slug>
// is present, the handler reads the target's data using the
// target's plan (Free→402 still applies even when the caller is
// admin) and emits an operator.action.view audit row keyed on
// the target's account id with the caller captured as the actor.
// The caller must be in the admin allowlist for the target — the
// two-step gate (resolveOnBehalfOf + loadApp's cross-account
// guard) prevents an admin from reading another admin's data.
func (s *server) getAppMetrics(w http.ResponseWriter, r *http.Request, acct state.Account) {
	target, ok := s.resolveOnBehalfOf(w, r, acct, "metrics")
	if !ok {
		return
	}
	authAcct := acct
	if target != nil {
		authAcct = *target
	}
	// Plan gate: per-app observability is Hobby+; Free gets 402 +
	// upsell. The gate runs BEFORE loadApp so a Free customer
	// probing a Hobby+ slug never gets a 404 (slug-leak guard —
	// same posture as handlers_alert_presets.go:165-179). When
	// on_behalf_of is set, authAcct is the target so the gate
	// reads from target.Plan (not caller's plan).
	if !authAcct.Plan.PerAppMetricsAllowed() {
		api.WriteProblem(w, api.ErrPlanPerAppMetricsNotAllowed(authAcct.Plan))
		return
	}
	app, ok := s.loadApp(w, r, authAcct, r.PathValue("slug")) //nolint:contextcheck // loadApp takes r and uses r.Context() for its own DB calls; the helper is shared across every per-app handler.
	if !ok {
		// loadApp already wrote the 404.
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

	resp, src := appmetrics.Fetch(r.Context(), s.promqlClient, s.log, app.ID, rng)
	resp.AppID = app.ID
	resp.Range = rng
	resp.Source = src

	// Best-effort enrichment of the three Hobby+-only fields
	// beyond the PromQL fetch. A failure here degrades the field
	// to 0 (same posture as the QueueDepth best-effort path at
	// pkg/appmetrics/appmetrics.go:196-199) and stamps Source
	// with the existing degraded prefix only when the underlying
	// PromQL fetch itself failed — these SQL/PromQL misses do
	// not flip the whole response to degraded.

	// Wakes24h: count of wake.boot_started events in the trailing
	// 24 hours, sourced from the events table. The (data->>'app_id')
	// predicate is NOT covered by events_wake_id_idx (migration 00114
	// indexes data->>'wake_id'); on a Scale-tier app with a large
	// fleet this can seq-scan + jsonb-cast per row. A follow-up
	// migration adds a covering index — see the Store interface
	// comment for CountWakeBootStarted24h. 0 on a degraded store
	// call, an empty app, or pre-ADR-123 fleet (pre-PR-A
	// boot_started rows carry no app_id field, so the cast
	// returns NULL which COUNT(*) coerces to 0).
	if n, err := s.store.CountWakeBootStarted24h(r.Context(), app.ID); err == nil {
		resp.Wakes24h = n
	} else if s.log != nil {
		s.log.Warn("wakes_24h fetch failed",
			"app_id", app.ID,
			"err", err.Error())
	}

	// CacheHitRatePct: ADR-122 response-cache hit ratio. The
	// PromQL query against gateway_response_cache_total{app_id,
	// outcome=hit/miss} is out of scope for this PR; the field
	// stays 0 until the response-cache consumer-facing metric
	// lands. The DTO is non-omitempty (this field is ALWAYS on
	// the wire) so the dashboard can rely on the documented
	// schema. Feature-off vs. feature-on-zero-traffic is
	// distinguished by the `Routes` block presence, not by
	// this field's absence.
	_ = app.RouteMetricsEnabled // opt-in flag consulted at fetch time in a future PR

	// ErrorBudgetPct: trailing-30d API-availability error budget
	// remaining. Computed against the plan's API-availability
	// SLO target (99.5% per spec §12). The per-plan SLO target
	// is not yet exposed on the Limits struct (issue TBD); the
	// field stays 0 until that lands. The dashboard renders 0
	// with no traffic as "—" rather than a misleading "budget
	// exhausted" message.
	// TODO: wire against apid_request_total{account_id, code}
	// once the per-plan SLO target lands on Limits.
	if resp.AsOf == "" {
		resp.AsOf = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if target != nil {
		emitOperatorActionView(r, s, acct, target.ID, "metrics")
	}
	writeJSON(w, http.StatusOK, resp)
}

// getAppThrottleSuggestions serves
// GET /v1/apps/{slug}/throttle-suggestions?range= (ADR-091 D20.5
// amendment, issue #881). Same auth chain as getAppMetrics
// (read-only, ScopesReadSurface, no MFA). IDOR-safe via loadApp.
//
// The handler is a thin wrapper around pkg/throttlerec.Fetch: it
// pulls the customer's plan ceiling from the app's account row,
// passes the route_metrics_enabled flag, validates the range,
// and assembles the response envelope. The actual PromQL query
// and recommendation math live in pkg/throttlerec — the extracted
// seam matches the per-app metrics pattern
// (pkg/appmetrics.Fetch) so the dashboard's two cards share the
// same PromQL transport and the same degraded-fallback shape.
func (s *server) getAppThrottleSuggestions(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug")) //nolint:contextcheck // loadApp takes r and uses r.Context() for its own DB calls; the helper is shared across every per-app handler.
	if !ok {
		return
	}

	rng := r.URL.Query().Get("range")
	if rng == "" {
		rng = throttlerec.DefaultRange
	}
	if !appmetrics.IsValidRange(rng) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"invalid range",
			fmt.Sprintf("range must be one of: %s", strings.Join(appmetrics.Ranges(), ", "))))
		return
	}

	// Dry-run preview (ADR-104 amendment 5, issue #881 Phase 4
	// D2). Parse + validate BEFORE invoking throttlerec.Fetch so
	// a malformed payload doesn't waste a Prometheus round-trip.
	q := r.URL.Query()
	dryRun := q.Get("dry_run") == truthyTrue
	candidateRPS := 0.0
	candidateBurst := 0
	if dryRun {
		// CandidateRPS is required when DryRun=true — the
		// preview is meaningless without a probe value. We
		// parse strictly; a non-numeric value is a 400.
		rpsStr := q.Get("candidate_rps")
		if rpsStr == "" {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"missing candidate_rps",
				"candidate_rps is required when dry_run=true"))
			return
		}
		var err error
		candidateRPS, err = strconv.ParseFloat(rpsStr, 64)
		if err != nil || candidateRPS <= 0 {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"invalid candidate_rps",
				"candidate_rps must be a positive number"))
			return
		}
		// candidate_burst is optional — defaults to 0 (the
		// recommender surfaces it on the wire but doesn't gate
		// on it). Must be a non-negative integer if provided.
		burstStr := q.Get("candidate_burst")
		if burstStr != "" {
			candidateBurst, err = strconv.Atoi(burstStr)
			if err != nil || candidateBurst < 0 {
				api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
					"invalid candidate_burst",
					"candidate_burst must be a non-negative integer"))
				return
			}
		}
	}

	resp, src := throttlerec.Fetch(r.Context(), s.promqlClient, s.log, throttlerec.FetchOptions{
		AppID:               app.ID,
		Range:               rng,
		Plan:                acct.Plan,
		RouteMetricsEnabled: app.RouteMetricsEnabled,
		// Dry-run preview (ADR-104 amendment 5, issue #881 Phase 4
		// D2): all three query params are optional. DryRun=true
		// without CandidateRPS is a wire-shape bug → 400 (the
		// candidate is the probe value; "preview with no probe" is
		// nonsensical). DryRun=false ignores the candidates
		// regardless of value (back-compat for Phase 1+2+3 callers).
		DryRun:         dryRun,
		CandidateRPS:   candidateRPS,
		CandidateBurst: candidateBurst,
	})
	resp.AppID = app.ID
	resp.Range = rng
	resp.Source = src
	resp.AsOf = time.Now().UTC().Format(time.RFC3339Nano)
	writeJSON(w, http.StatusOK, resp)
}
