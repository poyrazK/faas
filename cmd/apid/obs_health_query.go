// obs_health_query.go — Obs-Meta + Trace-IDs Mega-PR / C7: per-metric
// computation methods for GET /v1/admin/obs/health. Extracted so the
// handler stays ≤50 lines and the PromQL / SQL calls each live behind
// a small testable seam.
//
// Source-of-truth design:
//
//   - audit_log_write_total / audit_log_write_failures_total /
//     audit_log_coverage_ratio: apid's own Prometheus counters
//     (PR #TBD / C5). Federation is out of scope; schedd's gauges
//     stay on schedd's /metrics. This endpoint answers the local
//     apid's health, not the fleet's.
//   - operator_intent_outcome_missing_total: SELECT count(*) FROM
//     operator_intents WHERE status='running' AND started_at <
//     threshold GROUP BY kind. Single SQL round-trip.
//   - trace_id_completeness_ratio: SELECT kind, count(*) FILTER
//     (WHERE trace_id IS NOT NULL)::float / count(*) FROM events
//     WHERE kind LIKE 'operator.action.%' AND at > since GROUP BY
//     kind. Reads events (live), NOT audit_log (FK-free
//     post-deletion copy) — ADR-091 §3.7.4.
//   - alerts_firing: PromQL ALERTS{alertstate="firing"} via the
//     existing promqlClient.
//
// nil-safe: if s.promqlClient is nil (single-node dev / no
// Prometheus sidecar) the PromQL-derived fields retain their stable
// numeric fallback, while the status helpers report unavailable so the
// caller cannot mistake that fallback for observed telemetry.
package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// obsHealthQueryWindow is the trailing window for every PromQL +
// SQL aggregation in /v1/admin/obs/health. 5m matches the
// existing /v1/admin/obs/audit-log/search window and the C5
// gauge tick interval (60s); the operator can still scrape this
// endpoint on a 30s cadence and see smooth tiles.
const obsHealthQueryWindow = 5 * time.Minute

// obsHealthStuckRunningThreshold is the cutoff for the
// operator_intent_outcome_missing_total tile: a row is "stuck"
// when it has been running for longer than this. Mirrors the
// schedd-side operatorIntentStuckRunningTimeout (5m, default)
// so the operator's view matches schedd's reclaim horizon —
// schedd will reclaim a row at exactly the threshold the
// operator sees it as "missing" at.
const obsHealthStuckRunningThreshold = 5 * time.Minute

// auditLogWrite5m queries the apid Prometheus for the delta of
// audit_log_write_total over the trailing 5m window. Returns 0
// when s.promqlClient is nil OR when the query fails — the
// endpoint surfaces this as "no data" rather than a 500 so a
// Prometheus outage doesn't page the on-call about an unrelated
// apid bug.
func (s *server) auditLogWrite5m(ctx context.Context) int64 {
	v, _ := s.auditLogWrite5mStatus(ctx)
	return v
}

func (s *server) auditLogWrite5mStatus(ctx context.Context) (int64, bool) {
	if s.promqlClient == nil {
		return 0, false
	}
	v, err := s.promqlClient.QueryScalar(ctx,
		`sum(increase(audit_log_write_total[5m])) or vector(0)`)
	if err != nil {
		return 0, false
	}
	return int64(v), true
}

// auditLogWriteFailures5m mirrors auditLogWrite5m for the failure
// counter. Returned as a separate field so the dashboard can
// render a failed/total ratio without a per-field subtraction.
func (s *server) auditLogWriteFailures5m(ctx context.Context) int64 {
	v, _ := s.auditLogWriteFailures5mStatus(ctx)
	return v
}

func (s *server) auditLogWriteFailures5mStatus(ctx context.Context) (int64, bool) {
	if s.promqlClient == nil {
		return 0, false
	}
	v, err := s.promqlClient.QueryScalar(ctx,
		`sum(increase(audit_log_write_failures_total[5m])) or vector(0)`)
	if err != nil {
		return 0, false
	}
	return int64(v), true
}

// auditLogCoverageRatio5m reads the trace_id coverage ratio
// directly from Prometheus (the gauge is set by the C5 schedd
// 60s tick). Returns 1.0 (vacuous truth) when the gauge has
// never been set — a fresh apid that hasn't seen any
// audit_log writes yet reports "100% covered" rather than
// "0% covered", which is the right default for an empty window.
func (s *server) auditLogCoverageRatio5m(ctx context.Context) float64 {
	v, _ := s.auditLogCoverageRatio5mStatus(ctx)
	return v
}

func (s *server) auditLogCoverageRatio5mStatus(ctx context.Context) (float64, bool) {
	if s.promqlClient == nil {
		return 1.0, false
	}
	v, err := s.promqlClient.QueryScalar(ctx,
		`sum(audit_log_coverage_ratio_5m) or vector(1)`)
	if err != nil {
		return 1.0, false
	}
	if v <= 0 {
		return 1.0, true
	}
	return v, true
}

// operatorIntentOutcomeMissing returns the per-kind count of
// stuck-running operator_intents rows. The handler seeds
// zero-count kinds from obsHealthKindVocabulary so the JSON
// shape is stable. On store error the handler surfaces 503 —
// this method only returns the partial result it managed to
// read.
func (s *server) operatorIntentOutcomeMissing(ctx context.Context) (map[string]int, error) {
	threshold := time.Now().UTC().Add(-obsHealthStuckRunningThreshold)
	return s.store.OperatorIntentOutcomeMissingCounts(ctx, threshold)
}

// traceIDCompletenessRatio returns the per-kind ratio of
// operator.action.* events with non-NULL trace_id. Reads from
// the events table (live, AppendEventWithTrace writer). The
// handler seeds zero-row kinds to 1.0 (vacuous truth) per the
// Store interface comment.
func (s *server) traceIDCompletenessRatio(ctx context.Context) (map[string]float64, error) {
	since := time.Now().UTC().Add(-obsHealthQueryWindow)
	return s.store.OperatorActionTraceCompleteness(ctx, since)
}

// alertsFiring queries Prometheus for the count of alert rules
// in the firing state. Uses the standard ALERTS series
// {alertstate="firing"} which Alertmanager emits for every
// active firing rule. Returns 0 on nil-promql or query error
// — same "no data" posture as auditLogWrite5m.
func (s *server) alertsFiring(ctx context.Context) int64 {
	v, _ := s.alertsFiringStatus(ctx)
	return v
}

func (s *server) alertsFiringStatus(ctx context.Context) (int64, bool) {
	if s.promqlClient == nil {
		return 0, false
	}
	v, err := s.promqlClient.QueryScalar(ctx,
		`count(ALERTS{alertstate="firing"}) or vector(0)`)
	if err != nil {
		return 0, false
	}
	return int64(v), true
}

// seedHealthKindCounts returns the closed-set seed map for
// counts: every kind in api.ObsHealthKindVocabulary maps to 0 so
// a fresh deploy with no operator actions yet still reports
// "force_park: 0, force_cold_boot: 0, force_restart: 0" rather
// than an empty map. The handler overlays the SQL result on
// top of this seed.
//
// Used by both the handler and the test file (via exported
// name) — the test file pins the seed shape via
// TestObsHealthHandler_StableJSONShapeOnEmptyDB.
func seedHealthKindCounts() map[string]int {
	out := make(map[string]int, len(api.ObsHealthKindVocabulary))
	for _, k := range api.ObsHealthKindVocabulary {
		out[k] = 0
	}
	return out
}

// seedHealthKindRatios mirrors seedHealthKindCounts for the
// ratios map. Same closed-set seed; the per-kind ratio
// (vacuous truth 1.0) is the right default for an empty
// window.
func seedHealthKindRatios() map[string]float64 {
	out := make(map[string]float64, len(api.ObsHealthKindVocabulary))
	for _, k := range api.ObsHealthKindVocabulary {
		out[k] = 1.0
	}
	return out
}
